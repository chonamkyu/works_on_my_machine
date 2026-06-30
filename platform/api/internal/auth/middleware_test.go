package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticate_DevModeInjectsAdmin(t *testing.T) {
	m := New(true)
	var got *Claims
	h := m.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ClaimsFromCtx(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got == nil || got.Sub != "dev" || !got.HasRole("platform-admin") {
		t.Fatalf("dev claims missing/incorrect: %+v", got)
	}
}

func TestAuthenticate_RejectsMissingToken(t *testing.T) {
	m := New(false)
	rec := httptest.NewRecorder()
	m.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
