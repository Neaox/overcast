package trace

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxInternalRing bounds the internal ring however large the user floor gets.
//
// The internal share used to be capacity/5, which is 200 at the default floor
// of 1000 — and would be 2,000 once that floor is raised, which is 2,000
// retained health checks nobody will ever read. Two hundred is already several
// minutes of polling at the rate the UI and the health check run at.
const maxInternalRing = 200

// Buffer is a concurrency-safe store of traces keyed by request ID for O(1)
// lookup, holding them in two age-ordered rings: one for user-facing requests
// and one for the infrastructure polling isInternalPath classifies.
//
// They are separate rings rather than one ring with a quota because a shared
// ring cannot be both fair and cheap. Enforcing the quota inside it meant
// scanning for the oldest internal entry on every internal admission — a walk
// of the whole ring, on every health check — and reclaiming a slot from the
// middle meant shuffling entries, which cost the ring the one property worth
// having: that position tracks age. Split, each ring evicts by advancing its
// head, and polling that happens whether or not anyone is looking can no longer
// displace a request somebody made on purpose.
//
// It holds live *Recorder values rather than immutable snapshots. A trace is
// registered once, when its request starts, and everything recorded afterwards
// — response status, duration, and hops from asynchronous work that outlives
// the request, such as CloudFormation stack provisioning — is visible to
// readers without the writer ever copying anything. Readers materialise a
// Summary or an Entry on demand.
type Buffer struct {
	mu       sync.RWMutex
	live     *ring
	internal *ring
	index    map[string]*slot
}

// NewBuffer creates a buffer whose user-facing ring holds capacity traces.
// Pass 0 or negative to get the default (1000).
//
// The internal ring is sized alongside that rather than carved out of it, so
// the capacity asked for is the number of user-facing traces actually retained.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1000
	}
	internalCap := min(capacity/5, maxInternalRing)
	return &Buffer{
		live:     newRing(capacity),
		internal: newRing(internalCap),
		index:    make(map[string]*slot, capacity+internalCap),
	}
}

// Add registers a trace. Call it once, as soon as the Recorder exists: the
// buffer holds the Recorder itself, so later writes to it need no second call.
// Re-registering the same request ID replaces the recorder in place, keeping
// the entry's position and ring.
//
// Internal traces (health, inbox, SSE, debug polling) go to their own ring, so
// they evict each other and never a user-facing request.
func (b *Buffer) Add(rec *Recorder) {
	if b == nil || rec == nil || rec.requestID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.index[rec.requestID]; ok {
		s.rec = rec
		return
	}
	target := b.live
	if rec.internal {
		target = b.internal
	}
	s := &slot{rec: rec}
	if evicted := target.push(s); evicted != nil {
		delete(b.index, evicted.rec.requestID)
	}
	b.index[rec.requestID] = s
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
	// and holding the buffer's lock for that would block every writer.
	e := rec.Entry()
	b.inlineHopBodies(&e)
	return e, true
}

// inlineHopBodies fills each hop's bodies in from the trace of the call that
// hop records, and says why when it cannot.
//
// A hop retains no bodies of its own; this is where a reader gets them back.
// The work is in two passes so neither is done under the wrong lock: the index
// lookups need b.mu and are O(1) each, while copying the bodies is the
// expensive part and needs only each callee's own lock.
func (b *Buffer) inlineHopBodies(e *Entry) {
	if len(e.Hops) == 0 {
		return
	}

	sources := make([]*Recorder, len(e.Hops))
	b.mu.RLock()
	for i, hop := range e.Hops {
		if hop.RequestID == "" {
			continue
		}
		if s, ok := b.index[hop.RequestID]; ok {
			sources[i] = s.rec
		}
	}
	b.mu.RUnlock()

	budget := MaxInlinedHopBodies
	for i := range e.Hops {
		hop := &e.Hops[i]
		if hop.RequestID == "" {
			// Not a dispatched call, so there is no trace to resolve against
			// and nothing has been lost.
			continue
		}
		if sources[i] == nil {
			hop.RequestBodyOmitted, hop.ResponseBodyOmitted = OmitEvicted, OmitEvicted
			continue
		}
		req, reqOmit, resp, respOmit := sources[i].bodies()
		if len(req)+len(resp) > budget {
			hop.RequestBodyOmitted, hop.ResponseBodyOmitted = OmitTraceBudget, OmitTraceBudget
			continue
		}
		budget -= len(req) + len(resp)
		hop.RequestBody, hop.RequestBodyOmitted = req, reqOmit
		hop.ResponseBody, hop.ResponseBodyOmitted = resp, respOmit
	}
}

// recorderLocked returns the recorder for a request ID. Callers must hold b.mu.
func (b *Buffer) recorderLocked(requestID string) *Recorder {
	s, ok := b.index[requestID]
	if !ok {
		return nil
	}
	return s.rec
}

// Len returns the number of entries currently stored, across both rings.
func (b *Buffer) Len() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.index)
}

// Capacity returns the maximum number of entries the buffer can hold, across
// both rings — so Len never exceeds it.
func (b *Buffer) Capacity() int {
	if b == nil {
		return 0
	}
	return b.live.cap() + b.internal.cap()
}

// eachNewestFirstLocked calls fn for every retained recorder, newest first,
// merging the two rings by timestamp. fn returns false to stop the walk.
//
// Each ring is insertion-ordered, and a recorder's timestamp is fixed at
// construction, so this is a two-way merge over two already-ordered sequences.
// Insertion order and timestamp order can differ by the microseconds between
// clk.Now() and Add under concurrency; callers that care sort the page they
// collected, which is tens of entries rather than the whole buffer.
//
// Callers must hold b.mu.
func (b *Buffer) eachNewestFirstLocked(fn func(*Recorder) bool) {
	i, j := b.live.len()-1, b.internal.len()-1
	for i >= 0 || j >= 0 {
		var rec *Recorder
		switch {
		case j < 0:
			rec, i = b.live.at(i).rec, i-1
		case i < 0:
			rec, j = b.internal.at(j).rec, j-1
		default:
			l, n := b.live.at(i).rec, b.internal.at(j).rec
			if l.timestamp.Before(n.timestamp) {
				rec, j = n, j-1
			} else {
				rec, i = l, i-1
			}
		}
		if rec == nil {
			continue
		}
		if !fn(rec) {
			return
		}
	}
}

// eachRecorderLocked calls fn for every retained recorder, in no particular
// order. For callers that impose their own ordering afterwards.
// Callers must hold b.mu.
func (b *Buffer) eachRecorderLocked(fn func(*Recorder)) {
	for _, r := range [...]*ring{b.live, b.internal} {
		for i := 0; i < r.len(); i++ {
			if s := r.at(i); s != nil && s.rec != nil {
				fn(s.rec)
			}
		}
	}
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

	// One page, not the whole buffer. The walk stops as soon as it has what it
	// was asked for, so a deeper ring costs the poll nothing — and the slice is
	// sized to the page rather than to everything retained, which is what used
	// to allocate a hundred kilobytes a second behind the UI's 1 Hz list.
	var (
		page   = make([]Summary, 0, limit+1)
		found  bool // Before: the cursor was reached
		passed bool // After: the cursor was passed
	)

	b.mu.RLock()
	switch {
	case filter.Before != "":
		// Entries newer than the cursor, newest first — the UI polling for
		// fresh data. Stopping at limit is what makes the miss case (an evicted
		// cursor) cost the same as the hit case.
		//
		// Whether the cursor still exists decides the cursor handed back, and a
		// walk that stops at limit may never reach it. So settle that from the
		// index instead: O(1), and it asks the same question the full scan used
		// to answer — is the cursor still retained *and* still matching?
		if s, ok := b.index[filter.Before]; ok {
			_, found = summaryIfMatch(s.rec, f)
		}
		b.eachNewestFirstLocked(func(rec *Recorder) bool {
			s, ok := summaryIfMatch(rec, f)
			if !ok {
				return true
			}
			if s.RequestID == filter.Before {
				return false
			}
			page = append(page, s)
			return len(page) < limit
		})
	case filter.After != "":
		b.eachNewestFirstLocked(func(rec *Recorder) bool {
			s, ok := summaryIfMatch(rec, f)
			if !ok {
				return true
			}
			if !passed {
				passed = s.RequestID == filter.After
				return true
			}
			page = append(page, s)
			return len(page) <= limit
		})
	default:
		b.eachNewestFirstLocked(func(rec *Recorder) bool {
			s, ok := summaryIfMatch(rec, f)
			if !ok {
				return true
			}
			page = append(page, s)
			return len(page) <= limit
		})
	}
	b.mu.RUnlock()

	// The merge orders across rings exactly; this settles the microsecond skew
	// between insertion and timestamp order within one. It sorts a page, not a
	// buffer.
	sort.Slice(page, func(i, j int) bool {
		return page[i].Timestamp.After(page[j].Timestamp)
	})

	if filter.Before != "" {
		var cursor string
		if found && len(page) > 0 {
			cursor = page[0].RequestID
		}
		return page, cursor
	}
	if filter.After != "" && !passed {
		// The cursor is gone, so "the page after it" has no answer. Saying so
		// beats silently restarting from the newest entry.
		return nil, ""
	}
	if len(page) == 0 {
		return nil, ""
	}
	// One entry past the page was collected only to answer "is there more?".
	var nextCursor string
	if len(page) > limit {
		page = page[:limit]
		nextCursor = page[limit-1].RequestID
	}
	return page, nextCursor
}

// summaryIfMatch applies every filter to rec and materialises its Summary when
// it passes.
//
// The lock-free rejections come first deliberately: they read only fields fixed
// at construction, so a deploy hammering AddHop never contends with a list
// call. Only a trace that survives them pays for Summary's read lock.
func summaryIfMatch(rec *Recorder, f compiledFilter) (Summary, bool) {
	if !matchesAny(f.methods, rec.method, methodMatches) {
		return Summary{}, false
	}
	if f.path != "" && !strings.Contains(rec.path, f.path) {
		return Summary{}, false
	}
	if f.hopsFor != "" && !rec.HasHop(f.hopsFor) {
		return Summary{}, false
	}
	if !rec.MatchesSearch(f.search) {
		return Summary{}, false
	}
	s := rec.Summary()
	if !matchSummary(s, f) {
		return Summary{}, false
	}
	return s, true
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
