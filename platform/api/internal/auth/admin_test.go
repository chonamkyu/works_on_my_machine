package auth

import (
	"context"
	"testing"
)

func TestIsAdmin_FromRoleAndEnv(t *testing.T) {
	ctx := withClaims(context.Background(), &Claims{Sub: "u1", Roles: []string{"platform-admin"}})
	if !IsAdmin(ctx) {
		t.Fatal("platform-admin role should be admin")
	}
	SetAdminEnv("alice,bob")
	ctx2 := withClaims(context.Background(), &Claims{Sub: "alice"})
	if !IsAdmin(ctx2) {
		t.Fatal("env-listed sub should be admin")
	}
	ctx3 := withClaims(context.Background(), &Claims{Sub: "carol"})
	if IsAdmin(ctx3) {
		t.Fatal("non-listed non-role user must not be admin")
	}
}
