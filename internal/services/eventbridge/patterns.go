package eventbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// patternCacheMax bounds how many distinct event patterns are kept compiled.
// PutEvents evaluates every rule on the bus against every entry, so parsing
// the pattern JSON per (event, rule) pair is the one avoidable cost on that
// path; a rule's pattern is immutable for the life of that rule revision, so
// the parsed form is cached by pattern text. The cache is cleared wholesale
// when it reaches the limit rather than evicted entry by entry — patterns are
// small, the limit is far above any realistic rule count, and a clear is a
// handful of re-parses rather than a correctness problem.
const patternCacheMax = 512

// patternCache holds parsed event patterns keyed by their JSON text.
type patternCache struct {
	mu       sync.RWMutex
	compiled map[string]map[string]any
}

func newPatternCache() *patternCache {
	return &patternCache{compiled: make(map[string]map[string]any)}
}

// matches reports whether event satisfies pattern, parsing and caching the
// pattern on first use. An unparseable pattern never matches.
func (c *patternCache) matches(pattern string, event map[string]any) bool {
	if c == nil {
		return eventPatternMatches(pattern, event)
	}
	c.mu.RLock()
	parsed, ok := c.compiled[pattern]
	c.mu.RUnlock()
	if !ok {
		if json.Unmarshal([]byte(pattern), &parsed) != nil {
			parsed = nil
		}
		c.mu.Lock()
		if len(c.compiled) >= patternCacheMax {
			c.compiled = make(map[string]map[string]any, patternCacheMax)
		}
		c.compiled[pattern] = parsed
		c.mu.Unlock()
	}
	if parsed == nil {
		return false
	}
	return matchPatternMap(parsed, event)
}

// eventPatternMatches parses and evaluates a pattern without the cache. It is
// the uncached reference the cache is tested against.
func eventPatternMatches(pattern string, event map[string]any) bool {
	p, err := parseEventPattern(pattern)
	if err != nil {
		return false
	}
	return matchPatternMap(p, event)
}

// parseEventPattern decodes a pattern document, reporting why it is not one.
// Rule delivery treats an unparseable pattern as never matching; TestEventPattern
// surfaces the reason as InvalidEventPatternException instead.
func parseEventPattern(pattern string) (map[string]any, error) {
	var p map[string]any
	if err := json.Unmarshal([]byte(pattern), &p); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errors.New("pattern must be a JSON object")
	}
	return p, nil
}

func matchPatternMap(pattern, event map[string]any) bool {
	for key, want := range pattern {
		got, ok := event[key]
		if !ok {
			return false
		}
		switch wantTyped := want.(type) {
		case []any:
			if !matchAnyValue(wantTyped, got) {
				return false
			}
		case map[string]any:
			gotMap, ok := got.(map[string]any)
			if !ok || !matchPatternMap(wantTyped, gotMap) {
				return false
			}
		default:
			if fmt.Sprint(wantTyped) != fmt.Sprint(got) {
				return false
			}
		}
	}
	return true
}

func matchAnyValue(want []any, got any) bool {
	for _, candidate := range want {
		if fmt.Sprint(candidate) == fmt.Sprint(got) {
			return true
		}
	}
	return false
}
