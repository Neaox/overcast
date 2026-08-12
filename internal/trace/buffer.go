package trace

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Neaox/overcast/internal/clock"
)

// maxInternalRing bounds the internal ring however large the user floor gets.
//
// The internal share used to be capacity/5, which is 200 at the default floor
// of 1000 — and would be 2,000 once that floor is raised, which is 2,000
// retained health checks nobody will ever read. Two hundred is already several
// minutes of polling at the rate the UI and the health check run at.
const maxInternalRing = 200

// maxFamilyShare is the fraction of the pinned ring a failure's calls may
// occupy, as a divisor: 2 means at most half.
//
// Pinning a failure also pins the calls it made, and a CDK deploy makes
// thousands of them. Without a share limit one deploy fills the ring, and every
// failure arriving afterwards pushes an older failure out — the family
// displacing failures a step later rather than at the moment it was pinned,
// which is the loophole in "a family never displaces another failure".
//
// Capping the share closes it: failures always have the rest of the ring, so
// they compete with failures and never with somebody else's call log.
const maxFamilyShare = 2

// Buffer is a concurrency-safe store of traces keyed by request ID for O(1)
// lookup, holding them in three age-ordered rings: `live` for user-facing
// requests, `pinned` for the ones that went wrong, and `internal` for the
// infrastructure polling isInternalPath classifies.
//
// A trace enters `live`, and when the live ring finally evicts it, retireLocked
// decides whether it is worth pinning — see there for why that decision cannot
// be made any earlier.
//
// **A trace can be in two rings at once.** Pinning a failure also pins the calls
// it made (pinFamilyLocked), and those are usually still live, so `pinned` and
// `live` both reference them. The index holds one entry per trace regardless,
// and both iterators skip the pinned copy of a still-live trace so that a page
// — or a deep search — never sees it twice.
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
	pinned   *ring
	internal *ring
	index    map[string]*slot
	policy   RetentionPolicy
	clk      clock.Clock
	// bytes is the retained body total the byte backstop bounds. It is
	// maintained by dropLocked and Settle — the two places a trace's
	// contribution can change — so that it never has to be recomputed.
	bytes int64
	// dropped counts what retention has reclaimed, by rule. See RetentionStats.
	dropped DroppedCounts
	// familyPinned is how much of the pinned ring is currently held by calls
	// pinned alongside a failure. Maintained wherever a slot enters or leaves
	// that ring: a counter that only rose would stop families being pinned at
	// all after the first few deploys.
	familyPinned int
}

// NewBuffer creates a fixed-size buffer holding capacity user-facing traces.
// Pass 0 or negative to get the default (1000).
//
// It is deliberately *not* the retention policy: no growth, no age cull, no
// pinning. A caller asking for a buffer of three means three. Production opts
// into retention explicitly through NewBufferWithPolicy, so that the extra
// behaviour is something a reader can see being switched on rather than a
// default that follows a number around.
//
// The internal ring is sized alongside that rather than carved out of it, so
// the capacity asked for is the number of user-facing traces actually retained.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return NewBufferWithPolicy(RetentionPolicy{Floor: capacity, Ceiling: capacity}, nil)
}

// NewBufferWithPolicy creates a buffer retaining traces under p, reading time
// from clk. The clock is injected because the age cull is the first part of
// this package that depends on the passage of time, and a wall-clock test of it
// would be flaky by construction.
func NewBufferWithPolicy(p RetentionPolicy, clk clock.Clock) *Buffer {
	p = p.normalise()
	if clk == nil {
		clk = clock.New()
	}
	internalCap := min(p.Floor/5, maxInternalRing)
	return &Buffer{
		// The live ring starts at the floor and grows towards the ceiling only
		// if a burst arrives, so an idle emulator never allocates for one.
		live:     newRing(p.Floor),
		pinned:   newRing(p.Pinned),
		internal: newRing(internalCap),
		index:    make(map[string]*slot, p.Floor+internalCap),
		policy:   p,
		clk:      clk,
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

	if rec.internal {
		s := &slot{rec: rec}
		if evicted := b.internal.push(s); evicted != nil {
			b.dropLocked(evicted, dropInternal)
		}
		b.index[rec.requestID] = s
		return
	}

	// The age cull runs here rather than on a timer: arrivals are the only
	// thing that can make retention wrong, and a buffer nobody writes to has
	// nothing to reclaim.
	b.cullLocked()
	b.growLiveLocked()

	s := &slot{rec: rec, inLive: true}
	if evicted := b.live.push(s); evicted != nil {
		b.retireLocked(evicted, dropCapacity)
	}
	b.index[rec.requestID] = s
}

// Settle accounts for a trace's retained size once its response is recorded.
// The DebugTrace middleware calls it after the handler returns.
//
// Size is sampled here rather than maintained as the trace is built: the bodies
// are final at this point, and a running total updated from AddHop and AddLog
// would touch the hottest paths in a deploy thousands of times per trace to
// keep a number read rarely. What grows afterwards — hop metadata and capped
// log entries — is the small, bounded remainder.
func (b *Buffer) Settle(rec *Recorder) {
	if b == nil || rec == nil || rec.requestID == "" {
		return
	}
	size := rec.retainedBytes()

	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.index[rec.requestID]
	if !ok || s.rec != rec {
		// Already evicted, or the ID was re-registered with a different
		// recorder; either way this measurement is not about what is held.
		return
	}
	b.bytes += size - s.bytes
	s.bytes = size
	b.enforceBytesLocked()
}

// RetainedBytes reports the request and response body bytes currently held.
func (b *Buffer) RetainedBytes() int64 {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.bytes
}

// Cull reclaims overflow that has aged past the retention window, and anything
// over the byte budget. Add does both; this is for callers that want it to
// happen without an arrival.
func (b *Buffer) Cull() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cullLocked()
	b.enforceBytesLocked()
}

// enforceBytesLocked reclaims the oldest live traces until the retained bodies
// fit the budget, stopping at the floor.
//
// The order is the whole design: ordinary overflow above the floor goes first,
// and a pinned failure is surrendered only when there is nothing cheaper left.
// Rule 1 still wins outright — the floor is a promise, and a floor that is
// itself over budget is the operator's own configuration.
//
// **Pinned traces are reclaimable, and were not before.** Exempting them meant
// the budget was not a bound at all: pinned retention is capped by *count*, so
// the worst case was PinnedLimit traces of up to ~2 MiB each — around 2 GB at
// the shipped defaults — however small the byte budget was set. Someone
// lowering it because their machine was struggling would have found it did
// nothing about the memory actually being held, which is the one job it has.
// Callers must hold b.mu.
func (b *Buffer) enforceBytesLocked() {
	if b.policy.Bytes <= 0 {
		return
	}
	// Ordinary overflow first. retireLocked may pin what it pops, which
	// reclaims nothing — that is fine here, because the pinned ring is the next
	// thing this drains.
	for b.bytes > b.policy.Bytes && b.live.len() > b.policy.Floor {
		s := b.live.popOldest()
		if s == nil {
			return
		}
		b.retireLocked(s, dropBytes)
	}
	// Then failures, oldest first. Popping is what makes this terminate, so a
	// slot still reachable from the live ring is popped too: it merely loses
	// its pin, survives until the live ring evicts it, and is re-pinned then if
	// it still qualifies and there is room.
	for b.bytes > b.policy.Bytes && b.pinned.len() > 0 {
		s := b.pinned.popOldest()
		if s == nil {
			return
		}
		b.unpinLocked(s)
		if s.inLive {
			continue
		}
		b.dropLocked(s, dropBytes)
	}
}

// cullLocked drops live traces older than the window, down to the floor.
//
// Age is positional, so this is a walk from the head that stops at the first
// entry still inside the window — O(culled), with no scan and no sweeper.
// Callers must hold b.mu.
func (b *Buffer) cullLocked() {
	if b.policy.Window <= 0 {
		return
	}
	cutoff := b.clk.Now().Add(-b.policy.Window)
	for b.live.len() > b.policy.Floor {
		s := b.live.oldest()
		if s == nil || !s.rec.timestamp.Before(cutoff) {
			return
		}
		b.live.popOldest()
		b.retireLocked(s, dropAged)
	}
}

// growLiveLocked enlarges the live ring when a burst has filled it, doubling
// towards the ceiling. Callers must hold b.mu.
func (b *Buffer) growLiveLocked() {
	if b.live.len() < b.live.cap() || b.live.cap() >= b.policy.Ceiling {
		return
	}
	b.live.grow(min(b.live.cap()*2, b.policy.Ceiling))
}

// retireLocked decides what becomes of a trace leaving the live ring: pinned if
// it went wrong, dropped otherwise.
//
// **This is where a trace is classified, and it cannot happen earlier.** Add
// runs at request start, before a status exists, and a CloudFormation hop can
// fail minutes later on a goroutine that outlives the request. Eviction is the
// last possible moment, and therefore the most informed one — a trace that
// fails after this point was never going to be saved by an earlier check.
// Callers must hold b.mu.
func (b *Buffer) retireLocked(s *slot, why dropReason) {
	s.inLive = false
	if s.inPinned {
		// Already reachable from the pinned ring; the live ring merely dropped
		// one of the two references.
		return
	}
	if b.policy.Pinned > 0 && qualifiesPinned(s.rec) {
		b.pinLocked(s)
		b.pinFamilyLocked(s)
		return
	}
	b.dropLocked(s, why)
}

// pinFamilyLocked keeps the calls a pinned failure made.
//
// Pinning a failure that cannot show what it did is half a rescue. Each hop
// names a trace of its own — that is where its bodies live now — and those
// traces are *newer* than the parent, so FIFO evicts the parent first and the
// children moments after. The pinned deploy would then be a timeline whose
// every body reports OmitEvicted.
//
// The family rides along in whatever room is left, and is bounded to a share of
// the ring (maxFamilyShare) so that **it cannot spend the failure budget**.
//
// Stopping at the ring being full was not enough. The calls stay in the ring
// afterwards, so the next failure to arrive evicts the oldest entry — which
// could be an older failure that a deploy's call log had crowded towards the
// head. The family displaced a failure a step later rather than at the moment
// it was pinned. Capping the share closes that: failures always have the rest
// of the ring, so they compete with failures and never with somebody else's
// call log.
//
// A deploy with more calls than its share keeps the ones that fit; the rest
// degrade to OmitEvicted, which the UI already explains.
// Callers must hold b.mu.
func (b *Buffer) pinFamilyLocked(parent *slot) {
	limit := b.pinned.cap() / maxFamilyShare
	for _, id := range parent.rec.hopRequestIDList() {
		if b.pinned.len() >= b.pinned.cap() || b.familyPinned >= limit {
			return
		}
		child, ok := b.index[id]
		if !ok || child.inPinned {
			continue
		}
		child.viaFamily = true
		b.familyPinned++
		b.pinLocked(child)
	}
}

// dropLocked forgets a trace entirely, returning its bytes to the budget.
//
// Every path that removes an index entry goes through here. Accounting that
// only ever goes up is a budget that ratchets shut, and the failure mode is
// silent: retention quietly shrinks to the floor and stays there.
// Callers must hold b.mu.
func (b *Buffer) dropLocked(s *slot, why dropReason) {
	b.countDrop(why)
	b.bytes -= s.bytes
	s.bytes = 0
	delete(b.index, s.rec.requestID)
}

// unpinLocked clears a slot's pinned membership, keeping the family share
// accounting in step.
//
// Every path that removes a slot from the pinned ring goes through here, for
// the same reason dropLocked owns the byte accounting: a counter that only ever
// rises is one that silently stops working, and here that means no family is
// ever pinned again after the first few deploys.
// Callers must hold b.mu.
func (b *Buffer) unpinLocked(s *slot) {
	s.inPinned = false
	if s.viaFamily {
		s.viaFamily = false
		b.familyPinned--
	}
}

// pinLocked moves a slot onto the pinned ring. Callers must hold b.mu.
func (b *Buffer) pinLocked(s *slot) {
	s.inPinned = true
	if evicted := b.pinned.push(s); evicted != nil {
		b.unpinLocked(evicted)
		if !evicted.inLive {
			b.dropLocked(evicted, dropPinnedCap)
		}
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
	return b.live.cap() + b.pinned.cap() + b.internal.cap()
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
func (b *Buffer) eachNewestFirstLocked(fn func(*slot) bool) {
	rings := [...]*ring{b.live, b.pinned, b.internal}
	// Cursor into each ring, newest first.
	var at [len(rings)]int
	for i, r := range rings {
		at[i] = r.len() - 1
	}
	for {
		// Pick whichever ring's current entry is newest. A slot is in exactly
		// one of these — retireLocked clears inLive before pinning — so no
		// trace is yielded twice.
		pick := -1
		for i, r := range rings {
			if at[i] < 0 {
				continue
			}
			if pick < 0 || rings[pick].at(at[pick]).rec.timestamp.Before(r.at(at[i]).rec.timestamp) {
				pick = i
			}
		}
		if pick < 0 {
			return
		}
		sl := rings[pick].at(at[pick])
		at[pick]--
		if sl == nil || sl.rec == nil {
			continue
		}
		// A failure pinned while its trace is still live is reachable from two
		// rings. Yield it from `live` only, or a page shows it twice.
		if rings[pick] == b.pinned && sl.inLive {
			continue
		}
		if !fn(sl) {
			return
		}
	}
}

// eachRecorderLocked calls fn for every retained recorder, in no particular
// order. For callers that impose their own ordering afterwards.
// Callers must hold b.mu.
func (b *Buffer) eachRecorderLocked(fn func(*Recorder)) {
	for _, r := range [...]*ring{b.live, b.pinned, b.internal} {
		for i := 0; i < r.len(); i++ {
			s := r.at(i)
			if s == nil || s.rec == nil {
				continue
			}
			// See eachNewestFirstLocked: a pinned-and-still-live trace is in
			// two rings, and a deep search must not scan it twice.
			if r == b.pinned && s.inLive {
				continue
			}
			fn(s.rec)
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
			_, found = summaryIfMatch(s, f)
		}
		b.eachNewestFirstLocked(func(sl *slot) bool {
			s, ok := summaryIfMatch(sl, f)
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
		b.eachNewestFirstLocked(func(sl *slot) bool {
			s, ok := summaryIfMatch(sl, f)
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
		b.eachNewestFirstLocked(func(sl *slot) bool {
			s, ok := summaryIfMatch(sl, f)
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
func summaryIfMatch(sl *slot, f compiledFilter) (Summary, bool) {
	rec := sl.rec
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
	s.Pinned = sl.inPinned
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
