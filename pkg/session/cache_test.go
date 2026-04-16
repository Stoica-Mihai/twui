package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	dir := t.TempDir()
	c, err := NewCacheAt(filepath.Join(dir, "cache.json"))
	if err != nil {
		t.Fatalf("NewCacheAt: %v", err)
	}
	return c
}

func TestCache_SetGet(t *testing.T) {
	c := newTestCache(t)

	c.Set("key1", "hello", 0)

	var got string
	if !c.Get("key1", &got) {
		t.Fatal("Get returned false for existing key")
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestCache_SetGet_Integer(t *testing.T) {
	c := newTestCache(t)
	c.Set("count", 42, 0)

	var got int
	if !c.Get("count", &got) {
		t.Fatal("Get returned false")
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestCache_Miss(t *testing.T) {
	c := newTestCache(t)

	var s string
	if c.Get("nonexistent", &s) {
		t.Error("Get should return false for missing key")
	}
}

func TestCache_Expiry(t *testing.T) {
	c := newTestCache(t)

	// Use a tiny TTL and sleep to ensure it expires.
	c.Set("expired", "value", time.Nanosecond)
	time.Sleep(5 * time.Millisecond)

	var got string
	if c.Get("expired", &got) {
		t.Error("Get should return false for expired entry")
	}
}

func TestCache_ExpiryFuture(t *testing.T) {
	c := newTestCache(t)
	c.Set("future", "alive", 10*time.Second)

	var got string
	if !c.Get("future", &got) {
		t.Error("Get should return true for non-expired entry")
	}
	if got != "alive" {
		t.Errorf("got %q, want %q", got, "alive")
	}
}

func TestCache_Delete(t *testing.T) {
	c := newTestCache(t)
	c.Set("toDelete", "val", 0)

	c.Delete("toDelete")

	var got string
	if c.Get("toDelete", &got) {
		t.Error("Get should return false after Delete")
	}
}

func TestCache_Delete_NonExistent(t *testing.T) {
	c := newTestCache(t)
	// Deleting a non-existent key should not panic.
	c.Delete("ghost")
}

func TestCache_Overwrite(t *testing.T) {
	c := newTestCache(t)
	c.Set("k", "first", 0)
	c.Set("k", "second", 0)

	var got string
	if !c.Get("k", &got) {
		t.Fatal("Get returned false")
	}
	if got != "second" {
		t.Errorf("got %q, want %q (second Set should overwrite)", got, "second")
	}
}

func TestCache_NilSafe(t *testing.T) {
	var c *Cache
	// All operations on a nil cache should be no-ops.
	c.Set("k", "v", 0)
	c.Delete("k")
	var s string
	if c.Get("k", &s) {
		t.Error("nil cache Get should return false")
	}
}

func TestCache_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	c1, _ := NewCacheAt(path)
	c1.Set("persistent", "data", 0)

	// Reload cache from same file.
	c2, err := NewCacheAt(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var got string
	if !c2.Get("persistent", &got) {
		t.Error("Get should return true after reload")
	}
	if got != "data" {
		t.Errorf("got %q, want %q", got, "data")
	}
}

func TestNewCache_ResolvesDefaultPath(t *testing.T) {
	c, err := NewCache()
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if c == nil {
		t.Fatal("NewCache returned nil")
	}
	// The path should end with the expected suffix.
	if !strings.HasSuffix(c.path, filepath.Join("twui", "cache.json")) {
		t.Errorf("cache path = %q, should end with twui/cache.json", c.path)
	}
}

func TestNewCacheAt_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	// Write corrupt JSON.
	if err := os.WriteFile(path, []byte("{not valid json!!!"), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := NewCacheAt(path)
	if err != nil {
		t.Fatalf("NewCacheAt should not error on corrupt JSON, got: %v", err)
	}
	// The cache should be empty (corrupt data discarded).
	var val string
	if c.Get("anything", &val) {
		t.Error("corrupt cache should have no entries")
	}
}

func TestNewCacheAt_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	c, err := NewCacheAt(path)
	if err != nil {
		t.Fatalf("NewCacheAt: %v", err)
	}
	if c == nil {
		t.Fatal("cache is nil")
	}
	if len(c.data) != 0 {
		t.Error("new cache from nonexistent file should be empty")
	}
}

func TestCache_SaveLocked_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(roDir, 0500); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(roDir, "sub", "cache.json")
	c := &Cache{
		path: path,
		data: map[string]CacheEntry{"k": {Value: []byte(`"v"`)}},
	}

	// saveLocked needs to create directories, which should fail in a
	// read-only parent. This exercises the MkdirAll error path.
	err := c.saveLocked()
	if err == nil {
		t.Error("saveLocked should fail when directory is read-only")
	}
}

func TestCache_Get_ExpiredEntryIsRemoved(t *testing.T) {
	c := newTestCache(t)

	// Manually insert an already-expired entry.
	past := time.Now().Add(-time.Hour)
	c.data["old"] = CacheEntry{
		Value:   []byte(`"stale"`),
		Expires: &past,
	}

	var got string
	if c.Get("old", &got) {
		t.Error("Get should return false for expired entry")
	}

	// The entry should have been removed from the data map.
	c.mu.RLock()
	_, exists := c.data["old"]
	c.mu.RUnlock()
	if exists {
		t.Error("expired entry should be deleted from data map")
	}
}

func TestCache_Get_UnmarshalIntoWrongType(t *testing.T) {
	c := newTestCache(t)
	c.Set("str", "hello", 0)

	var got int
	if c.Get("str", &got) {
		t.Error("Get should return false when unmarshal into wrong type fails")
	}
}

func TestCache_Set_UnmarshalableValue(t *testing.T) {
	c := newTestCache(t)
	// Functions cannot be JSON-marshaled.
	c.Set("fn", func() {}, 0)

	var got string
	if c.Get("fn", &got) {
		t.Error("unmarshalable value should not be stored")
	}
}

func TestCache_Set_ZeroTTL_NoExpiry(t *testing.T) {
	c := newTestCache(t)
	c.Set("forever", "val", 0)

	c.mu.RLock()
	entry := c.data["forever"]
	c.mu.RUnlock()

	if entry.Expires != nil {
		t.Error("zero TTL should result in nil Expires")
	}
}
