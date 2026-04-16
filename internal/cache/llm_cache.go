package cache

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"
)

type LLMCache struct {
	DB  *sql.DB
	TTL time.Duration
}

func NewLLMCache(db *sql.DB, ttl time.Duration) *LLMCache {
	if ttl <= 0 {
		ttl = 14 * 24 * time.Hour
	}
	return &LLMCache{DB: db, TTL: ttl}
}

func KeyFromBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (c *LLMCache) Get(key string) (string, bool, error) {
	now := time.Now().Unix()
	var resp string
	var expires int64
	err := c.DB.QueryRow(`SELECT response, expires_at FROM llm_cache WHERE cache_key = ?`, key).Scan(&resp, &expires)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires <= now {
		_, _ = c.DB.Exec(`DELETE FROM llm_cache WHERE cache_key = ?`, key)
		return "", false, nil
	}
	return resp, true, nil
}

func (c *LLMCache) Put(key string, response string) error {
	now := time.Now().Unix()
	expires := time.Now().Add(c.TTL).Unix()
	_, err := c.DB.Exec(`INSERT OR REPLACE INTO llm_cache(cache_key, response, created_at, expires_at) VALUES (?, ?, ?, ?)`, key, response, now, expires)
	return err
}

// PruneExpired deletes expired cache entries.
func (c *LLMCache) PruneExpired() error {
	now := time.Now().Unix()
	_, err := c.DB.Exec(`DELETE FROM llm_cache WHERE expires_at <= ?`, now)
	return err
}
