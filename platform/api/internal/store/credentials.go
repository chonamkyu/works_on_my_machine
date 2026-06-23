package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Credential struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Connector  string     `json:"connector"`
	Label      string     `json:"label"`
	Secret     string     `json:"-"`
	Scopes     []string   `json:"scopes"`
	Status     string     `json:"status"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
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
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *CredentialStore) decrypt(cipher []byte) (string, error) {
	if len(cipher) < s.gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := cipher[:s.gcm.NonceSize()], cipher[s.gcm.NonceSize():]
	pt, err := s.gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func (s *CredentialStore) Upsert(c *Credential) (*Credential, error) {
	if c.ID == "" {
		c.ID = "cred_" + randID()
	}
	if c.Status == "" {
		c.Status = "active"
	}
	enc, err := s.encrypt(c.Secret)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.Now()
	_, err = s.pool.Exec(context.Background(), `
		INSERT INTO credentials (id, user_id, connector, label, encrypted, scopes, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id, connector, label)
		DO UPDATE SET encrypted=EXCLUDED.encrypted, scopes=EXCLUDED.scopes, status=EXCLUDED.status`,
		c.ID, c.UserID, c.Connector, c.Label, enc, c.Scopes, c.Status, c.CreatedAt)
	return c, err
}

func (s *CredentialStore) List(userID string) []*Credential {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, user_id, connector, label, scopes, status, last_used_at, created_at
		FROM credentials WHERE user_id=$1 ORDER BY connector, label`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.UserID, &c.Connector, &c.Label, &c.Scopes, &c.Status, &c.LastUsedAt, &c.CreatedAt); err == nil {
			out = append(out, &c)
		}
	}
	return out
}

// Resolve fetches and decrypts a credential's secret for runtime injection.
// Records last_used_at as a side effect.
func (s *CredentialStore) Resolve(userID, connector, label string) (string, error) {
	row := s.pool.QueryRow(context.Background(), `
		SELECT encrypted FROM credentials
		WHERE user_id=$1 AND connector=$2 AND label=$3 AND status='active'`,
		userID, connector, label)
	var enc []byte
	if err := row.Scan(&enc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	_, _ = s.pool.Exec(context.Background(), `
		UPDATE credentials SET last_used_at=NOW()
		WHERE user_id=$1 AND connector=$2 AND label=$3`, userID, connector, label)
	return s.decrypt(enc)
}

// ResolveDefault picks the first active credential for the connector when
// the task did not pin a specific label.
func (s *CredentialStore) ResolveDefault(userID, connector string) (string, string, error) {
	row := s.pool.QueryRow(context.Background(), `
		SELECT label, encrypted FROM credentials
		WHERE user_id=$1 AND connector=$2 AND status='active'
		ORDER BY last_used_at DESC NULLS LAST, created_at DESC
		LIMIT 1`, userID, connector)
	var label string
	var enc []byte
	if err := row.Scan(&label, &enc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", err
	}
	secret, err := s.decrypt(enc)
	return label, secret, err
}

func (s *CredentialStore) Delete(id string) error {
	tag, err := s.pool.Exec(context.Background(), `DELETE FROM credentials WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
