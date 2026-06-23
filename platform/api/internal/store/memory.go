package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Memory is the tasks/runs/logs/sessions store, backed by Postgres.
// (The name is kept from the in-memory iteration to avoid churn on callers.)
type Memory struct {
	pool *pgxpool.Pool
}

func NewMemory(pool *pgxpool.Pool) *Memory {
	return &Memory{pool: pool}
}

// Seed inserts a tiny default set if the user has no tasks yet — useful for
// the first-run experience. The default session row is always ensured so
// chat history can be persisted from the very first message.
func (m *Memory) Seed(ctx context.Context) {
	// Always ensure the default session exists (idempotent upsert).
	_ = m.upsertSession(ctx, &Session{
		ID:        "sess_default",
		UserID:    DefaultUserID,
		Title:     "Welcome",
		LastMsgAt: time.Now(),
	})

	row := m.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE user_id=$1`, DefaultUserID)
	var n int
	_ = row.Scan(&n)
	if n > 0 {
		return
	}
	seed := []*Task{
		{
			ID:          "tsk_seed_daily_bugs",
			UserID:      DefaultUserID,
			Name:        "daily-bugs",
			Description: "Summarize new GitHub bug issues to Slack at 9am.",
			Status:      "scheduled",
			Triggers: []Trigger{
				{ID: "trg_cron_9am", Type: "cron", Enabled: true, Config: map[string]any{"cron": "0 9 * * 1-5"}},
				{ID: "trg_gh_issue", Type: "github.issue_opened", Enabled: true},
			},
			Capabilities: []string{"github", "slack"},
		},
		{
			ID:          "tsk_seed_pr_review",
			UserID:      DefaultUserID,
			Name:        "pr-review",
			Description: "Auto-comment a checklist on new PRs.",
			Status:      "idle",
			Triggers:    []Trigger{{ID: "trg_gh_pr", Type: "github.pr_opened", Enabled: true}},
			Capabilities: []string{"github"},
		},
		{
			ID:          "tsk_seed_memo_sync",
			UserID:      DefaultUserID,
			Name:        "memo-sync",
			Description: "Mirror Notion notes tagged #share to Slack #notes.",
			Status:      "idle",
			Triggers:    []Trigger{{ID: "trg_notion", Type: "notion.page_updated", Enabled: false}},
			Capabilities: []string{"notion", "slack"},
		},
	}
	for _, t := range seed {
		_ = m.insertTask(ctx, t)
	}
	_ = m.upsertSession(ctx, &Session{
		ID:        "sess_default",
		UserID:    DefaultUserID,
		Title:     "Welcome",
		LastMsgAt: time.Now(),
	})
}

// --- tasks ------------------------------------------------------------

const taskColumns = `id, user_id, name, description, status, triggers, capabilities, model_config, kind, created_at, updated_at`

func (m *Memory) ListTasks(userID string) []*Task {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT `+taskColumns+`
		FROM tasks WHERE user_id=$1 ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (m *Memory) GetTask(id string) (*Task, error) {
	ctx := context.Background()
	row := m.pool.QueryRow(ctx, `
		SELECT `+taskColumns+`
		FROM tasks WHERE id=$1`, id)
	t, err := scanTaskRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListServiceTasks returns service-kind tasks that are NOT disabled. The
// supervisor uses this to decide which daemons should be alive.
func (m *Memory) ListServiceTasks() []*Task {
	rows, err := m.pool.Query(context.Background(), `
		SELECT `+taskColumns+`
		FROM tasks WHERE kind='service' AND status != 'disabled' ORDER BY created_at ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err == nil {
			out = append(out, t)
		}
	}
	return out
}

func (m *Memory) CreateTask(t *Task) *Task {
	if t.ID == "" {
		t.ID = "tsk_" + randID()
	}
	if t.Status == "" {
		t.Status = "idle"
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	_ = m.insertTask(context.Background(), t)
	// Every task gets a 1:1 chat session keyed by task id — that session is
	// the conversation history for working on this task.
	_ = m.upsertSession(context.Background(), &Session{
		ID:        t.ID,
		UserID:    t.UserID,
		Title:     t.Name,
		LastMsgAt: now,
	})
	return t
}

func (m *Memory) insertTask(ctx context.Context, t *Task) error {
	triggersJSON, _ := json.Marshal(t.Triggers)
	capsJSON, _ := json.Marshal(t.Capabilities)
	modelJSON, _ := json.Marshal(t.Model)
	kind := t.Kind
	if kind == "" {
		kind = "oneshot"
	}
	_, err := m.pool.Exec(ctx, `
		INSERT INTO tasks (id, user_id, name, description, status, triggers, capabilities, model_config, kind, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.UserID, t.Name, t.Description, t.Status,
		triggersJSON, capsJSON, modelJSON, kind, t.CreatedAt, t.UpdatedAt)
	return err
}

func (m *Memory) UpdateTask(id string, mutate func(*Task)) (*Task, error) {
	t, err := m.GetTask(id)
	if err != nil {
		return nil, err
	}
	mutate(t)
	t.UpdatedAt = time.Now()
	triggersJSON, _ := json.Marshal(t.Triggers)
	capsJSON, _ := json.Marshal(t.Capabilities)
	modelJSON, _ := json.Marshal(t.Model)
	kind := t.Kind
	if kind == "" {
		kind = "oneshot"
	}
	_, err = m.pool.Exec(context.Background(), `
		UPDATE tasks SET name=$1, description=$2, status=$3, triggers=$4, capabilities=$5, model_config=$6, kind=$7, updated_at=$8
		WHERE id=$9`,
		t.Name, t.Description, t.Status, triggersJSON, capsJSON, modelJSON, kind, t.UpdatedAt, id)
	return t, err
}

func (m *Memory) DeleteTask(id string) error {
	tag, err := m.pool.Exec(context.Background(), `DELETE FROM tasks WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Task ↔ chat session are 1:1 keyed by the same id; drop the session
	// too. messages cascade via the FK on sessions.id.
	_, _ = m.pool.Exec(context.Background(), `DELETE FROM sessions WHERE id=$1`, id)
	return nil
}

// --- runs -------------------------------------------------------------

func (m *Memory) CreateRun(taskID, trigger string) *Run {
	r := &Run{
		ID:      "run_" + randID(),
		TaskID:  taskID,
		UserID:  DefaultUserID, // single-user dev mode
		State:   RunQueued,
		Trigger: trigger,
	}
	_, _ = m.pool.Exec(context.Background(), `
		INSERT INTO runs (id, task_id, user_id, state, trigger) VALUES ($1,$2,$3,$4,$5)`,
		r.ID, r.TaskID, r.UserID, r.State, r.Trigger)
	return r
}

func (m *Memory) GetRun(id string) (*Run, error) {
	row := m.pool.QueryRow(context.Background(), `
		SELECT id, task_id, user_id, state, trigger, started_at, finished_at, duration_ms, output, error
		FROM runs WHERE id=$1`, id)
	r, err := scanRunRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (m *Memory) UpdateRun(id string, mutate func(*Run)) (*Run, error) {
	r, err := m.GetRun(id)
	if err != nil {
		return nil, err
	}
	mutate(r)
	var outputArg any
	if r.Output != nil {
		b, _ := json.Marshal(r.Output)
		outputArg = b
	}
	var errArg any
	if r.Error != "" {
		errArg = r.Error
	}
	_, err = m.pool.Exec(context.Background(), `
		UPDATE runs SET state=$1, started_at=$2, finished_at=$3, duration_ms=$4, output=$5, error=$6
		WHERE id=$7`,
		r.State, r.StartedAt, r.FinishedAt, r.DurationMs, outputArg, errArg, id)
	return r, err
}

func (m *Memory) ListRunsForTask(taskID string) []*Run {
	rows, err := m.pool.Query(context.Background(), `
		SELECT id, task_id, user_id, state, trigger, started_at, finished_at, duration_ms, output, error
		FROM runs WHERE task_id=$1 ORDER BY COALESCE(started_at, NOW()) DESC LIMIT 50`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err == nil {
			out = append(out, r)
		}
	}
	return out
}

// --- logs -------------------------------------------------------------

func (m *Memory) AppendLog(line LogLine) {
	_, _ = m.pool.Exec(context.Background(), `
		INSERT INTO run_logs (run_id, ts, stream, text) VALUES ($1,$2,$3,$4)`,
		line.RunID, line.Ts, line.Stream, line.Text)
}

func (m *Memory) GetLogs(runID string) []LogLine {
	rows, err := m.pool.Query(context.Background(), `
		SELECT run_id, ts, stream, text FROM run_logs WHERE run_id=$1 ORDER BY id ASC`, runID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []LogLine
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.RunID, &l.Ts, &l.Stream, &l.Text); err == nil {
			out = append(out, l)
		}
	}
	return out
}

// --- sessions / messages ----------------------------------------------

func (m *Memory) ListSessions(userID string) []*Session {
	rows, err := m.pool.Query(context.Background(), `
		SELECT id, user_id, title, pinned_tasks, model_id, message_count, last_msg_at, archived_at, created_at
		FROM sessions WHERE user_id=$1 AND archived_at IS NULL ORDER BY last_msg_at DESC`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err == nil {
			out = append(out, s)
		}
	}
	return out
}

func (m *Memory) CreateSession(s *Session) *Session {
	if s.ID == "" {
		s.ID = "sess_" + randID()
	}
	now := time.Now()
	s.CreatedAt = now
	s.LastMsgAt = now
	if s.Title == "" {
		s.Title = "Untitled"
	}
	_ = m.upsertSession(context.Background(), s)
	return s
}

func (m *Memory) upsertSession(ctx context.Context, s *Session) error {
	pinned := s.PinnedTasks
	if pinned == nil {
		pinned = []string{}
	}
	created := s.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	lastMsg := s.LastMsgAt
	if lastMsg.IsZero() {
		lastMsg = created
	}
	_, err := m.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, title, pinned_tasks, model_id, last_msg_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, last_msg_at=EXCLUDED.last_msg_at`,
		s.ID, s.UserID, s.Title, pinned, nullableModel(s.ModelID), lastMsg, created)
	return err
}

func nullableModel(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (m *Memory) GetSession(id string) (*Session, error) {
	row := m.pool.QueryRow(context.Background(), `
		SELECT id, user_id, title, pinned_tasks, model_id, message_count, last_msg_at, archived_at, created_at
		FROM sessions WHERE id=$1`, id)
	s, err := scanSessionRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

func (m *Memory) AppendMessage(sessionID string, role, content string, metadata map[string]any) (*Message, error) {
	// Defensive: ensure session row exists. This makes the chat endpoint
	// resilient to clients that supply an arbitrary sessionId before any
	// session has been explicitly created.
	_ = m.upsertSession(context.Background(), &Session{
		ID:     sessionID,
		UserID: DefaultUserID,
		Title:  "Untitled",
	})
	metaJSON, _ := json.Marshal(metadata)
	row := m.pool.QueryRow(context.Background(), `
		INSERT INTO messages (session_id, role, content, metadata) VALUES ($1,$2,$3,$4)
		RETURNING id, ts`, sessionID, role, content, metaJSON)
	var msg Message
	msg.SessionID = sessionID
	msg.Role = role
	msg.Content = content
	msg.Metadata = metadata
	if err := row.Scan(&msg.ID, &msg.Ts); err != nil {
		return nil, err
	}
	_, _ = m.pool.Exec(context.Background(), `
		UPDATE sessions SET message_count = message_count + 1, last_msg_at = NOW()
		WHERE id = $1`, sessionID)
	return &msg, nil
}

func (m *Memory) ListMessages(sessionID string, limit int) []*Message {
	if limit <= 0 {
		limit = 200
	}
	rows, err := m.pool.Query(context.Background(), `
		SELECT id, session_id, role, content, metadata, ts FROM messages
		WHERE session_id=$1 ORDER BY id ASC LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		var msg Message
		var metaJSON []byte
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &metaJSON, &msg.Ts); err == nil {
			_ = json.Unmarshal(metaJSON, &msg.Metadata)
			out = append(out, &msg)
		}
	}
	return out
}

// --- scan helpers -----------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTaskRow(row pgx.Row) (*Task, error) {
	return scanTaskScanner(row)
}

func scanTask(rows pgx.Rows) (*Task, error) {
	return scanTaskScanner(rows)
}

func scanTaskScanner(s rowScanner) (*Task, error) {
	var t Task
	var triggersJSON, capsJSON, modelJSON []byte
	if err := s.Scan(&t.ID, &t.UserID, &t.Name, &t.Description, &t.Status, &triggersJSON, &capsJSON, &modelJSON, &t.Kind, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(triggersJSON, &t.Triggers)
	_ = json.Unmarshal(capsJSON, &t.Capabilities)
	_ = json.Unmarshal(modelJSON, &t.Model)
	if t.Kind == "" {
		t.Kind = "oneshot"
	}
	return &t, nil
}

func scanRunRow(row pgx.Row) (*Run, error) {
	return scanRunScanner(row)
}

func scanRun(rows pgx.Rows) (*Run, error) {
	return scanRunScanner(rows)
}

func scanRunScanner(s rowScanner) (*Run, error) {
	var r Run
	var outputJSON []byte
	var errStr *string
	if err := s.Scan(&r.ID, &r.TaskID, &r.UserID, &r.State, &r.Trigger, &r.StartedAt, &r.FinishedAt, &r.DurationMs, &outputJSON, &errStr); err != nil {
		return nil, err
	}
	if errStr != nil {
		r.Error = *errStr
	}
	if len(outputJSON) > 0 {
		_ = json.Unmarshal(outputJSON, &r.Output)
	}
	return &r, nil
}

func scanSessionRow(row pgx.Row) (*Session, error) {
	return scanSessionScanner(row)
}

func scanSession(rows pgx.Rows) (*Session, error) {
	return scanSessionScanner(rows)
}

func scanSessionScanner(s rowScanner) (*Session, error) {
	var sess Session
	var modelID *string
	if err := s.Scan(&sess.ID, &sess.UserID, &sess.Title, &sess.PinnedTasks, &modelID, &sess.MessageCount, &sess.LastMsgAt, &sess.ArchivedAt, &sess.CreatedAt); err != nil {
		return nil, err
	}
	if modelID != nil {
		sess.ModelID = *modelID
	}
	return &sess, nil
}

func randID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "0000000000"
	}
	return hex.EncodeToString(b)
}
