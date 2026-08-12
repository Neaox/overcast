package trace

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Buffer is a fixed-size, concurrency-safe ring buffer of traces keyed by
// request ID for O(1) lookup. Internal traces are capped at capacity/5 so they
// can never crowd out user-facing requests.
//
// It holds live *Recorder values rather than immutable snapshots. A trace is
// registered once, when its request starts, and everything recorded afterwards
// — response status, duration, and hops from asynchronous work that outlives
// the request, such as CloudFormation stack provisioning — is visible to
// readers without the writer ever copying anything. Readers materialise a
// Summary or an Entry on demand.
type Buffer struct {
	mu            sync.RWMutex
	entries       []*Recorder
	index         map[string]int
	head          int
	capacity      int
	internalCount int
}

// NewBuffer creates a ring buffer with the given capacity.
// Pass 0 or negative to get the default (1000).
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &Buffer{
		entries:  make([]*Recorder, capacity),
		index:    make(map[string]int, capacity),
		capacity: capacity,
	}
}

// Add registers a trace. Call it once, as soon as the Recorder exists: the
// buffer holds the Recorder itself, so later writes to it need no second call.
// Re-registering the same request ID replaces the recorder in place.
//
// Internal traces (health, inbox, SSE, debug polling) are capped at capacity/5
// so they can never crowd out user-facing requests; once that quota is full, a
// new internal trace recycles the oldest internal entry instead of consuming
// user budget.
func (b *Buffer) Add(rec *Recorder) {
	if b == nil || rec == nil || rec.requestID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if idx, ok := b.index[rec.requestID]; ok {
		b.entries[idx] = rec
		return
	}

	internal := rec.internal
	maxInternal := max(b.capacity/5, 1)

	// Internal cap: when the quota is full, recycle the oldest internal
	// entry (ring semantics within the quota) so the freshest polls stay
	// visible without evicting user entries.
	if internal && b.internalCount >= maxInternal {
		if idx, ok := b.oldestInternalLocked(); ok {
			b.replaceLocked(idx, rec)
		}
		// No internal entry found despite a full quota can only mean the
		// accounting drifted; drop rather than evict a user entry.
		return
	}

	if len(b.index) >= b.capacity {
		// When a non-internal entry arrives, try reclaiming an internal slot.
		if !internal {
			if idx, ok := b.oldestInternalLocked(); ok {
				b.replaceLocked(idx, rec)
				return
			}
		}
		// Evict oldest (at head).
		b.replaceLocked(b.head, rec)
		return
	}

	// Not full: take the slot at head.
	old := b.entries[b.head]
	if old != nil {
		delete(b.index, old.requestID)
		if old.internal {
			b.internalCount--
		}
	}
	b.entries[b.head] = rec
	b.index[rec.requestID] = b.head
	b.head = (b.head + 1) % b.capacity
	if internal {
		b.internalCount++
	}
}

// oldestInternalLocked returns the slot of the oldest internal entry, scanning
// from head (oldest) forward. Callers must hold b.mu.
func (b *Buffer) oldestInternalLocked() (int, bool) {
	for i := 0; i < b.capacity; i++ {
		idx := (b.head + i) % b.capacity
		if c := b.entries[idx]; c != nil && c.internal {
			return idx, true
		}
	}
	return 0, false
}

// replaceLocked evicts the entry at idx and inserts rec as the newest entry,
// keeping internalCount in sync. To preserve FIFO fairness, rec never inherits
// the victim's slot position: the current head occupant (the oldest entry, if
// any) moves into the victim's slot, rec takes the head slot, and head advances
// — so the new entry gets a full lifetime instead of the victim's remaining
// one. Callers must hold b.mu.
func (b *Buffer) replaceLocked(idx int, rec *Recorder) {
	if victim := b.entries[idx]; victim != nil {
		delete(b.index, victim.requestID)
		if victim.internal {
			b.internalCount--
		}
	}
	if idx != b.head {
		moved := b.entries[b.head]
		b.entries[idx] = moved
		if moved != nil {
			b.index[moved.requestID] = idx
		}
	}
	b.entries[b.head] = rec
	b.index[rec.requestID] = b.head
	b.head = (b.head + 1) % b.capacity
	if rec.internal {
		b.internalCount++
	}
}

// Get materialises the full trace for a request ID, current as of this call.
// Returns false if no such trace is retained.
func (b *Buffer) Get(requestID string) (Entry, bool) {
	if b == nil {
		return Entry{}, false
	}
	b.mu.RLock()
	rec := b.recorderLocked(requestID)
	b.mu.RUnlock()
	if rec == nil {
		return Entry{}, false
	}
	// Deliberately outside b.mu: materialising deep-copies bodies and hops,
	// and holding the ring's lock for that would block every writer.
	return rec.Entry(), true
}

// recorderLocked returns the recorder for a request ID. Callers must hold b.mu.
func (b *Buffer) recorderLocked(requestID string) *Recorder {
	idx, ok := b.index[requestID]
	if !ok {
		return nil
	}
	return b.entries[idx]
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

// ListFilter controls which entries ListSummaries returns.
//
// Methods and Statuses are match-any sets: an entry matches if it matches any
// value in the set, and an empty set means "no filter". Empty strings within a
// set are ignored.
type ListFilter struct {
	Service  string
	Methods  []string
	Path     string
	Statuses []string
	Search   string
	After    string // cursor for older entries (next page)
	Before   string // cursor for newer entries (polling for fresh data)
	HopsFor  string // filter to entries whose hops carry this request ID (upstream)
	Limit    int
}

// compiledFilter is a ListFilter with its per-call normalisation already done.
// matchFilter runs once per retained trace on every list call — once a second
// from the web UI's poll — so lowercasing and pruning happen here, once, rather
// than inside that loop.
type compiledFilter struct {
	service  string
	methods  []string
	path     string
	statuses []string
	search   string // lowercased
	hopsFor  string
}

func compileFilter(f ListFilter) compiledFilter {
	return compiledFilter{
		service:  f.Service,
		methods:  nonEmpty(f.Methods),
		path:     f.Path,
		statuses: nonEmpty(f.Statuses),
		search:   strings.ToLower(f.Search),
		hopsFor:  f.HopsFor,
	}
}

// nonEmpty returns vals without its empty entries, reusing vals when it has
// none. An empty value must not be treated as "match everything" — a UI that
// submits `status=&status=4xx` means 4xx only.
func nonEmpty(vals []string) []string {
	for _, v := range vals {
		if v != "" {
			continue
		}
		out := make([]string, 0, len(vals))
		for _, keep := range vals {
			if keep != "" {
				out = append(out, keep)
			}
		}
		return out
	}
	return vals
}

// SplitFilterValues flattens repeated query parameters into one match-any set,
// expanding comma-separated values (`status=4xx,5xx`) and dropping blanks. It
// returns nil when nothing usable remains, which every filter reads as "no
// filter".
func SplitFilterValues(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// ListSummaries returns lightweight summaries of the traces matching filter,
// ordered by timestamp descending (newest first), paginated. It returns the
// matching slice and a cursor for the next page (empty if this is the last
// page). No bodies, headers, hops or log entries are copied.
func (b *Buffer) ListSummaries(filter ListFilter) ([]Summary, string) {
	if b == nil {
		return nil, ""
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f := compileFilter(filter)

	b.mu.RLock()
	candidates := make([]Summary, 0, len(b.index))
	for _, rec := range b.entries {
		if rec == nil {
			continue
		}
		// Lock-free rejections first: these read only fields fixed at
		// construction, so a deploy hammering AddHop never contends with them.
		if !matchesAny(f.methods, rec.method, methodMatches) {
			continue
		}
		if f.path != "" && !strings.Contains(rec.path, f.path) {
			continue
		}
		if f.hopsFor != "" && !rec.HasHop(f.hopsFor) {
			continue
		}
		if !rec.MatchesSearch(f.search) {
			continue
		}
		s := rec.Summary()
		if !matchSummary(s, f) {
			continue
		}
		candidates = append(candidates, s)
	}
	b.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Timestamp.After(candidates[j].Timestamp)
	})

	start := 0
	if filter.Before != "" {
		// Return entries newer than the cursor (poll for fresh data),
		// newest first, capped at limit.
		for i, s := range candidates {
			if s.RequestID == filter.Before {
				result := candidates[:i]
				if len(result) > limit {
					result = result[:limit]
				}
				var cursor string
				if len(result) > 0 {
					cursor = result[0].RequestID
				}
				return result, cursor
			}
		}
		// Cursor not found (evicted) — return the newest entries, still
		// honouring the limit.
		if len(candidates) > limit {
			candidates = candidates[:limit]
		}
		return candidates, ""
	}
	if filter.After != "" {
		found := false
		for i, s := range candidates {
			if s.RequestID == filter.After {
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

// matchSummary applies the filters that need fields only a live read can
// supply. The method and path filters are applied before the summary is taken,
// and the search term by Recorder.MatchesSearch — which reaches fields a
// Summary does not carry; see ListSummaries.
func matchSummary(s Summary, f compiledFilter) bool {
	if f.service != "" && !strings.EqualFold(s.Service, f.service) {
		return false
	}
	return matchesAny(f.statuses, s.StatusCode, statusMatches)
}

// matchesAny reports whether subject satisfies any of the filter values. An
// empty filter set matches everything. match must be a plain function, not a
// closure over the subject, so this allocates nothing per entry.
func matchesAny[T any](filters []string, subject T, match func(T, string) bool) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if match(subject, f) {
			return true
		}
	}
	return false
}

func methodMatches(method, filter string) bool {
	return strings.EqualFold(method, filter)
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
