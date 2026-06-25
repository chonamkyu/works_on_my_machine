package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type SkillProposal struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	SkillName   string     `json:"skillName"`
	SkillBody   string     `json:"skillBody"`
	Rationale   string     `json:"rationale"`
	Confidence  string     `json:"confidence"`
	AppliesWhen string     `json:"appliesWhen"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

func (m *Memory) CreateSkillProposal(p *SkillProposal) (*SkillProposal, error) {
	if p.ID == "" {
		p.ID = "sp_" + randID()
	}
	if p.Status == "" {
		p.Status = "pending"
	}
	if p.AppliesWhen == "" {
		p.AppliesWhen = "always"
	}
	if p.Confidence == "" {
		p.Confidence = "medium"
	}
	_, err := m.pool.Exec(context.Background(), `
		INSERT INTO skill_proposals (id, user_id, skill_name, skill_body, rationale, confidence, applies_when, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.ID, p.UserID, p.SkillName, p.SkillBody, p.Rationale, p.Confidence, p.AppliesWhen, p.Status)
	return p, err
}

func (m *Memory) ListSkillProposals(userID, status string) []*SkillProposal {
	q := `SELECT id, user_id, skill_name, skill_body, rationale, confidence, applies_when, status, created_at, resolved_at
		FROM skill_proposals WHERE user_id=$1`
	args := []any{userID}
	if status != "" {
		q += ` AND status=$2`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT 100`
	rows, err := m.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*SkillProposal
	for rows.Next() {
		p := &SkillProposal{}
		var resolved *time.Time
		if err := rows.Scan(&p.ID, &p.UserID, &p.SkillName, &p.SkillBody, &p.Rationale, &p.Confidence, &p.AppliesWhen, &p.Status, &p.CreatedAt, &resolved); err != nil {
			continue
		}
		p.ResolvedAt = resolved
		out = append(out, p)
	}
	return out
}

func (m *Memory) GetSkillProposal(id string) (*SkillProposal, error) {
	row := m.pool.QueryRow(context.Background(), `
		SELECT id, user_id, skill_name, skill_body, rationale, confidence, applies_when, status, created_at, resolved_at
		FROM skill_proposals WHERE id=$1`, id)
	p := &SkillProposal{}
	var resolved *time.Time
	if err := row.Scan(&p.ID, &p.UserID, &p.SkillName, &p.SkillBody, &p.Rationale, &p.Confidence, &p.AppliesWhen, &p.Status, &p.CreatedAt, &resolved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.ResolvedAt = resolved
	return p, nil
}

func (m *Memory) ResolveSkillProposal(id, status string) (*SkillProposal, error) {
	row := m.pool.QueryRow(context.Background(), `
		UPDATE skill_proposals SET status=$1, resolved_at=now() WHERE id=$2
		RETURNING id, user_id, skill_name, skill_body, rationale, confidence, applies_when, status, created_at, resolved_at`,
		status, id)
	p := &SkillProposal{}
	var resolved *time.Time
	if err := row.Scan(&p.ID, &p.UserID, &p.SkillName, &p.SkillBody, &p.Rationale, &p.Confidence, &p.AppliesWhen, &p.Status, &p.CreatedAt, &resolved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.ResolvedAt = resolved
	return p, nil
}
