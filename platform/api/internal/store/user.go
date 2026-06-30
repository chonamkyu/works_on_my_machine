package store

import (
	"context"
	"time"
)

// User represents a platform user row from the users table.
type User struct {
	Sub               string
	Email             string
	PreferredUsername string
	DisplayName       string
	IsAdmin           bool
	Disabled          bool
	CreatedAt         time.Time
	LastSeenAt        time.Time
}

// UpsertUserOnLogin inserts or updates a user record on login.
// On insert, display_name is set to username. On conflict (same sub),
// it updates email, preferred_username, and bumps last_seen_at.
func (m *Memory) UpsertUserOnLogin(sub, email, username string) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO users (sub, email, preferred_username, display_name, last_seen_at)
		 VALUES ($1,$2,$3,$3, now())
		 ON CONFLICT (sub) DO UPDATE SET
		   email=EXCLUDED.email,
		   preferred_username=EXCLUDED.preferred_username,
		   last_seen_at=now()`,
		sub, email, username)
	return err
}

// ListUsers returns all users ordered by created_at ascending.
func (m *Memory) ListUsers() []*User {
	rows, err := m.pool.Query(context.Background(),
		`SELECT sub, email, preferred_username, display_name, is_admin, disabled, created_at, last_seen_at
		 FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Sub, &u.Email, &u.PreferredUsername, &u.DisplayName,
			&u.IsAdmin, &u.Disabled, &u.CreatedAt, &u.LastSeenAt); err != nil {
			continue
		}
		out = append(out, &u)
	}
	return out
}

// SetUserAdmin sets or clears the is_admin flag for the given sub.
func (m *Memory) SetUserAdmin(sub string, isAdmin bool) error {
	_, err := m.pool.Exec(context.Background(),
		`UPDATE users SET is_admin=$1 WHERE sub=$2`, isAdmin, sub)
	return err
}

// SetUserDisabled sets or clears the disabled flag for the given sub.
func (m *Memory) SetUserDisabled(sub string, disabled bool) error {
	_, err := m.pool.Exec(context.Background(),
		`UPDATE users SET disabled=$1 WHERE sub=$2`, disabled, sub)
	return err
}

// IsAdminDB satisfies auth.AdminChecker. Returns false for unknown subs.
func (m *Memory) IsAdminDB(sub string) bool {
	var isAdmin bool
	err := m.pool.QueryRow(context.Background(),
		`SELECT is_admin FROM users WHERE sub=$1`, sub).Scan(&isAdmin)
	if err != nil {
		return false
	}
	return isAdmin
}
