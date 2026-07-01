package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	proxySecretMu  sync.RWMutex
	proxySecretRaw []byte
)

// SetProxySecret sets the HMAC signing key used by MintProxyToken /
// VerifyProxyToken. raw must be a hex-encoded 32- or 64-byte value.
// Called from main once at startup.
func SetProxySecret(raw string) {
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) < 32 {
		// Treat as raw bytes if not valid hex (e.g. a plain passphrase in dev).
		b = []byte(raw)
	}
	proxySecretMu.Lock()
	proxySecretRaw = b
	proxySecretMu.Unlock()
}

// proxySecret returns the configured HMAC signing key, or nil if none has been
// set (via SetProxySecret) and neither PLATFORM_PROXY_SECRET nor
// PLATFORM_ENCRYPTION_KEY is present in the environment. Callers MUST treat a
// nil return as fail-closed: mint nothing and verify nothing. There is
// deliberately NO hardcoded fallback key — a publicly-known default would let
// anyone forge proxy-session cookies and bypass auth.
func proxySecret() []byte {
	proxySecretMu.RLock()
	if len(proxySecretRaw) > 0 {
		cp := make([]byte, len(proxySecretRaw))
		copy(cp, proxySecretRaw)
		proxySecretMu.RUnlock()
		return cp
	}
	proxySecretMu.RUnlock()

	// Fallback: read from environment at first use.
	if v := os.Getenv("PLATFORM_PROXY_SECRET"); v != "" {
		if b, err := hex.DecodeString(v); err == nil && len(b) >= 32 {
			return b
		}
		return []byte(v)
	}
	if v := os.Getenv("PLATFORM_ENCRYPTION_KEY"); v != "" {
		if b, err := hex.DecodeString(v); err == nil && len(b) >= 32 {
			return b
		}
		return []byte(v)
	}
	// No secret configured: fail closed.
	return nil
}

type proxyPayload struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

// MintProxyToken creates a signed compact token for sub, valid for ttl.
// Format: base64url(JSON).base64url(HMAC-SHA256).
func MintProxyToken(sub string, ttl time.Duration) (string, error) {
	secret := proxySecret()
	if len(secret) == 0 {
		// Fail closed: no signing secret configured, so we cannot mint a token.
		return "", nil
	}
	p := proxyPayload{Sub: sub, Exp: time.Now().Add(ttl).Unix()}
	payloadJSON, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	b64payload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(b64payload))
	sig := mac.Sum(nil)
	b64sig := base64.RawURLEncoding.EncodeToString(sig)

	return b64payload + "." + b64sig, nil
}

// VerifyProxyToken validates the token and returns the sub. ok is false when
// the token is malformed, expired, or carries an invalid signature.
// Constant-time HMAC comparison is used to prevent timing attacks.
func VerifyProxyToken(tok string) (sub string, ok bool) {
	secret := proxySecret()
	if len(secret) == 0 {
		// Fail closed: with no signing secret configured, no token is valid.
		return "", false
	}
	dot := strings.LastIndex(tok, ".")
	if dot < 0 {
		return "", false
	}
	b64payload := tok[:dot]
	b64sig := tok[dot+1:]

	// Recompute expected MAC.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(b64payload))
	expected := mac.Sum(nil)

	// Decode provided signature.
	provided, err := base64.RawURLEncoding.DecodeString(b64sig)
	if err != nil {
		return "", false
	}

	// Constant-time compare.
	if !hmac.Equal(provided, expected) {
		return "", false
	}

	// Decode payload.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(b64payload)
	if err != nil {
		return "", false
	}
	var p proxyPayload
	if err := json.Unmarshal(payloadJSON, &p); err != nil {
		return "", false
	}
	if p.Sub == "" {
		return "", false
	}
	if time.Now().Unix() > p.Exp {
		return "", false
	}
	return p.Sub, true
}
