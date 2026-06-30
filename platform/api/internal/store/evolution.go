package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EvolutionPRState string

const (
	EvolutionPending  EvolutionPRState = "pending"
	EvolutionAccepted EvolutionPRState = "accepted"
	EvolutionRejected EvolutionPRState = "rejected"
)

type EvolutionPR struct {
	ID                 string           `json:"id"`
	TaskID             string           `json:"taskId"`
	UserID             string           `json:"userId"`
	WorkspaceID        string           `json:"workspaceId"`
	State              EvolutionPRState `json:"state"`
	Reason             string           `json:"reason"`
	Rationale          string           `json:"rationale"`
	DiffSummary        string           `json:"diffSummary"`
	SelfReview         string           `json:"selfReview"`
	OldMainTs          string           `json:"oldMainTs"`
	NewMainTs          string           `json:"newMainTs"`
	OldWorkflowYaml    string           `json:"oldWorkflowYaml"`
	NewWorkflowYaml    string           `json:"newWorkflowYaml"`
	BasedOnReflections []string         `json:"basedOnReflections"`
	CreatedAt          time.Time        `json:"createdAt"`
	ResolvedAt         *time.Time       `json:"resolvedAt,omitempty"`
}

type EvolutionStore struct {
	pool *pgxpool.Pool
}

func NewEvolutionStore(pool *pgxpool.Pool) *EvolutionStore {
	return &EvolutionStore{pool: pool}
}

func (s *EvolutionStore) Create(pr *EvolutionPR) *EvolutionPR {
	if pr.ID == "" {
		pr.ID = "evo_" + randID()
	}
	pr.CreatedAt = time.Now()
	if pr.State == "" {
		pr.State = EvolutionPending
	}
	wsID := pr.WorkspaceID
	if wsID == "" {
		wsID = pr.UserID // backward-compat fallback
	}
	_, _ = s.pool.Exec(context.Background(), `
		INSERT INTO evolution_prs (id, task_id, user_id, workspace_id, state, reason, rationale, diff_summary, self_review,
		                         old_main_ts, new_main_ts, old_workflow_yaml, new_workflow_yaml,
		                         based_on_reflections, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		pr.ID, pr.TaskID, pr.UserID, wsID, pr.State, pr.Reason, pr.Rationale, pr.DiffSummary, pr.SelfReview,
		pr.OldMainTs, pr.NewMainTs, pr.OldWorkflowYaml, pr.NewWorkflowYaml,
		pr.BasedOnReflections, pr.CreatedAt)
	return pr
}

func (s *EvolutionStore) Get(id string) (*EvolutionPR, error) {
	row := s.pool.QueryRow(context.Background(), `
		SELECT id, task_id, user_id, state, reason, rationale, diff_summary, self_review,
		       old_main_ts, new_main_ts, old_workflow_yaml, new_workflow_yaml,
		       based_on_reflections, created_at, resolved_at
		FROM evolution_prs WHERE id=$1`, id)
	pr, err := scanEvolution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pr, err
}

func (s *EvolutionStore) Resolve(id string, state EvolutionPRState) (*EvolutionPR, error) {
	now := time.Now()
	_, err := s.pool.Exec(context.Background(), `
		UPDATE evolution_prs SET state=$1, resolved_at=$2 WHERE id=$3`, state, now, id)
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

func (s *EvolutionStore) List(workspaceID string) []*EvolutionPR {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, task_id, user_id, state, reason, rationale, diff_summary, self_review,
		       old_main_ts, new_main_ts, old_workflow_yaml, new_workflow_yaml,
		       based_on_reflections, created_at, resolved_at
		FROM evolution_prs WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 100`, workspaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*EvolutionPR
	for rows.Next() {
		pr, err := scanEvolution(rows)
		if err == nil {
			out = append(out, pr)
		}
	}
	return out
}

func (s *EvolutionStore) PendingForTask(taskID string) int {
	var n int
	_ = s.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM evolution_prs WHERE task_id=$1 AND state='pending'`, taskID).Scan(&n)
	return n
}

func scanEvolution(s rowScanner) (*EvolutionPR, error) {
	var pr EvolutionPR
	var state string
	if err := s.Scan(&pr.ID, &pr.TaskID, &pr.UserID, &state, &pr.Reason, &pr.Rationale, &pr.DiffSummary, &pr.SelfReview,
		&pr.OldMainTs, &pr.NewMainTs, &pr.OldWorkflowYaml, &pr.NewWorkflowYaml,
		&pr.BasedOnReflections, &pr.CreatedAt, &pr.ResolvedAt); err != nil {
		return nil, err
	}
	pr.State = EvolutionPRState(state)
	return &pr, nil
}
