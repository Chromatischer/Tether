package store

import (
	"database/sql"
	"errors"
	"time"
)

type WebFetchCacheEntry struct {
	UserID      int64
	CacheKey    string
	URL         string
	Status      int
	ContentType string
	FetchedAt   time.Time
	Body        []byte
	Truncated   bool
	Bytes       int
}

func GetWebFetchCache(db *sql.DB, userID int64, cacheKey string) (WebFetchCacheEntry, bool, error) {
	var e WebFetchCacheEntry
	var fetched string
	var truncated int
	err := db.QueryRow(`
SELECT user_id, cache_key, url, status, content_type, fetched_at, body, truncated, bytes
FROM web_fetch_cache
WHERE user_id=? AND cache_key=?
LIMIT 1
`, userID, cacheKey).Scan(&e.UserID, &e.CacheKey, &e.URL, &e.Status, &e.ContentType, &fetched, &e.Body, &truncated, &e.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return WebFetchCacheEntry{}, false, nil
	}
	if err != nil {
		return WebFetchCacheEntry{}, false, err
	}
	t, err := time.Parse(time.RFC3339Nano, fetched)
	if err == nil {
		e.FetchedAt = t
	}
	e.Truncated = truncated != 0
	return e, true, nil
}

func UpsertWebFetchCache(db *sql.DB, e WebFetchCacheEntry) error {
	tr := 0
	if e.Truncated {
		tr = 1
	}
	_, err := db.Exec(`
INSERT INTO web_fetch_cache (user_id, cache_key, url, status, content_type, body, truncated, bytes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, cache_key) DO UPDATE SET
  url=excluded.url,
  status=excluded.status,
  content_type=excluded.content_type,
  fetched_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),
  body=excluded.body,
  truncated=excluded.truncated,
  bytes=excluded.bytes
`, e.UserID, e.CacheKey, e.URL, e.Status, e.ContentType, e.Body, tr, e.Bytes)
	return err
}
