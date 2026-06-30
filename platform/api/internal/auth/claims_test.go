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

func TestDecodeJWT_AggregatesResourceAccessRoles(t *testing.T) {
	// Test that roles from resource_access.<client>.roles are aggregated alongside realm_access.roles.
	// Payload: {"sub":"u2","resource_access":{"platform":{"roles":["editor"]}},"realm_access":{"roles":["base"]}}
	tok := "h." +
		"eyJzdWIiOiJ1MiIsInJlc291cmNlX2FjY2VzcyI6eyJwbGF0Zm9ybSI6eyJyb2xlcyI6WyJlZGl0b3IiXX19LCJyZWFsbV9hY2Nlc3MiOnsicm9sZXMiOlsiYmFzZSJdfX0" +
		".s"
	c, err := DecodeJWT(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Sub != "u2" {
		t.Fatalf("expected sub u2, got %q", c.Sub)
	}
	if !c.HasRole("editor") {
		t.Fatalf("expected editor role from resource_access: %v", c.Roles)
	}
	if !c.HasRole("base") {
		t.Fatalf("expected base role from realm_access: %v", c.Roles)
	}
}
