package lambda

import (
	"testing"
)

func TestTarCache_lruEvictsByBytes(t *testing.T) {
	// Given: a cache with room for two 40-byte entries.
	c := newTarCache(100)
	c.put(&tarCacheEntry{key: "a", data: make([]byte, 40)})
	c.put(&tarCacheEntry{key: "b", data: make([]byte, 40)})

	// When: "a" is freshened and a third entry pushes past the budget.
	if _, ok := c.get("a"); !ok {
		t.Fatal("entry a missing before eviction")
	}
	c.put(&tarCacheEntry{key: "c", data: make([]byte, 40)})

	// Then: the least recently used entry ("b") is gone; "a" and "c" remain.
	if _, ok := c.get("b"); ok {
		t.Fatal("expected b evicted as least recently used")
	}
	if _, ok := c.get("a"); !ok {
		t.Fatal("recently used entry a was evicted")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("new entry c missing")
	}
}

func TestTarCache_replaceAdjustsBytes(t *testing.T) {
	// Given: a full cache.
	c := newTarCache(100)
	c.put(&tarCacheEntry{key: "a", data: make([]byte, 60)})

	// When: the same key is replaced with a smaller artifact (code updated).
	c.put(&tarCacheEntry{key: "a", data: make([]byte, 10)})
	c.put(&tarCacheEntry{key: "b", data: make([]byte, 80)})

	// Then: both fit — the byte accounting followed the replacement.
	if _, ok := c.get("a"); !ok {
		t.Fatal("replaced entry a missing")
	}
	if _, ok := c.get("b"); !ok {
		t.Fatal("entry b missing — stale byte accounting evicted it")
	}
}

func TestTarCache_oversizedEntryNotCached(t *testing.T) {
	// Given: a small cache holding one entry.
	c := newTarCache(50)
	c.put(&tarCacheEntry{key: "small", data: make([]byte, 20)})

	// When: an artifact bigger than the whole budget is offered.
	c.put(&tarCacheEntry{key: "huge", data: make([]byte, 51)})

	// Then: it is not cached, and it did not evict the resident entry.
	if _, ok := c.get("huge"); ok {
		t.Fatal("oversized entry should not be cached")
	}
	if _, ok := c.get("small"); !ok {
		t.Fatal("resident entry evicted by an uncacheable artifact")
	}
}

func TestTarCache_disabledIsNilAndSafe(t *testing.T) {
	// Given: caching disabled (LAMBDA_TAR_CACHE_MB=0).
	c := newTarCache(0)
	if c != nil {
		t.Fatal("disabled cache should be nil")
	}

	// When/Then: nil receivers are safe no-ops for every caller.
	c.put(&tarCacheEntry{key: "a", data: []byte("x")})
	if _, ok := c.get("a"); ok {
		t.Fatal("nil cache must never hit")
	}
	if _, ok := c.get(""); ok {
		t.Fatal("empty key must never hit")
	}
}
