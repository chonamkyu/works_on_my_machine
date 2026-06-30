package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type MemoryScope string

const (
	ScopeUser MemoryScope = "user"
	ScopeTask MemoryScope = "task"
)

type MemoryEntry struct {
	ID          string      `json:"id"`
	UserID      string      `json:"userId"`
	WorkspaceID string      `json:"workspaceId"`
	Scope       MemoryScope `json:"scope"`
	ScopeKey    string      `json:"scopeKey"`
	Topic       string      `json:"topic"`
	Content     string      `json:"content"`
	Embedding   []float32   `json:"-"`
	Pinned      bool        `json:"pinned"`
	SourceRun   string      `json:"sourceRunId,omitempty"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

// MemoryStore is the user/task scoped memory backed by pgvector.
// Embeddings come from an external embedder (set with SetEmbedder). When
// unset, Search falls back to keyword-only ranking using pg_trgm.
type MemoryStore struct {
	pool     *pgxpool.Pool
	embedder Embedder
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

func NewMemoryStore(pool *pgxpool.Pool) *MemoryStore {
	return &MemoryStore{pool: pool}
}

func (m *MemoryStore) SetEmbedder(e Embedder) {
	m.embedder = e
}

func (m *MemoryStore) Write(e *MemoryEntry) *MemoryEntry {
	ctx := context.Background()
	if e.ID == "" {
		e.ID = "mem_" + randID()
	}
	now := time.Now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now

	if e.Embedding == nil && m.embedder != nil {
		if v, err := m.embedder.Embed(ctx, e.Topic+" "+e.Content); err == nil {
			e.Embedding = v
		}
	}

	var embeddingArg any
	if e.Embedding != nil {
		v := pgvector.NewVector(e.Embedding)
		embeddingArg = v
	}

	wsID := e.WorkspaceID
	if wsID == "" {
		wsID = e.UserID // backward-compat fallback
	}
	_, _ = m.pool.Exec(ctx, `
		INSERT INTO memory_entries (id, user_id, workspace_id, scope, scope_key, topic, content, embedding, pinned, source_run_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET
			topic=EXCLUDED.topic,
			content=EXCLUDED.content,
			embedding=EXCLUDED.embedding,
			pinned=EXCLUDED.pinned,
			updated_at=EXCLUDED.updated_at`,
		e.ID, e.UserID, wsID, string(e.Scope), e.ScopeKey, e.Topic, e.Content,
		embeddingArg, e.Pinned, nullableString(e.SourceRun), e.CreatedAt, e.UpdatedAt)
	return e
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (m *MemoryStore) Delete(id string) bool {
	tag, err := m.pool.Exec(context.Background(), `DELETE FROM memory_entries WHERE id=$1`, id)
	if err != nil {
		return false
	}
	return tag.RowsAffected() > 0
}

func (m *MemoryStore) Update(id string, mutate func(*MemoryEntry)) (*MemoryEntry, error) {
	e, err := m.get(context.Background(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	mutate(e)
	e.UpdatedAt = time.Now()
	m.Write(e)
	return e, nil
}

func (m *MemoryStore) get(ctx context.Context, id string) (*MemoryEntry, error) {
	row := m.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, scope, scope_key, topic, content, pinned, source_run_id, created_at, updated_at
		FROM memory_entries WHERE id=$1`, id)
	var e MemoryEntry
	var scope string
	var src *string
	if err := row.Scan(&e.ID, &e.UserID, &e.WorkspaceID, &scope, &e.ScopeKey, &e.Topic, &e.Content, &e.Pinned, &src, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	e.Scope = MemoryScope(scope)
	if src != nil {
		e.SourceRun = *src
	}
	return &e, nil
}

func (m *MemoryStore) List(workspaceID string, scope MemoryScope, scopeKey string) []*MemoryEntry {
	q := `SELECT id, user_id, workspace_id, scope, scope_key, topic, content, pinned, source_run_id, created_at, updated_at
		FROM memory_entries WHERE workspace_id=$1 AND archived_at IS NULL`
	args := []any{workspaceID}
	if scope != "" {
		q += ` AND scope=$2`
		args = append(args, string(scope))
		if scope == ScopeTask && scopeKey != "" {
			q += ` AND scope_key=$3`
			args = append(args, scopeKey)
		}
	}
	q += ` ORDER BY updated_at DESC LIMIT 500`
	rows, err := m.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var sc string
		var src *string
		if err := rows.Scan(&e.ID, &e.UserID, &e.WorkspaceID, &sc, &e.ScopeKey, &e.Topic, &e.Content, &e.Pinned, &src, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		e.Scope = MemoryScope(sc)
		if src != nil {
			e.SourceRun = *src
		}
		out = append(out, &e)
	}
	return out
}

// Search runs a hybrid query: vector similarity (if embeddings available)
// combined with pg_trgm content similarity. topK results return blended by
// 0.6 vector + 0.4 lexical.
func (m *MemoryStore) Search(workspaceID, query string, topK int) []*MemoryEntry {
	if topK <= 0 {
		topK = 10
	}
	ctx := context.Background()

	var queryVec *pgvector.Vector
	if m.embedder != nil {
		if v, err := m.embedder.Embed(ctx, query); err == nil {
			vv := pgvector.NewVector(v)
			queryVec = &vv
		}
	}

	if queryVec != nil {
		rows, err := m.pool.Query(ctx, `
			SELECT id, user_id, workspace_id, scope, scope_key, topic, content, pinned, source_run_id, created_at, updated_at,
			       (0.6 * (1 - (embedding <=> $1)) + 0.4 * similarity(content, $2)) AS rank
			FROM memory_entries
			WHERE workspace_id=$3 AND embedding IS NOT NULL AND archived_at IS NULL
			ORDER BY rank DESC
			LIMIT $4`, queryVec, query, workspaceID, topK)
		if err == nil {
			defer rows.Close()
			return collectMemoryWithRank(rows)
		}
	}
	// Lexical-only fallback.
	rows, err := m.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, scope, scope_key, topic, content, pinned, source_run_id, created_at, updated_at,
		       similarity(content, $1) AS rank
		FROM memory_entries
		WHERE workspace_id=$2 AND content % $1 AND archived_at IS NULL
		ORDER BY rank DESC
		LIMIT $3`, query, workspaceID, topK)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return collectMemoryWithRank(rows)
}

// PruneStale soft-archives non-pinned memories that have aged out and are
// not getting retrieval hits. Three tiers, OR'd together:
//   1. created > maxAgeDays days ago AND read_count = 0
//   2. last_read_at older than staleDays
//   3. created > maxAgeDays AND read_count <= rarelyReadThreshold
// Pinned entries are NEVER archived. Returns the number of rows archived.
func (m *MemoryStore) PruneStale(workspaceID string, maxAgeDays, staleDays, rarelyReadThreshold int) (int, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}
	if staleDays <= 0 {
		staleDays = 60
	}
	tag, err := m.pool.Exec(context.Background(), `
		UPDATE memory_entries
		SET archived_at = now()
		WHERE workspace_id = $1
		  AND archived_at IS NULL
		  AND pinned = false
		  AND (
		    (created_at < now() - make_interval(days => $2) AND read_count = 0)
		    OR (last_read_at IS NOT NULL AND last_read_at < now() - make_interval(days => $3))
		    OR (created_at < now() - make_interval(days => $2) AND read_count <= $4)
		  )`,
		workspaceID, maxAgeDays, staleDays, rarelyReadThreshold)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// PruneStats returns counters for the most recent prune decisions — useful
// for telemetry and the Settings → Memory view.
type PruneStats struct {
	Total    int
	Pinned   int
	Archived int
	Active   int
}

func (m *MemoryStore) Stats(workspaceID string) PruneStats {
	var s PruneStats
	row := m.pool.QueryRow(context.Background(), `
		SELECT
		  count(*),
		  count(*) FILTER (WHERE pinned = true),
		  count(*) FILTER (WHERE archived_at IS NOT NULL),
		  count(*) FILTER (WHERE archived_at IS NULL)
		FROM memory_entries WHERE workspace_id = $1`, workspaceID)
	_ = row.Scan(&s.Total, &s.Pinned, &s.Archived, &s.Active)
	return s
}

func collectMemoryWithRank(rows pgx.Rows) []*MemoryEntry {
	var out []*MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var sc string
		var src *string
		var rank float64
		if err := rows.Scan(&e.ID, &e.UserID, &e.WorkspaceID, &sc, &e.ScopeKey, &e.Topic, &e.Content, &e.Pinned, &src, &e.CreatedAt, &e.UpdatedAt, &rank); err != nil {
			continue
		}
		e.Scope = MemoryScope(sc)
		if src != nil {
			e.SourceRun = *src
		}
		out = append(out, &e)
	}
	return out
}

// ListAllWorkspaceIDs returns distinct workspace IDs that have memory entries.
// Used by the pruner to iterate all workspaces without knowing them upfront.
func (m *MemoryStore) ListAllWorkspaceIDs() []string {
	rows, err := m.pool.Query(context.Background(), `SELECT DISTINCT workspace_id FROM memory_entries WHERE archived_at IS NULL`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && id != "" {
			out = append(out, id)
		}
	}
	return out
}
