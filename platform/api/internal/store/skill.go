package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

// SkillRef is a parsed "name@version" reference.
// Version is either a numeric string (immutable) or an alias name (e.g. "latest").
// A bare "name" is normalized to "name@latest".
type SkillRef struct {
	Name    string
	Version string // numeric ("1", "2") or alias ("latest", "stable")
}

func (r SkillRef) String() string {
	return r.Name + "@" + r.Version
}

func (r SkillRef) isNumeric() (int, bool) {
	n, err := strconv.Atoi(r.Version)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ParseSkillRef parses "name@version" or bare "name" (treated as "name@latest").
func ParseSkillRef(s string) (SkillRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return SkillRef{}, fmt.Errorf("empty ref")
	}
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return SkillRef{Name: s, Version: "latest"}, nil
	}
	name := s[:at]
	version := s[at+1:]
	if name == "" {
		return SkillRef{}, fmt.Errorf("invalid ref %q: missing name", s)
	}
	if version == "" {
		version = "latest"
	}
	return SkillRef{Name: name, Version: version}, nil
}

// Skill is the metadata row for a global skill.
type Skill struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	LatestVersion int      `json:"latestVersion"`
	CreatedBy     string   `json:"createdBy"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

// SkillVersion is an immutable version of a skill.
type SkillVersion struct {
	ID          string          `json:"id"`
	SkillID     string          `json:"skillId"`
	VersionNo   int             `json:"versionNo"`
	Frontmatter json.RawMessage `json:"frontmatter"`
	Body        string          `json:"body"`
	Raw         string          `json:"raw"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags"`
	Comment     string          `json:"comment"`
	CreatedBy   string          `json:"createdBy"`
	CreatedAt   string          `json:"createdAt"`
}

// SkillAlias is a mutable pointer from an alias name to a version number.
type SkillAlias struct {
	SkillID   string `json:"skillId"`
	Alias     string `json:"alias"`
	VersionNo int    `json:"versionNo"`
	UpdatedBy string `json:"updatedBy"`
	UpdatedAt string `json:"updatedAt"`
}

// ── Skill metadata ───────────────────────────────────────────────────────────

// ListSkills returns all skills ordered by name.
func (m *Memory) ListSkills(ctx context.Context) ([]*Skill, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT s.id, s.name, s.description, s.tags, s.created_by,
		       s.created_at::text, s.updated_at::text,
		       COALESCE((SELECT MAX(version_no) FROM skill_versions WHERE skill_id = s.id), 0)
		FROM skills s
		ORDER BY s.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// GetSkill returns the skill with the given name.
func (m *Memory) GetSkill(name string) (*Skill, error) {
	row := m.pool.QueryRow(context.Background(), `
		SELECT s.id, s.name, s.description, s.tags, s.created_by,
		       s.created_at::text, s.updated_at::text,
		       COALESCE((SELECT MAX(version_no) FROM skill_versions WHERE skill_id = s.id), 0)
		FROM skills s
		WHERE s.name = $1`, name)
	sk, err := scanSkillRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill %s: %w", name, err)
	}
	return sk, nil
}

// CreateSkill inserts a skill + version 1 + "latest" alias atomically.
// Returns an error if the name already exists.
func (m *Memory) CreateSkill(createdBy, name, description string, tags []string, body, comment string) (*Skill, *SkillVersion, error) {
	if tags == nil {
		tags = []string{}
	}
	ctx := context.Background()
	raw := assembleSkillRaw(name, description, tags, body)

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var sk Skill
	err = tx.QueryRow(ctx, `
		INSERT INTO skills (name, description, tags, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, description, tags, created_by,
		          created_at::text, updated_at::text`,
		name, description, tags, createdBy,
	).Scan(&sk.ID, &sk.Name, &sk.Description, &sk.Tags, &sk.CreatedBy, &sk.CreatedAt, &sk.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("create skill: %w", err)
	}
	if sk.Tags == nil {
		sk.Tags = []string{}
	}

	ver, err := publishTx(ctx, tx, &sk, createdBy, name, description, tags, raw, comment)
	if err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	sk.LatestVersion = ver.VersionNo
	return &sk, ver, nil
}

// PublishSkillVersion appends an immutable version N+1, moves @latest to it,
// and mirrors description/tags onto the skills row.
func (m *Memory) PublishSkillVersion(name, createdBy, description string, tags []string, body, comment string) (*SkillVersion, error) {
	if tags == nil {
		tags = []string{}
	}
	ctx := context.Background()

	sk, err := m.GetSkill(name)
	if err != nil {
		return nil, err
	}

	raw := assembleSkillRaw(name, description, tags, body)

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ver, err := publishTx(ctx, tx, sk, createdBy, name, description, tags, raw, comment)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ver, nil
}

// publishTx writes a new skill_version inside the given transaction, mirrors
// metadata onto the parent skill row, and auto-moves @latest.
func publishTx(ctx context.Context, tx pgx.Tx, sk *Skill, createdBy, name, description string, tags []string, raw, comment string) (*SkillVersion, error) {
	var nextNo int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_no), 0) + 1 FROM skill_versions WHERE skill_id = $1`,
		sk.ID).Scan(&nextNo); err != nil {
		return nil, err
	}

	// Build frontmatter JSON: {name, description, tags}
	fmMap := map[string]any{
		"name":        name,
		"description": description,
		"tags":        tags,
	}
	fmJSON, _ := json.Marshal(fmMap)

	// Extract body from raw (strip frontmatter).
	body := extractBody(raw)

	var ver SkillVersion
	row := tx.QueryRow(ctx, `
		INSERT INTO skill_versions (skill_id, version_no, frontmatter, body, raw,
		                            description, tags, comment, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, skill_id, version_no, frontmatter, body, raw,
		          description, tags, comment, created_by, created_at::text`,
		sk.ID, nextNo, fmJSON, body, raw,
		description, tags, comment, createdBy)
	v, err := scanSkillVersionRow(row)
	if err != nil {
		return nil, err
	}
	ver = *v

	// Mirror description/tags onto parent skill row.
	if _, err := tx.Exec(ctx, `
		UPDATE skills SET description=$1, tags=$2, updated_at=NOW()
		WHERE id=$3`, description, tags, sk.ID); err != nil {
		return nil, err
	}

	// Move @latest to the new version (UPSERT).
	if _, err := tx.Exec(ctx, `
		INSERT INTO skill_aliases (skill_id, alias, version_no, updated_by)
		VALUES ($1, 'latest', $2, $3)
		ON CONFLICT (skill_id, alias)
		DO UPDATE SET version_no = EXCLUDED.version_no,
		              updated_by = EXCLUDED.updated_by,
		              updated_at = NOW()`,
		sk.ID, nextNo, createdBy); err != nil {
		return nil, err
	}

	return &ver, nil
}

// ── Versions ─────────────────────────────────────────────────────────────────

// ListSkillVersions returns all versions of the named skill ordered by version_no DESC.
func (m *Memory) ListSkillVersions(name string) ([]*SkillVersion, error) {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT v.id, v.skill_id, v.version_no, v.frontmatter, v.body, v.raw,
		       v.description, v.tags, v.comment, v.created_by, v.created_at::text
		FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1
		ORDER BY v.version_no DESC`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SkillVersion
	for rows.Next() {
		v, err := scanSkillVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetSkillVersion returns a specific version of a named skill.
func (m *Memory) GetSkillVersion(name string, versionNo int) (*SkillVersion, error) {
	ctx := context.Background()
	row := m.pool.QueryRow(ctx, `
		SELECT v.id, v.skill_id, v.version_no, v.frontmatter, v.body, v.raw,
		       v.description, v.tags, v.comment, v.created_by, v.created_at::text
		FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.version_no = $2`, name, versionNo)
	v, err := scanSkillVersionRow(row)
	if err != nil {
		return nil, fmt.Errorf("version not found: %s@%d", name, versionNo)
	}
	return v, nil
}

// ── Aliases ───────────────────────────────────────────────────────────────────

// ListSkillAliases returns all aliases for the named skill.
func (m *Memory) ListSkillAliases(name string) ([]*SkillAlias, error) {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT a.skill_id, a.alias, a.version_no, a.updated_by, a.updated_at::text
		FROM skill_aliases a
		JOIN skills s ON s.id = a.skill_id
		WHERE s.name = $1
		ORDER BY a.alias`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SkillAlias
	for rows.Next() {
		var a SkillAlias
		if err := rows.Scan(&a.SkillID, &a.Alias, &a.VersionNo, &a.UpdatedBy, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// MoveSkillAlias points an alias to a specific version (upsert). The alias
// must not already be pointing to a non-existent version.
func (m *Memory) MoveSkillAlias(name, updatedBy, alias string, versionNo int) (*SkillAlias, error) {
	ctx := context.Background()
	alias = strings.TrimPrefix(alias, "@")

	sk, err := m.GetSkill(name)
	if err != nil {
		return nil, err
	}

	// Validate version exists.
	var exists bool
	if err := m.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM skill_versions WHERE skill_id=$1 AND version_no=$2)`,
		sk.ID, versionNo).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("version %d not found for skill %s", versionNo, name)
	}

	var a SkillAlias
	err = m.pool.QueryRow(ctx, `
		INSERT INTO skill_aliases (skill_id, alias, version_no, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (skill_id, alias)
		DO UPDATE SET version_no = EXCLUDED.version_no,
		              updated_by = EXCLUDED.updated_by,
		              updated_at = NOW()
		RETURNING skill_id, alias, version_no, updated_by, updated_at::text`,
		sk.ID, alias, versionNo, updatedBy,
	).Scan(&a.SkillID, &a.Alias, &a.VersionNo, &a.UpdatedBy, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ── Resolve ───────────────────────────────────────────────────────────────────

// ResolveSkill fetches the SkillVersion for a ref.
// Numeric version refs return that version directly; alias refs resolve via
// the skill_aliases table.
func (m *Memory) ResolveSkill(ref SkillRef) (*SkillVersion, error) {
	ctx := context.Background()

	var versionNo int
	if n, ok := ref.isNumeric(); ok {
		versionNo = n
	} else {
		// alias lookup
		err := m.pool.QueryRow(ctx, `
			SELECT a.version_no
			FROM skill_aliases a
			JOIN skills s ON s.id = a.skill_id
			WHERE s.name = $1 AND a.alias = $2`,
			ref.Name, ref.Version).Scan(&versionNo)
		if err != nil {
			return nil, fmt.Errorf("skill %s: alias @%s not found", ref.Name, ref.Version)
		}
	}

	return m.GetSkillVersion(ref.Name, versionNo)
}

// ── Delete ────────────────────────────────────────────────────────────────────

// DeleteSkill removes a skill and all its versions/aliases (cascade).
func (m *Memory) DeleteSkill(name, deletedBy string) error {
	ctx := context.Background()
	tag, err := m.pool.Exec(ctx, `DELETE FROM skills WHERE name=$1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill not found: %s", name)
	}
	_ = deletedBy // reserved for audit
	return nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

type skillFrontmatter struct {
	Name        string   `yaml:"name,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
}

// assembleSkillRaw builds "---\n<yaml frontmatter>\n---\n\n<body>".
func assembleSkillRaw(name, description string, tags []string, body string) string {
	fm := skillFrontmatter{Name: name, Description: description, Tags: tags}
	out, err := yaml.Marshal(fm)
	if err != nil {
		out = []byte(fmt.Sprintf("name: %s\n", name))
	}
	body = strings.TrimLeft(body, "\r\n")
	return "---\n" + string(out) + "---\n\n" + body
}

// extractBody returns the body portion from an assembled raw skill document.
// If no frontmatter fence is present, returns the whole raw string.
func extractBody(raw string) string {
	trimmed := strings.TrimLeft(raw, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return raw
	}
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimLeft(rest, "\r")
	if !strings.HasPrefix(rest, "\n") {
		return raw
	}
	rest = rest[1:]
	closeIdx := strings.Index(rest, "\n---")
	if closeIdx < 0 {
		return raw
	}
	bodyStart := closeIdx + len("\n---")
	body := rest[bodyStart:]
	return strings.TrimLeft(body, "\r\n")
}

type skillScanner interface {
	Scan(...any) error
}

func scanSkill(s skillScanner) (*Skill, error) {
	var sk Skill
	if err := s.Scan(&sk.ID, &sk.Name, &sk.Description, &sk.Tags, &sk.CreatedBy,
		&sk.CreatedAt, &sk.UpdatedAt, &sk.LatestVersion); err != nil {
		return nil, err
	}
	if sk.Tags == nil {
		sk.Tags = []string{}
	}
	return &sk, nil
}

func scanSkillRow(row pgx.Row) (*Skill, error) {
	var sk Skill
	if err := row.Scan(&sk.ID, &sk.Name, &sk.Description, &sk.Tags, &sk.CreatedBy,
		&sk.CreatedAt, &sk.UpdatedAt, &sk.LatestVersion); err != nil {
		return nil, err
	}
	if sk.Tags == nil {
		sk.Tags = []string{}
	}
	return &sk, nil
}

func scanSkillVersion(s skillScanner) (*SkillVersion, error) {
	var v SkillVersion
	var fmJSON []byte
	if err := s.Scan(&v.ID, &v.SkillID, &v.VersionNo, &fmJSON, &v.Body, &v.Raw,
		&v.Description, &v.Tags, &v.Comment, &v.CreatedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	if len(fmJSON) > 0 {
		v.Frontmatter = json.RawMessage(fmJSON)
	} else {
		v.Frontmatter = json.RawMessage(`{}`)
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	return &v, nil
}

func scanSkillVersionRow(row pgx.Row) (*SkillVersion, error) {
	var v SkillVersion
	var fmJSON []byte
	if err := row.Scan(&v.ID, &v.SkillID, &v.VersionNo, &fmJSON, &v.Body, &v.Raw,
		&v.Description, &v.Tags, &v.Comment, &v.CreatedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	if len(fmJSON) > 0 {
		v.Frontmatter = json.RawMessage(fmJSON)
	} else {
		v.Frontmatter = json.RawMessage(`{}`)
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	return &v, nil
}
