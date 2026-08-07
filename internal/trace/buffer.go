package trace

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Buffer is a fixed-size, concurrency-safe ring buffer of trace entries keyed
// by request ID for O(1) lookup. When full, the oldest entry is evicted (FIFO).
type Buffer struct {
	mu       sync.RWMutex
	entries  []*Entry
	index    map[string]int
	head     int
	capacity int
}

// NewBuffer creates a ring buffer with the given capacity.
// Pass 0 or negative to get the default (1000).
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &Buffer{
		entries:  make([]*Entry, capacity),
		index:    make(map[string]int, capacity),
		capacity: capacity,
	}
}

// Store inserts or updates an entry. If the entry already exists (same
// RequestID), the slot is updated in-place. Otherwise the entry is written to
// the next ring slot, evicting the oldest entry when full. When full and a
// non-internal entry is being stored, the oldest *internal* entry is evicted
// preferentially, so polling traces never crowd out user requests.
func (b *Buffer) Store(e *Entry) {
	if e == nil || e.RequestID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if idx, ok := b.index[e.RequestID]; ok {
		b.entries[idx] = e
		return
	}
	if len(b.index) >= b.capacity {
		// When a non-internal entry arrives, try replacing an internal one.
		if !isInternalPath(e.Path) {
			for i := 0; i < b.capacity; i++ {
				idx := (b.head + i) % b.capacity
				if candidate := b.entries[idx]; candidate != nil && isInternalPath(candidate.Path) {
					delete(b.index, candidate.RequestID)
					b.entries[idx] = e
					b.index[e.RequestID] = idx
					return
				}
			}
		}
		// Replace the oldest (at head) and advance.
		if old := b.entries[b.head]; old != nil {
			delete(b.index, old.RequestID)
		}
		b.entries[b.head] = e
		b.index[e.RequestID] = b.head
		b.head = (b.head + 1) % b.capacity
		return
	}
	old := b.entries[b.head]
	if old != nil {
		delete(b.index, old.RequestID)
	}
	b.entries[b.head] = e
	b.index[e.RequestID] = b.head
	b.head = (b.head + 1) % b.capacity
}

// Get retrieves an entry by request ID. Returns nil, false if not found.
func (b *Buffer) Get(requestID string) (*Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx, ok := b.index[requestID]
	if !ok {
		return nil, false
	}
	return b.entries[idx], true
}

// Len returns the number of entries currently stored.
func (b *Buffer) Len() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.index)
}

// Capacity returns the maximum number of entries the buffer can hold.
func (b *Buffer) Capacity() int {
	if b == nil {
		return 0
	}
	return b.capacity
}

// ListSummaries is like List but returns lightweight Summary objects instead of
// full Entry pointers.
func (b *Buffer) ListSummaries(filter ListFilter) ([]Summary, string) {
	entries, cursor := b.List(filter)
	summaries := make([]Summary, len(entries))
	for i, e := range entries {
		summaries[i] = e.ToSummary()
	}
	return summaries, cursor
}

// ListFilter controls which entries List returns.
type ListFilter struct {
	Service string
	Method  string
	Path    string
	Status  string
	Search  string
	After   string // cursor for older entries (next page)
	Before  string // cursor for newer entries (polling for fresh data)
	HopsFor string // filter to entries whose hops contain this request ID (upstream)
	Limit   int
}

// List returns entries matching filter, ordered by timestamp descending
// (newest first), paginated. Returns the matching slice and a cursor for the
// next page (empty if this is the last page).
func (b *Buffer) List(filter ListFilter) ([]*Entry, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	candidates := make([]*Entry, 0, len(b.index))
	for _, e := range b.entries {
		if e == nil {
			continue
		}
		if !matchFilter(e, filter) {
			continue
		}
		candidates = append(candidates, e)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Timestamp.After(candidates[j].Timestamp)
	})

	start := 0
	if filter.Before != "" {
		// Return entries newer than the cursor (poll for fresh data).
		for i, e := range candidates {
			if e.RequestID == filter.Before {
				result := candidates[:i]
				var cursor string
				if len(result) > 0 {
					cursor = result[0].RequestID
				}
				return result, cursor
			}
		}
		// Cursor not found — return everything (all newer than "nothing").
		return candidates, ""
	}
	if filter.After != "" {
		found := false
		for i, e := range candidates {
			if e.RequestID == filter.After {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, ""
		}
	}
	if start >= len(candidates) {
		return nil, ""
	}

	end := start + limit
	if end > len(candidates) {
		end = len(candidates)
	}
	result := candidates[start:end]

	var nextCursor string
	if end < len(candidates) {
		nextCursor = candidates[end-1].RequestID
	}
	return result, nextCursor
}

func matchFilter(e *Entry, f ListFilter) bool {
	if f.Service != "" && !strings.EqualFold(e.Service, f.Service) {
		return false
	}
	if f.Method != "" && !strings.EqualFold(e.Method, f.Method) {
		return false
	}
	if f.Path != "" && !strings.Contains(e.Path, f.Path) {
		return false
	}
	if f.Status != "" {
		if !statusMatches(e.StatusCode, f.Status) {
			return false
		}
	}
	if f.Search != "" {
		s := strings.ToLower(f.Search)
		if !strings.Contains(strings.ToLower(e.RequestID), s) &&
			!strings.Contains(strings.ToLower(e.Path), s) &&
			!strings.Contains(strings.ToLower(e.Service), s) {
			return false
		}
	}
	if f.HopsFor != "" {
		found := false
		for _, h := range e.Hops {
			if h.RequestID == f.HopsFor {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func statusMatches(code int, filter string) bool {
	switch filter {
	case "2xx":
		return code >= 200 && code < 300
	case "3xx":
		return code >= 300 && code < 400
	case "4xx":
		return code >= 400 && code < 500
	case "5xx":
		return code >= 500 && code < 600
	default:
		if n, err := strconv.Atoi(filter); err == nil {
			return code == n
		}
		return true
	}
}
