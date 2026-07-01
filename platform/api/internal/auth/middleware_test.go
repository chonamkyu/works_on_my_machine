package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestOptionalCookieFallback(t *testing.T) {
	SetProxySecret("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	tok, err := MintProxyToken("cookie-user", 10*time.Minute)
	if err != nil {
		t.Fatalf("MintProxyToken: %v", err)
	}

	mw := New(false) // non-dev mode
	var got *Claims
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClaimsFromCtx(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "platform_proxy", Value: tok})

	mw.Optional(inner).ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatal("Optional: want claims from cookie, got nil")
	}
	if got.Sub != "cookie-user" {
		t.Fatalf("Optional cookie claims: want sub=%q, got %q", "cookie-user", got.Sub)
	}
}
