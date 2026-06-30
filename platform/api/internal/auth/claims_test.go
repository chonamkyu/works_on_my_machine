package auth

import "testing"

func TestDecodeJWT_ExtractsClaims(t *testing.T) {
	// header.payload.sig — payload is base64url of the claims JSON.
	// {"sub":"u1","email":"u1@x","preferred_username":"u1","groups":["/team-a"],"realm_access":{"roles":["platform-admin"]}}
	tok := "h." +
		"eyJzdWIiOiJ1MSIsImVtYWlsIjoidTFAeCIsInByZWZlcnJlZF91c2VybmFtZSI6InUxIiwiZ3JvdXBzIjpbIi90ZWFtLWEiXSwicmVhbG1fYWNjZXNzIjp7InJvbGVzIjpbInBsYXRmb3JtLWFkbWluIl19fQ" +
		".s"
	c, err := DecodeJWT(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Sub != "u1" || c.Email != "u1@x" || c.PreferredUsername != "u1" {
		t.Fatalf("bad claims: %+v", c)
	}
	if !c.HasRole("platform-admin") {
		t.Fatalf("expected platform-admin role: %+v", c.Roles)
	}
	if c.PrimaryWorkspace() != "u1" {
		t.Fatalf("primary workspace should be sub, got %q", c.PrimaryWorkspace())
	}
}

func TestDecodeJWT_RejectsMalformed(t *testing.T) {
	if _, err := DecodeJWT("not-a-jwt"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}
