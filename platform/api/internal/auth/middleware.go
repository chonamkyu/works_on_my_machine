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
