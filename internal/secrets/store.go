package secrets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

type Store struct {
	DB  *sql.DB
	Key []byte // 32 bytes
	TTL time.Duration
}

func NewStore(db *sql.DB, keyB64 string, ttl time.Duration) (*Store, error) {
	keyB64 = strings.TrimSpace(keyB64)
	if keyB64 == "" {
		return nil, errors.New("missing secrets master key")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("master key must be 32 bytes")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Store{DB: db, Key: key, TTL: ttl}, nil
}

func (s *Store) Put(ctx context.Context, userID int64, label string, secret string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("label required")
	}
	if secret == "" {
		return fmt.Errorf("secret required")
	}
	if err := s.PruneExpired(ctx); err != nil {
		return err
	}

	aead, err := chacha20poly1305.NewX(s.Key)
	if err != nil {
		return err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(secret), nil)

	now := time.Now().Unix()
	exp := time.Now().Add(s.TTL).Unix()
	_, err = s.DB.ExecContext(ctx, `INSERT OR REPLACE INTO secrets(user_id,label,nonce,ciphertext,created_at,expires_at) VALUES (?,?,?,?,?,?)`, userID, label, nonce, ciphertext, now, exp)
	return err
}

func (s *Store) Get(ctx context.Context, userID int64, label string) (string, bool, error) {
	if err := s.PruneExpired(ctx); err != nil {
		return "", false, err
	}
	var nonce, ciphertext []byte
	var expires int64
	err := s.DB.QueryRowContext(ctx, `SELECT nonce,ciphertext,expires_at FROM secrets WHERE user_id=? AND label=?`, userID, label).Scan(&nonce, &ciphertext, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires <= time.Now().Unix() {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM secrets WHERE user_id=? AND label=?`, userID, label)
		return "", false, nil
	}

	aead, err := chacha20poly1305.NewX(s.Key)
	if err != nil {
		return "", false, err
	}
	pt, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", false, err
	}
	return string(pt), true, nil
}

type Item struct {
	Label     string
	ExpiresAt time.Time
}

func (s *Store) List(ctx context.Context, userID int64) ([]Item, error) {
	if err := s.PruneExpired(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT label, expires_at FROM secrets WHERE user_id=? ORDER BY label`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var label string
		var exp int64
		if err := rows.Scan(&label, &exp); err != nil {
			return nil, err
		}
		out = append(out, Item{Label: label, ExpiresAt: time.Unix(exp, 0)})
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, userID int64, label string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM secrets WHERE user_id=? AND label=?`, userID, strings.TrimSpace(label))
	return err
}

func (s *Store) Clear(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM secrets WHERE user_id=?`, userID)
	return err
}

func (s *Store) PruneExpired(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM secrets WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
