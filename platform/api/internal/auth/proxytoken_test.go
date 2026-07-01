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
