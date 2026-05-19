package cloudfront

import (
	"strings"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

type cfCacheEntry struct {
	statusCode int
	headers    map[string][]string
	body       []byte
	tags       []string
	expiresAt  time.Time
}

type cfCache struct {
	mu      sync.RWMutex
	entries map[string]*cfCacheEntry
	clk     clock.Clock
}

func newCFCache(clk clock.Clock) *cfCache {
	return &cfCache{clk: clk, entries: make(map[string]*cfCacheEntry)}
}

// get returns a valid entry or nil if not found / expired.
func (c *cfCache) get(key string) *cfCacheEntry {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	if c.clk.Now().After(e.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil
	}
	return e
}

// set stores a response entry with the given TTL and optional cache tags.
func (c *cfCache) set(key string, e *cfCacheEntry) {
	c.mu.Lock()
	c.entries[key] = e
	c.mu.Unlock()
}

// invalidate removes all entries whose keys match a CloudFront invalidation
// path pattern (supports "/*" and "/prefix/*" wildcards) or a tag pattern
// (patterns starting with "#" invalidate by cache tag).
// distID is used to scope invalidations to a single distribution.
func (c *cfCache) invalidate(distID, pattern string) int {
	if strings.HasPrefix(pattern, "#") {
		return c.invalidateTag(distID, pattern[1:])
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := distID + ":"
	count := 0
	for k := range c.entries {
		if len(k) <= len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		path := k[len(prefix):]
		if matchInvalidationPattern(pattern, path) {
			delete(c.entries, k)
			count++
		}
	}
	return count
}

// invalidateTag removes all cache entries for a distribution that contain
// the given tag. Tag comparison is case-insensitive.
func (c *cfCache) invalidateTag(distID, tag string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := distID + ":"
	count := 0
	for k, e := range c.entries {
		if len(k) <= len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		for _, t := range e.tags {
			if strings.EqualFold(t, tag) {
				delete(c.entries, k)
				count++
				break
			}
		}
	}
	return count
}

// invalidateAll removes all cache entries for a distribution.
func (c *cfCache) invalidateAll(distID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := distID + ":"
	for k := range c.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
}

// matchInvalidationPattern returns true if path matches a CloudFront
// invalidation pattern. Supports:
//   - exact match: "/images/logo.png"
//   - suffix wildcard: "/images/*" matches "/images/logo.png" and "/images/sub/logo.png"
//   - root wildcard: "/*" matches everything
func matchInvalidationPattern(pattern, path string) bool {
	if pattern == "/*" {
		return true
	}
	if !strings.HasSuffix(pattern, "*") {
		return pattern == path
	}
	prefix := pattern[:len(pattern)-1]
	return strings.HasPrefix(path, prefix)
}

// isValidCacheTag returns true if the tag value is valid for tag-based invalidation.
// Tags must be ASCII visible chars (33-126), no spaces, no commas, max 256 chars.
func isValidCacheTag(tag string) bool {
	if len(tag) == 0 || len(tag) > 256 {
		return false
	}
	for _, r := range tag {
		if r < 33 || r > 126 || r == ' ' || r == ',' {
			return false
		}
	}
	return true
}

// parseCacheTags splits a comma-separated tag header value into individual
// tags, trimming whitespace and filtering out invalid/empty tags.
// Returns at most 50 tags per AWS behavior.
func parseCacheTags(headerValue string) []string {
	parts := strings.Split(headerValue, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" || !isValidCacheTag(t) {
			continue
		}
		tags = append(tags, t)
		if len(tags) >= 50 {
			break
		}
	}
	return tags
}
