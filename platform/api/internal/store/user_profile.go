package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// UserProfile carries the stable, slow-changing facts about the user the
// agent should always honor (preferred language, reply tone, hard rules,
// etc.). Distinct from memory_entries which churn quickly.
type UserProfile struct {
	UserID            string            `json:"userId"`
	PreferredLanguage string            `json:"preferredLanguage"`
	ReplyTone         string            `json:"replyTone"`
	ExpertiseLevel    string            `json:"expertiseLevel"`
	HardRules         []string          `json:"hardRules"`
	DefaultRepos      []string          `json:"defaultRepos"`
	DefaultChannels   []string          `json:"defaultChannels"`
	DefaultModels     map[string]string `json:"defaultModels"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// ProfileSuggestion is a reflection-proposed change to one field of
// UserProfile, awaiting user accept/reject in Settings.
type ProfileSuggestion struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Field      string     `json:"field"`
	Value      string     `json:"value"`
	Source     string     `json:"source"`
	SessionID  string     `json:"sessionId,omitempty"`
	Status     string     `json:"status"` // pending | accepted | rejected
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

func (m *Memory) GetUserProfile(workspaceID string) (*UserProfile, error) {
	row := m.pool.QueryRow(context.Background(), `
		SELECT user_id, preferred_language, reply_tone, expertise_level,
		       hard_rules, default_repos, default_channels, default_models, updated_at
		FROM user_profile WHERE workspace_id=$1`, workspaceID)
	var p UserProfile
	var hardJSON, reposJSON, channelsJSON, modelsJSON []byte
	if err := row.Scan(&p.UserID, &p.PreferredLanguage, &p.ReplyTone, &p.ExpertiseLevel,
		&hardJSON, &reposJSON, &channelsJSON, &modelsJSON, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(hardJSON, &p.HardRules)
	_ = json.Unmarshal(reposJSON, &p.DefaultRepos)
	_ = json.Unmarshal(channelsJSON, &p.DefaultChannels)
	_ = json.Unmarshal(modelsJSON, &p.DefaultModels)
	return &p, nil
}

func (m *Memory) UpsertUserProfile(p *UserProfile) error {
	hard, _ := json.Marshal(orEmpty(p.HardRules))
	repos, _ := json.Marshal(orEmpty(p.DefaultRepos))
	channels, _ := json.Marshal(orEmpty(p.DefaultChannels))
	models, _ := json.Marshal(orEmptyMap(p.DefaultModels))
	_, err := m.pool.Exec(context.Background(), `
		INSERT INTO user_profile (user_id, workspace_id, preferred_language, reply_tone, expertise_level,
			hard_rules, default_repos, default_channels, default_models, updated_at)
		VALUES ($1,$1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (user_id) DO UPDATE SET
		  preferred_language=EXCLUDED.preferred_language,
		  reply_tone=EXCLUDED.reply_tone,
		  expertise_level=EXCLUDED.expertise_level,
		  hard_rules=EXCLUDED.hard_rules,
		  default_repos=EXCLUDED.default_repos,
		  default_channels=EXCLUDED.default_channels,
		  default_models=EXCLUDED.default_models,
		  updated_at=now()`,
		p.UserID, p.PreferredLanguage, p.ReplyTone, p.ExpertiseLevel,
		hard, repos, channels, models)
	return err
}

func (m *Memory) AppendProfileSuggestion(workspaceID, field, value, sessionID string) error {
	if workspaceID == "" || field == "" || value == "" {
		return nil
	}
	// De-duplicate: if a pending suggestion for the same field+value already
	// exists, leave it alone.
	var existing int
	_ = m.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_profile_suggestions WHERE workspace_id=$1 AND field=$2 AND value=$3 AND status='pending'`,
		workspaceID, field, value).Scan(&existing)
	if existing > 0 {
		return nil
	}
	id := "ps_" + randID()
	_, err := m.pool.Exec(context.Background(), `
		INSERT INTO user_profile_suggestions (id, user_id, workspace_id, field, value, source, session_id)
		VALUES ($1,$2,$2,$3,$4,'chat-reflect',$5)`,
		id, workspaceID, field, value, nullableString(sessionID))
	return err
}

func (m *Memory) ListProfileSuggestions(workspaceID, status string) []*ProfileSuggestion {
	q := `SELECT id, user_id, field, value, source, session_id, status, created_at, resolved_at
		FROM user_profile_suggestions WHERE workspace_id=$1`
	args := []any{workspaceID}
	if status != "" {
		q += ` AND status=$2`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := m.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*ProfileSuggestion
	for rows.Next() {
		var s ProfileSuggestion
		var sess *string
		var resolved *time.Time
		if err := rows.Scan(&s.ID, &s.UserID, &s.Field, &s.Value, &s.Source, &sess, &s.Status, &s.CreatedAt, &resolved); err != nil {
			continue
		}
		if sess != nil {
			s.SessionID = *sess
		}
		s.ResolvedAt = resolved
		out = append(out, &s)
	}
	return out
}

// ResolveProfileSuggestion marks a suggestion accepted or rejected. On accept
// the value is also written into user_profile via a separate UpsertUserProfile
// call by the handler.
func (m *Memory) ResolveProfileSuggestion(id, status string) (*ProfileSuggestion, error) {
	row := m.pool.QueryRow(context.Background(), `
		UPDATE user_profile_suggestions SET status=$1, resolved_at=now() WHERE id=$2
		RETURNING id, user_id, field, value, source, session_id, status, created_at, resolved_at`,
		status, id)
	var s ProfileSuggestion
	var sess *string
	var resolved *time.Time
	if err := row.Scan(&s.ID, &s.UserID, &s.Field, &s.Value, &s.Source, &sess, &s.Status, &s.CreatedAt, &resolved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if sess != nil {
		s.SessionID = *sess
	}
	s.ResolvedAt = resolved
	return &s, nil
}

// NoteMemoryRead bumps the read_count + last_read_at counters for retrieval
// utility tracking (Phase 2 pruning signal).
func (m *Memory) NoteMemoryRead(ids []string) {
	if len(ids) == 0 {
		return
	}
	_, _ = m.pool.Exec(context.Background(),
		`UPDATE memory_entries SET read_count=read_count+1, last_read_at=now() WHERE id=ANY($1)`,
		ids)
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
