package session

import (
	"path/filepath"
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
