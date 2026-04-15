package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CacheEntry holds a serialized value and an optional expiration timestamp.
type CacheEntry struct {
	Value   json.RawMessage `json:"value"`
	Expires *time.Time      `json:"expires,omitempty"`
}

// Cache is a JSON file-backed key-value store with TTL-based expiration.
// It is safe for concurrent use.
type Cache struct {
	path string
	data map[string]CacheEntry
	mu   sync.RWMutex
}

// NewCacheAt creates or loads a cache at the specified file path.
func NewCacheAt(path string) (*Cache, error) {
	c := &Cache{
		path: path,
		data: make(map[string]CacheEntry),
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("cache: read %s: %w", path, err)
	}

	if err := json.Unmarshal(raw, &c.data); err != nil {
		c.data = make(map[string]CacheEntry)
	}

	return c, nil
}

// NewCache creates or loads the cache stored at
// ~/.config/twui/cache.json.
func NewCache() (*Cache, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("cache: determine config dir: %w", err)
	}
	return NewCacheAt(filepath.Join(configDir, "twui", "cache.json"))
}

// Get deserializes the cached value for key into dst. It returns false
// when the key does not exist or has expired. Expired entries are removed
// automatically. Safe to call on a nil Cache.
func (c *Cache) Get(key string, dst any) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.data[key]
	if !ok {
		return false
	}

	if entry.Expires != nil && time.Now().After(*entry.Expires) {
		delete(c.data, key)
		// Best-effort persist the cleanup.
		if err := c.saveLocked(); err != nil {
			slog.Warn("Cache write failed", "err", err)
		}
		return false
	}

	if err := json.Unmarshal(entry.Value, dst); err != nil {
		return false
	}

	return true
}

// Set serializes val and stores it under key with the given TTL.
// A zero TTL means the entry never expires. Safe to call on a nil Cache.
func (c *Cache) Set(key string, val any, ttl time.Duration) {
	if c == nil {
		return
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := CacheEntry{Value: raw}
	if ttl > 0 {
		exp := time.Now().Add(ttl)
		entry.Expires = &exp
	}

	c.data[key] = entry
	if err := c.saveLocked(); err != nil {
		slog.Warn("Cache write failed", "err", err)
	}
}

// Delete removes a key from the cache and persists the change.
// Safe to call on a nil Cache.
func (c *Cache) Delete(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.data, key)
	if err := c.saveLocked(); err != nil {
		slog.Warn("Cache write failed", "err", err)
	}
}

// saveLocked performs the actual atomic write. It must be called while
// the caller holds the mutex.
func (c *Cache) saveLocked() error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cache: create dir %s: %w", dir, err)
	}

	raw, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "cache-*.json.tmp")
	if err != nil {
		return fmt.Errorf("cache: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("cache: write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("cache: close temp file: %w", err)
	}

	if err := os.Rename(tmpName, c.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("cache: rename %s -> %s: %w", tmpName, c.path, err)
	}

	return nil
}
