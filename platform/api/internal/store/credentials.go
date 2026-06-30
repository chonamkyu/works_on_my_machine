package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Credential struct {
	ID          string         `json:"id"`
	UserID      string         `json:"userId"`
	WorkspaceID string         `json:"workspaceId"`
	Connector   string         `json:"connector"`
	Label       string         `json:"label"`
	Secret      string         `json:"-"` // legacy: raw secret value (plain string)
	Value       map[string]any `json:"-"` // new: arbitrary JSON payload (auth bag)
	Scopes      []string       `json:"scopes"`
	Status      string         `json:"status"`
	LastUsedAt  *time.Time     `json:"lastUsedAt,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

type CredentialStore struct {
	pool *pgxpool.Pool
	gcm  cipher.AEAD
}

func NewCredentialStore(pool *pgxpool.Pool) (*CredentialStore, error) {
	keyHex := os.Getenv("PLATFORM_ENCRYPTION_KEY")
	if keyHex == "" {
		// Dev-only stable key. Real deployments must set this.
		keyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || (len(key) != 16 && len(key) != 32) {
		return nil, fmt.Errorf("PLATFORM_ENCRYPTION_KEY must be 32 or 64 hex chars")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CredentialStore{pool: pool, gcm: gcm}, nil
}

func (s *CredentialStore) encrypt(plain string) ([]byte, error) {
	return s.encryptBytes([]byte(plain))
}

func (s *CredentialStore) encryptBytes(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.gcm.Seal(nonce, nonce, plain, nil), nil
}


// Upsert encrypts the credential's payload and stores it. The payload is
// always serialised as JSON so any auth shape can be saved:
//   - a single token  →  {"token": "xoxb-..."}
//   - an OAuth pair   →  {"bot_token": "xoxb-...", "user_token": "xoxp-..."}
//   - AWS keys        →  {"access_key_id": "...", "secret_access_key": "...", "region": "..."}
//   - a DB connection →  {"uri": "postgres://..."}
//   - anything custom →  {...arbitrary keys...}
func (s *CredentialStore) Upsert(c *Credential) (*Credential, error) {
	if c.ID == "" {
		c.ID = "cred_" + randID()
	}
	if c.Status == "" {
		c.Status = "active"
	}
	payload := c.Value
	if payload == nil {
		payload = map[string]any{}
	}
	// Legacy callers (OAuth flow, prior endpoints) still pass Secret as a
	// plain string. Promote that into a {token: ...} bag.
	if c.Secret != "" {
		if _, has := payload["token"]; !has {
			payload["token"] = c.Secret
		}
	}
	raw, _ := json.Marshal(payload)
	enc, err := s.encryptBytes(raw)
	if err != nil {
		return nil, err
	}
	c.Value = payload
	c.CreatedAt = time.Now()
	scopes := c.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	wsID := c.WorkspaceID
	if wsID == "" {
		wsID = c.UserID // backward-compat fallback
	}
	_, err = s.pool.Exec(context.Background(), `
		INSERT INTO credentials (id, user_id, workspace_id, connector, label, encrypted, scopes, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (user_id, connector, label)
		DO UPDATE SET encrypted=EXCLUDED.encrypted, scopes=EXCLUDED.scopes, status=EXCLUDED.status`,
		c.ID, c.UserID, wsID, c.Connector, c.Label, enc, scopes, c.Status, c.CreatedAt)
	return c, err
}

func (s *CredentialStore) List(workspaceID string) []*Credential {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, user_id, workspace_id, connector, label, scopes, status, last_used_at, created_at
		FROM credentials WHERE workspace_id=$1 ORDER BY connector, label`, workspaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.Connector, &c.Label, &c.Scopes, &c.Status, &c.LastUsedAt, &c.CreatedAt); err == nil {
			out = append(out, &c)
		}
	}
	return out
}

// decode unwraps the encrypted payload. Returns:
//   - the JSON payload as a map (always non-nil on success)
//   - a "primary" string suitable for env injection: prefers `token`,
//     then `bot_token`, then `api_key`, then the first string-valued field.
//     Falls back to the entire JSON if no obvious single field exists.
func (s *CredentialStore) decode(enc []byte) (map[string]any, string, error) {
	plain, err := s.decryptToBytes(enc)
	if err != nil {
		return nil, "", err
	}
	val := map[string]any{}
	// New format: a JSON object.
	if len(plain) > 0 && plain[0] == '{' {
		if err := json.Unmarshal(plain, &val); err != nil {
			// Treat as raw string fallback.
			return map[string]any{"token": string(plain)}, string(plain), nil
		}
	} else {
		// Legacy format: a bare string secret.
		val["token"] = string(plain)
	}
	primary := pickPrimary(val)
	return val, primary, nil
}

func pickPrimary(val map[string]any) string {
	prefs := []string{"token", "bot_token", "api_key", "access_token", "secret"}
	for _, k := range prefs {
		if v, ok := val[k].(string); ok && v != "" {
			return v
		}
	}
	for _, v := range val {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	b, _ := json.Marshal(val)
	return string(b)
}

func (s *CredentialStore) decryptToBytes(cipherbuf []byte) ([]byte, error) {
	if len(cipherbuf) < s.gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, body := cipherbuf[:s.gcm.NonceSize()], cipherbuf[s.gcm.NonceSize():]
	return s.gcm.Open(nil, nonce, body, nil)
}

// Resolve fetches and decrypts a credential, returning (primary string, full
// JSON payload). Records last_used_at as a side effect.
func (s *CredentialStore) Resolve(workspaceID, connector, label string) (string, error) {
	primary, _, err := s.ResolveFull(workspaceID, connector, label)
	return primary, err
}

func (s *CredentialStore) ResolveFull(workspaceID, connector, label string) (string, map[string]any, error) {
	row := s.pool.QueryRow(context.Background(), `
		SELECT encrypted FROM credentials
		WHERE workspace_id=$1 AND connector=$2 AND label=$3 AND status='active'`,
		workspaceID, connector, label)
	var enc []byte
	if err := row.Scan(&enc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		return "", nil, err
	}
	_, _ = s.pool.Exec(context.Background(), `
		UPDATE credentials SET last_used_at=NOW()
		WHERE workspace_id=$1 AND connector=$2 AND label=$3`, workspaceID, connector, label)
	val, primary, err := s.decode(enc)
	return primary, val, err
}

// ResolveDefault picks the first active credential for the connector when
// the task did not pin a specific label.
func (s *CredentialStore) ResolveDefault(workspaceID, connector string) (string, string, error) {
	label, _, primary, err := s.ResolveDefaultFull(workspaceID, connector)
	return label, primary, err
}

func (s *CredentialStore) ResolveDefaultFull(workspaceID, connector string) (string, map[string]any, string, error) {
	row := s.pool.QueryRow(context.Background(), `
		SELECT label, encrypted FROM credentials
		WHERE workspace_id=$1 AND connector=$2 AND status='active'
		ORDER BY last_used_at DESC NULLS LAST, created_at DESC
		LIMIT 1`, workspaceID, connector)
	var label string
	var enc []byte
	if err := row.Scan(&label, &enc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, "", ErrNotFound
		}
		return "", nil, "", err
	}
	val, primary, err := s.decode(enc)
	return label, val, primary, err
}

func (s *CredentialStore) Delete(id, workspaceID string) error {
	tag, err := s.pool.Exec(context.Background(), `DELETE FROM credentials WHERE id=$1 AND workspace_id=$2`, id, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
