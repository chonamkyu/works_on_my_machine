package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open returns a connected pool. The URL is read from PLATFORM_DB_URL.
// On startup we ensure pgvector + pg_trgm extensions exist and run the
// embedded migration file (idempotent — CREATE TABLE IF NOT EXISTS).
func Open(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("PLATFORM_DB_URL")
	if url == "" {
		url = "postgres://platform:platform@localhost:5432/platform?sslmode=disable"
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pg parse: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pg connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("pg ping: %w", err)
	}

	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg migrate: %w", err)
	}
	return pool, nil
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	dir, err := findMigrationsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(b)); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return nil
}

// findMigrationsDir walks up from the binary location to locate the
// migrations directory at api/internal/db/migrations.
func findMigrationsDir() (string, error) {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "internal", "db", "migrations"),
		filepath.Join(cwd, "api", "internal", "db", "migrations"),
		filepath.Join(cwd, "..", "api", "internal", "db", "migrations"),
		filepath.Join(cwd, "..", "db", "migrations"),
		filepath.Join(cwd, "..", "..", "db", "migrations"),
		filepath.Join(cwd, "..", "..", "..", "internal", "db", "migrations"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("could not locate migrations directory")
}

// MustOpen panics on failure — convenience for main.
func MustOpen(ctx context.Context) *pgxpool.Pool {
	pool, err := Open(ctx)
	if err != nil {
		panic(err)
	}
	return pool
}

// Quiet is a convenience to swallow pgx.ErrNoRows.
func Quiet(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
