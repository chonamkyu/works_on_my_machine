package handler_test

// Tests for the per-workspace reverse-proxy routes:
//   ALL /w/{ws}/d/{taskId}/*  (ServeWorkspaceDashboard)
//
// Covers:
//  1. Member → 200 (proxied to upstream).
//  2. Non-member (authenticated, wrong workspace) → 403.
//  3. Unregistered port name → 404.

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/supervisor"
)

// fakeProxySupervisor is a full supervisor.Client stub whose Endpoint returns
// a caller-supplied base URL and port map. All other methods are no-ops.
type fakeProxySupervisor struct {
	endpointBase  string
	endpointPorts map[string]int
	endpointOK    bool

	store *store.Memory
}

func (f *fakeProxySupervisor) StartRun(ws string, task *store.Task, trigger string) *store.Run {
	return f.store.CreateRun(task.ID, trigger)
}
func (f *fakeProxySupervisor) CancelRun(ws, runID string) bool                    { return false }
func (f *fakeProxySupervisor) StopService(ws, taskID string) error                { return nil }
func (f *fakeProxySupervisor) EnsureService(ws string, task *store.Task) error    { return nil }
func (f *fakeProxySupervisor) Endpoint(ws, taskID string) (string, map[string]int, bool) {
	return f.endpointBase, f.endpointPorts, f.endpointOK
}
func (f *fakeProxySupervisor) Health(ws string) supervisor.Status          { return supervisor.Status{Up: true} }
func (f *fakeProxySupervisor) ServiceRunning(ws, taskID string) bool       { return false }
func (f *fakeProxySupervisor) RestartService(ws, taskID string) error      { return nil }
func (f *fakeProxySupervisor) BootServices() error                         { return nil }

// upstreamPort parses the port from a "host:port" listener address string.
func upstreamPort(addr string) (int, error) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, err
	}
	return port, nil
}

// buildProxyRouter sets up a chi router with the workspace proxy route.
func buildProxyRouter(t *testing.T, sup *fakeProxySupervisor) (*chi.Mux, *store.Memory) {
	t.Helper()
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	sup.store = s

	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.Sup = sup

	r := chi.NewRouter()
	r.HandleFunc("/w/{ws}/d/{taskId}/*", h.ServeWorkspaceDashboard)
	return r, s
}

// TestProxyMemberGets200 verifies that a workspace member is proxied to the
// upstream and receives its 200 response.
func TestProxyMemberGets200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	port, err := upstreamPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	sup := &fakeProxySupervisor{
		endpointBase:  "http://127.0.0.1",
		endpointPorts: map[string]int{"ui": port},
		endpointOK:    true,
	}
	r, s := buildProxyRouter(t, sup)

	ownerSub := "owner-" + randSuffix()
	memberSub := "member-" + randSuffix()

	if err := s.UpsertUserOnLogin(ownerSub, ownerSub+"@example.com", ownerSub); err != nil {
		t.Fatalf("UpsertUserOnLogin owner: %v", err)
	}
	if err := s.EnsurePersonal(ownerSub, ownerSub); err != nil {
		t.Fatalf("EnsurePersonal owner: %v", err)
	}
	ws, err := s.CreateWorkspace(ownerSub, "ws-member-"+randSuffix())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsA := ws.ID

	if err := s.UpsertUserOnLogin(memberSub, memberSub+"@example.com", memberSub); err != nil {
		t.Fatalf("UpsertUserOnLogin member: %v", err)
	}
	if err := s.EnsurePersonal(memberSub, memberSub); err != nil {
		t.Fatalf("EnsurePersonal member: %v", err)
	}
	if err := s.InviteMember(wsA, ownerSub, memberSub, "member"); err != nil {
		t.Fatalf("InviteMember: %v", err)
	}

	task := s.CreateTask(&store.Task{
		UserID:      ownerSub,
		WorkspaceID: wsA,
		Name:        "proxy-test-member",
		Kind:        "service",
	})

	req := httptest.NewRequest(http.MethodGet, "/w/"+wsA+"/d/"+task.ID+"/", nil)
	req = injectClaims(req, memberSub, nil)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("member proxy: want 200, got %d body=%q", rr.Code, rr.Body.String())
	}
}

// TestProxyNonMemberGets403 verifies that an authenticated user who is not a
// member of the target workspace receives 403.
func TestProxyNonMemberGets403(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	port, err := upstreamPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	sup := &fakeProxySupervisor{
		endpointBase:  "http://127.0.0.1",
		endpointPorts: map[string]int{"ui": port},
		endpointOK:    true,
	}
	r, s := buildProxyRouter(t, sup)

	ownerSub := "owner-" + randSuffix()
	if err := s.UpsertUserOnLogin(ownerSub, ownerSub+"@example.com", ownerSub); err != nil {
		t.Fatalf("UpsertUserOnLogin: %v", err)
	}
	if err := s.EnsurePersonal(ownerSub, ownerSub); err != nil {
		t.Fatalf("EnsurePersonal: %v", err)
	}
	ws, err := s.CreateWorkspace(ownerSub, "ws-forbidden-"+randSuffix())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsA := ws.ID

	task := s.CreateTask(&store.Task{
		UserID:      ownerSub,
		WorkspaceID: wsA,
		Name:        "proxy-test-forbidden",
		Kind:        "service",
	})

	// outsiderSub is not a member of wsA.
	outsiderSub := "outsider-" + randSuffix()
	req := httptest.NewRequest(http.MethodGet, "/w/"+wsA+"/d/"+task.ID+"/", nil)
	req = injectClaims(req, outsiderSub, nil)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("non-member: want 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

// TestProxyUnregisteredPortNameGets404 verifies that requesting a port name
// absent from the supervisor's port map returns 404 (never forward to unknown ports).
func TestProxyUnregisteredPortNameGets404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	port, err := upstreamPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	// Port map has only "ui"; the request will ask for "unknownname".
	sup := &fakeProxySupervisor{
		endpointBase:  "http://127.0.0.1",
		endpointPorts: map[string]int{"ui": port},
		endpointOK:    true,
	}
	r, s := buildProxyRouter(t, sup)

	ownerSub := "owner-" + randSuffix()
	if err := s.UpsertUserOnLogin(ownerSub, ownerSub+"@example.com", ownerSub); err != nil {
		t.Fatalf("UpsertUserOnLogin: %v", err)
	}
	if err := s.EnsurePersonal(ownerSub, ownerSub); err != nil {
		t.Fatalf("EnsurePersonal: %v", err)
	}
	ws, err := s.CreateWorkspace(ownerSub, "ws-portname-"+randSuffix())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	wsA := ws.ID

	task := s.CreateTask(&store.Task{
		UserID:      ownerSub,
		WorkspaceID: wsA,
		Name:        "proxy-test-portname",
		Kind:        "service",
	})

	req := httptest.NewRequest(http.MethodGet, "/w/"+wsA+"/d/"+task.ID+"/unknownname/", nil)
	req = injectClaims(req, ownerSub, nil)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("unregistered port name: want 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}
