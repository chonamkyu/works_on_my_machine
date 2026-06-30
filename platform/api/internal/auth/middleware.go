package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey int

const (
	claimsKey ctxKey = iota
	workspaceKey
)

type Middleware struct{ devMode bool }

func New(devMode bool) *Middleware { return &Middleware{devMode: devMode} }

func devClaims() *Claims {
	return &Claims{Sub: "dev", Email: "dev@example.com", PreferredUsername: "dev",
		Department: "dev", Groups: []string{"/dev"}, Roles: []string{"platform-admin"}}
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.devMode {
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), devClaims())))
			return
		}
		c, err := claimsFromHeader(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), c)))
	})
}

func (m *Middleware) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.devMode {
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), devClaims())))
			return
		}
		if c, err := claimsFromHeader(r); err == nil {
			r = r.WithContext(withClaims(r.Context(), c))
		}
		next.ServeHTTP(w, r)
	})
}

func claimsFromHeader(r *http.Request) (*Claims, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		// SSE/iframe can't set headers — accept ?access_token=.
		if t := r.URL.Query().Get("access_token"); t != "" {
			return DecodeJWT(t)
		}
		return nil, errMissingToken
	}
	return DecodeJWT(strings.TrimPrefix(h, "Bearer "))
}

var errMissingToken = &authError{"missing bearer token"}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func ClaimsFromCtx(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

func MustUserID(ctx context.Context) string {
	if c := ClaimsFromCtx(ctx); c != nil {
		return c.Sub
	}
	return ""
}

// WithWorkspace / WorkspaceID — set by WorkspaceMiddleware (Task 8).
func WithWorkspace(ctx context.Context, ws string) context.Context {
	return context.WithValue(ctx, workspaceKey, ws)
}

func WorkspaceID(ctx context.Context) string {
	ws, _ := ctx.Value(workspaceKey).(string)
	return ws
}

// MustWorkspaceID falls back to the personal workspace (sub) when no explicit
// workspace was resolved, so handlers always get a non-empty scope.
func MustWorkspaceID(ctx context.Context) string {
	if ws := WorkspaceID(ctx); ws != "" {
		return ws
	}
	return MustUserID(ctx)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// MemberChecker is satisfied by any store that can verify workspace membership.
type MemberChecker interface {
	IsMember(workspaceID, sub string) bool
}

// Workspace resolves the caller's active workspace from the X-Workspace-ID header.
// - Empty or equal to the caller's sub → personal workspace (sub).
// - Different workspace → mc.IsMember must be true, else 403.
// - No claims in context (unauthenticated) → pass through unchanged.
func (m *Middleware) Workspace(mc MemberChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ClaimsFromCtx(r.Context())
			if c == nil {
				next.ServeHTTP(w, r)
				return
			}
			wsID := r.Header.Get("X-Workspace-ID")
			if wsID == "" || wsID == c.Sub {
				r = r.WithContext(WithWorkspace(r.Context(), c.Sub))
				next.ServeHTTP(w, r)
				return
			}
			if !mc.IsMember(wsID, c.Sub) {
				writeErr(w, http.StatusForbidden, "not a member of this workspace")
				return
			}
			r = r.WithContext(WithWorkspace(r.Context(), wsID))
			next.ServeHTTP(w, r)
		})
	}
}

var adminEnv = map[string]bool{}

func SetAdminEnv(csv string) {
	adminEnv = map[string]bool{}
	for _, u := range strings.Split(csv, ",") {
		if u = strings.TrimSpace(u); u != "" {
			adminEnv[u] = true
		}
	}
}

type AdminChecker interface{ IsAdminDB(sub string) bool }

var adminChecker AdminChecker

func SetAdminChecker(c AdminChecker) { adminChecker = c }

func IsAdmin(ctx context.Context) bool {
	c := ClaimsFromCtx(ctx)
	if c == nil {
		return false
	}
	if adminEnv[c.Sub] || adminEnv[c.PreferredUsername] {
		return true
	}
	if c.HasRole("platform-admin") {
		return true
	}
	for _, g := range c.Groups {
		if strings.TrimPrefix(g, "/") == "platform-admin" {
			return true
		}
	}
	if adminChecker != nil && adminChecker.IsAdminDB(c.Sub) {
		return true
	}
	return false
}

func (m *Middleware) AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			writeErr(w, http.StatusForbidden, "platform-admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
