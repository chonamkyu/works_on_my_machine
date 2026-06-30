package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestMemory(t *testing.T) *Memory {
	t.Helper()
	url := os.Getenv("PLATFORM_DB_URL")
	if url == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return NewMemory(pool)
}

func TestUsers_UpsertAndAdminToggle(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	m := newTestMemory(t)

	// Clean up test row at the end.
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, "DELETE FROM users WHERE sub=$1", "t_user")
	})

	if err := m.UpsertUserOnLogin("t_user", "t@x", "tuser"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m.IsAdminDB("t_user") {
		t.Fatal("new user should not be admin")
	}
	if err := m.SetUserAdmin("t_user", true); err != nil {
		t.Fatal(err)
	}
	if !m.IsAdminDB("t_user") {
		t.Fatal("expected admin after toggle")
	}
	found := false
	for _, u := range m.ListUsers() {
		if u.Sub == "t_user" && u.IsAdmin {
			found = true
		}
	}
	if !found {
		t.Fatal("user not listed as admin")
	}
}
