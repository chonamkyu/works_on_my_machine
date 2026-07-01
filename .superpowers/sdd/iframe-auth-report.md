# iframe/Proxy Authentication Fix — Implementation Report

**Status:** COMPLETE

## Commits (93ecb21 → e9cf43c)

| SHA | Subject |
|---|---|
| 1dd0d81 | auth: add MintProxyToken/VerifyProxyToken for signed proxy-session cookie |
| d3ce897 | auth: extend claimsFromHeader to fall back to platform_proxy cookie |
| b9f1015 | handler: add POST /v1/proxy-session endpoint to mint proxy-session cookie |
| 915b949 | handler: harden checkWorkspaceMembership — nil claims → 403 (no prod pass-through) |
| 7864e79 | handler: fix stale ServeWorkspaceDashboard comment (no more unauthenticated pass-through) |
| 9cdcc9c | web: add /w proxy rule to vite dev server for workspace dashboard iframes |
| e644c79 | web: add establishProxySession + credentials:include for iframe cookie auth |
| e9cf43c | web: use relative iframe src for same-origin dashboard proxy (cookie auth) |

## Build / Test Results

- `go build ./...` — PASS
- `go vet ./...` — PASS
- `go test ./...` — PASS (auth: 15 tests, handler: all proxy tests, store, scheduler, supervisor)
- `CI=true pnpm build` — PASS (1605 modules, no TypeScript errors)

## Cookie Design

| Attribute | Value |
|---|---|
| Name | `platform_proxy` |
| Signing | HMAC-SHA256, key from `PLATFORM_PROXY_SECRET` else `PLATFORM_ENCRYPTION_KEY` |
| Format | `base64url(JSON{sub,exp}).base64url(sig)` (compact, not JWT) |
| HttpOnly | true |
| SameSite | Lax |
| Path | / |
| Secure | true when `PLATFORM_PUBLIC_URL` starts with `https` |
| MaxAge | 43200 (12 hours) |
| Constant-time compare | `hmac.Equal` |

## Proxy Enforcement

| Scenario | Before | After |
|---|---|---|
| Non-member with valid cookie | 403 (already worked) | 403 |
| Member with valid cookie | pass-through | 200 (cookie → claims → IsMember → ok) |
| No cookie, no Bearer, prod | **200 (BYPASS — bug)** | **403** |
| Dev mode (devClaims injected) | 200 | 200 (unchanged) |
| Bearer token (API calls) | 200/403 per membership | 200/403 unchanged |

## Same-Origin Iframe

- `IframeView` `defaultUrl` changed from `${origin}/d/${taskId}/` (absolute) to `/d/${taskId}/` (relative)
- Relative path is same-origin by construction: dev (Vite proxies `/d` to API) + prod (unified gateway)
- Address bar draft stays absolute (`${origin}/d/...`) for user-facing display
- Vite proxy config extended: `/w` rule added for workspace-scoped dashboard routes

## Concerns / Notes

- `SetProxySecret` silent fallback on short hex keys is plan-mandated behavior (no change needed)
- `Secure` flag resolved at request time via `os.Getenv` rather than startup (cosmetic, one syscall per request, harmless)
- Test `SetProxySecret` calls mutate package-level state; tests reset at entry so ordering is safe; already established pattern in codebase

## Security Review Fixes (2026-07-01)

Follow-up hardening from a security review of the proxy-cookie auth. Three fixes on branch `feat/multiuser-a1`.

### Fix 1 (CRITICAL) — fail-closed proxy signing secret

`internal/auth/proxytoken.go` previously fell back to a hardcoded HMAC key
(`"platform-proxy-dev-secret-32bytes"`) when no secret env var was set. A
publicly-known signing key means anyone can forge a valid `platform_proxy`
cookie → auth bypass. Now fail-closed:

- Removed the hardcoded literal fallback entirely. `proxySecret()` returns the
  configured secret bytes, or `nil` when none is set.
- `MintProxyToken`: nil/empty secret → returns `""` (cannot mint).
- `VerifyProxyToken`: nil/empty secret → returns `("", false)` (no cookie is
  ever considered valid).
- `handler.SetProxySession`: when mint returns `""` → responds `503` with
  `{"error":"proxy session unavailable: signing secret not configured"}` and
  sets NO cookie. No forgeable cookie is ever issued.
- `cmd/api/main.go`: when neither `PLATFORM_PROXY_SECRET` nor
  `PLATFORM_ENCRYPTION_KEY` is set, logs a clear WARNING (proxy/iframe auth
  disabled) rather than `log.Fatalf` — the server still runs, the proxy just
  fails closed.
- New test `TestProxyTokenFailClosed`: with NO secret configured,
  `MintProxyToken` returns `""` and `VerifyProxyToken(<empty>)` /
  `VerifyProxyToken(<forged>)` both return `ok=false`. Existing round-trip /
  tamper / expiry / wrong-secret tests retained (they set a secret).

### Fix 2 (Important) — env-driven CORS origins

`cmd/api/main.go` hardcoded `AllowedOrigins: []string{"http://localhost:5173"}`,
which would preflight-reject `POST /v1/proxy-session` (and all API calls) in
prod. Now reads `PLATFORM_CORS_ORIGINS` (comma-separated, trimmed) via a new
`corsOrigins()` helper, defaulting to `http://localhost:5173` when unset.
`AllowCredentials: true` retained (needed for the cookie).

### Fix 3 (Minor) — read Secure flag once

`handler.SetProxySession` computed
`secure := strings.HasPrefix(os.Getenv("PLATFORM_PUBLIC_URL"), "https")` on
every request. Now read once at startup into a new `Handler.ProxyCookieSecure`
field (set in `cmd/api/main.go`) and used directly in the cookie. This resolves
the "Concerns / Notes" item above about per-request `os.Getenv`.

### Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `go test ./...` — all pass (incl. new `TestProxyTokenFailClosed`; `internal/auth` + `internal/handler` ok)
- `pnpm build` (web) — clean, 1605 modules, built in ~0.6s
