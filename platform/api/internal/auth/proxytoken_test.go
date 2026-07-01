package auth

import (
	"testing"
	"time"
)

func TestProxyTokenRoundTrip(t *testing.T) {
	// Override secret for predictable test.
	SetProxySecret("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	tok, err := MintProxyToken("user-abc", 10*time.Minute)
	if err != nil {
		t.Fatalf("MintProxyToken error: %v", err)
	}
	sub, ok := VerifyProxyToken(tok)
	if !ok {
		t.Fatal("VerifyProxyToken: want ok=true, got false")
	}
	if sub != "user-abc" {
		t.Fatalf("VerifyProxyToken sub: want %q, got %q", "user-abc", sub)
	}
}

func TestProxyTokenExpired(t *testing.T) {
	SetProxySecret("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	tok, err := MintProxyToken("user-abc", -1*time.Second) // already expired
	if err != nil {
		t.Fatalf("MintProxyToken error: %v", err)
	}
	_, ok := VerifyProxyToken(tok)
	if ok {
		t.Fatal("VerifyProxyToken: want ok=false for expired token, got true")
	}
}

func TestProxyTokenTampered(t *testing.T) {
	SetProxySecret("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	tok, err := MintProxyToken("user-abc", 10*time.Minute)
	if err != nil {
		t.Fatalf("MintProxyToken error: %v", err)
	}
	// Flip the last byte of the token.
	b := []byte(tok)
	b[len(b)-1] ^= 0xff
	_, ok := VerifyProxyToken(string(b))
	if ok {
		t.Fatal("VerifyProxyToken: want ok=false for tampered token, got true")
	}
}

// clearProxySecret resets the package-level signing key to the "unset" state so
// proxySecret() falls back to the environment. It also clears the env vars for
// the duration of the test via t.Setenv (auto-restored on cleanup).
func clearProxySecret(t *testing.T) {
	t.Helper()
	proxySecretMu.Lock()
	proxySecretRaw = nil
	proxySecretMu.Unlock()
	t.Setenv("PLATFORM_PROXY_SECRET", "")
	t.Setenv("PLATFORM_ENCRYPTION_KEY", "")
}

// TestProxyTokenFailClosed verifies that with NO signing secret configured,
// MintProxyToken returns "" (cannot mint) and VerifyProxyToken rejects any
// input — so a missing secret can never yield a forgeable cookie.
func TestProxyTokenFailClosed(t *testing.T) {
	clearProxySecret(t)

	tok, err := MintProxyToken("user-abc", 10*time.Minute)
	if err != nil {
		t.Fatalf("MintProxyToken error: %v", err)
	}
	if tok != "" {
		t.Fatalf("MintProxyToken: want \"\" with no secret configured, got %q", tok)
	}

	// No token — and even a well-formed-looking one — must be rejected.
	if _, ok := VerifyProxyToken(""); ok {
		t.Fatal("VerifyProxyToken(\"\"): want ok=false with no secret, got true")
	}
	if _, ok := VerifyProxyToken("eyJzdWIiOiJ1c2VyLWFiYyIsImV4cCI6OTk5OTk5OTk5OX0.deadbeef"); ok {
		t.Fatal("VerifyProxyToken(<forged>): want ok=false with no secret, got true")
	}
}

func TestProxyTokenWrongSecret(t *testing.T) {
	SetProxySecret("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	tok, err := MintProxyToken("user-abc", 10*time.Minute)
	if err != nil {
		t.Fatalf("MintProxyToken error: %v", err)
	}
	// Switch to a different secret before verifying.
	SetProxySecret("aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899")
	_, ok := VerifyProxyToken(tok)
	if ok {
		t.Fatal("VerifyProxyToken: want ok=false for wrong-secret token, got true")
	}
}
