package lambda

// tar_cache.go — byte-bounded LRU for cold-start artifacts that were being
// rebuilt identically on every cold start (Phase 2.2 of
// docs/plans/lambda-cold-start.md): code tars keyed by the function's
// CodeHash and layer tars keyed by layer-version ARN (immutable by
// definition). A hit also skips materializing the deployment package from
// the store. Filling happens on demand — the first cold start of a code
// version pays the conversion once, subsequent cold starts of the same code
// skip it. (Background pre-fill after deploy settle is deferred until the
// proactive-init phase introduces the settle debounce.)

import (
	"container/list"
	"sync"
)

// tarCacheEntry is one cached artifact.
type tarCacheEntry struct {
	key  string
	data []byte
	// extensions holds the external extension names discovered in a layer
	// zip, so a cache hit skips re-scanning the archive too. Nil for code
	// entries.
	extensions []string
}

func (e *tarCacheEntry) size() int64 { return int64(len(e.data)) }

// tarCache is a mutex-guarded LRU bounded by total artifact bytes.
// A nil cache is valid and never hits — callers need no guards.
type tarCache struct {
	mu       sync.Mutex
	maxBytes int64
	curBytes int64
	entries  map[string]*list.Element // value: *tarCacheEntry
	order    *list.List               // front = most recently used
}

// newTarCache returns a cache bounded to maxBytes, or nil when maxBytes <= 0
// (caching disabled).
func newTarCache(maxBytes int64) *tarCache {
	if maxBytes <= 0 {
		return nil
	}
	return &tarCache{
		maxBytes: maxBytes,
		entries:  make(map[string]*list.Element),
		order:    list.New(),
	}
}

// get returns the entry for key and marks it most recently used.
func (c *tarCache) get(key string) (*tarCacheEntry, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*tarCacheEntry), true
}

// stats reports the cache's current entry count, resident bytes, and budget.
// A nil cache reports zeros.
func (c *tarCache) stats() (entries int, bytes, maxBytes int64) {
	if c == nil {
		return 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.curBytes, c.maxBytes
}

// put stores entry, evicting least-recently-used entries past the byte
// budget. An entry bigger than the whole budget is not cached at all — it
// would only evict everything else for a single-function benefit.
func (c *tarCache) put(entry *tarCacheEntry) {
	if c == nil || entry == nil || entry.key == "" || entry.size() > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[entry.key]; ok {
		c.curBytes += entry.size() - el.Value.(*tarCacheEntry).size()
		el.Value = entry
		c.order.MoveToFront(el)
	} else {
		c.entries[entry.key] = c.order.PushFront(entry)
		c.curBytes += entry.size()
	}
	for c.curBytes > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		evicted := oldest.Value.(*tarCacheEntry)
		c.order.Remove(oldest)
		delete(c.entries, evicted.key)
		c.curBytes -= evicted.size()
	}
}
