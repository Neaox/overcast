package trace

// Deep search — finding a trace by something it said.
//
// Recorder.MatchesSearch matches the short scalar fields and stops there,
// because everything else a trace holds is unbounded: up to MaxHopBodyBytes of
// hop bodies and 500 log entries each, across a ring of a thousand. Scanning
// all of it is a gigabyte-scale job in the worst case, which is exactly the
// case worth searching — a CDK deploy fills the ring with traces at the cap.
//
// So this is not a list filter. It is a budgeted, resumable, cancellable scan:
// one call walks backwards from a cursor until it has scanned its budget,
// returns what it found and where it stopped, and the caller decides whether to
// continue. There is no server-side job to garbage-collect and nothing to race
// with the ring evicting entries.
//
// See docs/plans/trace-deep-search.md for the design and the numbers.

import (
	"bytes"
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultScanBudget is how many bytes of body and log text one DeepSearch call
// will read before pausing and handing back a cursor.
//
// It is a budget rather than a limit on traces because traces are not the unit
// of cost: one deploy trace carrying 8 MiB of hop bodies is worth two thousand
// ordinary ones. 64 MiB keeps a single call to a few tens of milliseconds on
// any machine that can run the emulator, which is what makes the progress the
// caller renders move smoothly instead of in lurches.
const DefaultScanBudget = 64 << 20 // 64 MiB

// DefaultMaxMatches bounds what one call returns. A query like "e" matches
// nearly every body in the ring, and a caller that asked for it should get a
// page of results and a cursor, not a hundred megabytes of excerpts.
const DefaultMaxMatches = 50

// excerptRadius is how much text either side of a match travels with it. Enough
// to place the match in its sentence, short enough that a page of matches stays
// readable.
const excerptRadius = 60

// MatchField names where in a trace a deep search found its query. It is the
// caller's label for the match, so the values are the wire's, not Go's.
type MatchField string

const (
	MatchLog          MatchField = "log"
	MatchHopError     MatchField = "hopError"
	MatchHopResponse  MatchField = "hopResponse"
	MatchHopRequest   MatchField = "hopRequest"
	MatchRequestBody  MatchField = "requestBody"
	MatchResponseBody MatchField = "responseBody"
)

// Match is one trace a deep search matched, and where.
//
// Before/Text/After are the excerpt, split rather than concatenated so a caller
// highlights the match without searching for it again — and, more importantly,
// without having to guess which occurrence was the one that matched.
type Match struct {
	RequestID string     `json:"requestId"`
	Field     MatchField `json:"field"`
	HopID     string     `json:"hopId,omitempty"`
	// Label places the match: "warn · cfn: stack failed" for a log line,
	// "ecr.DescribeImages → 400" for a hop.
	Label  string `json:"label"`
	Before string `json:"before,omitempty"`
	Text   string `json:"text,omitempty"`
	After  string `json:"after,omitempty"`
	// Binary marks a match inside something that is not text — CBOR, protobuf,
	// a compressed payload. Rendering an excerpt of it produces mojibake, so
	// Offset says where it hit instead and the excerpt fields stay empty.
	Binary bool `json:"binary,omitempty"`
	Offset int  `json:"offset,omitempty"`
	// Summary is the trace's list row, carried along so a caller can render the
	// result without a second lookup that might find the trace already evicted.
	Summary Summary `json:"summary"`
}

// DeepFilter is one call's worth of deep search.
type DeepFilter struct {
	Query string
	// Cursor is where to resume, as returned by a previous call. Empty starts
	// at the newest trace.
	Cursor string
	// IncludeInternal scans the health checks, SSE streams and debug polls the
	// UI hides. Off by default: they are a fifth of the ring and nobody
	// searching for a deploy failure means them.
	IncludeInternal bool
	// Budget and MaxMatches default to DefaultScanBudget and
	// DefaultMaxMatches. Present so a test can make the budget bite without
	// building a gigabyte of fixtures.
	Budget     int
	MaxMatches int
}

// DeepResult is what one call scanned and found.
type DeepResult struct {
	Matches []Match `json:"matches"`
	// NextCursor resumes the scan. Empty when Done.
	NextCursor string `json:"nextCursor,omitempty"`
	// Done reports that the scan reached the oldest retained trace. A caller
	// that stops before this has stopped early — by its own choice, or because
	// Cancelled.
	Done bool `json:"done"`
	// Cancelled reports that the caller's context ended the scan. Distinct from
	// Done: one means "there is no more", the other "we stopped".
	Cancelled bool `json:"cancelled,omitempty"`
	// Scanned and Remaining drive a progress indicator. Remaining counts traces
	// this scan has not reached yet, which is the number a reader wants — not
	// bytes, which they have no feel for.
	Scanned   int `json:"scanned"`
	Remaining int `json:"remaining"`
}

// DeepSearch scans one budget's worth of retained traces, newest first, for
// traces containing query in a body, a hop error or a log line.
//
// A trace is reported once however many times it matches: the caller renders
// one row per trace, and ten matches inside one body would bury every other
// trace that also matched.
func (b *Buffer) DeepSearch(ctx context.Context, filter DeepFilter) DeepResult {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if b == nil || query == "" {
		return DeepResult{Done: true}
	}
	budget := filter.Budget
	if budget <= 0 {
		budget = DefaultScanBudget
	}
	maxMatches := filter.MaxMatches
	if maxMatches <= 0 {
		maxMatches = DefaultMaxMatches
	}

	candidates := b.scanOrder(filter.IncludeInternal)
	start := resumeAt(candidates, filter.Cursor)

	needle := []byte(query)
	result := DeepResult{Matches: []Match{}}
	spent := 0

	for i := start; i < len(candidates); i++ {
		// Cancellation and budget are the same decision — "should this call
		// keep going" — so they are asked in one place, before each trace. A
		// trace is the coarsest unit this may check at; see scanTrace for the
		// finer-grained check inside one.
		if ctx.Err() != nil {
			result.Cancelled = true
			result.NextCursor = cursorFor(candidates[i])
			result.Remaining = len(candidates) - i
			return result
		}
		if spent >= budget || len(result.Matches) >= maxMatches {
			result.NextCursor = cursorFor(candidates[i])
			result.Remaining = len(candidates) - i
			return result
		}

		rec := candidates[i]
		match, cost, cancelled := scanTrace(ctx, rec, needle)
		spent += cost
		result.Scanned++
		if cancelled {
			result.Cancelled = true
			result.NextCursor = cursorFor(rec)
			result.Remaining = len(candidates) - i
			return result
		}
		if match != nil {
			result.Matches = append(result.Matches, *match)
		}
	}

	result.Done = true
	return result
}

// scanOrder returns the recorders to scan, newest first. Taking the whole
// ordering on every call rather than caching it is deliberate: the ring is
// bounded at a thousand, sorting it costs microseconds, and a cached ordering
// would have to be invalidated by every arriving request — which is the one
// thing happening constantly while someone searches.
func (b *Buffer) scanOrder(includeInternal bool) []*Recorder {
	b.mu.RLock()
	candidates := make([]*Recorder, 0, len(b.index))
	for _, rec := range b.entries {
		if rec == nil || (rec.internal && !includeInternal) {
			continue
		}
		candidates = append(candidates, rec)
	}
	b.mu.RUnlock()

	// Newest first, ties broken by request ID so the order is total: two traces
	// recorded in the same instant would otherwise make the cursor ambiguous,
	// and a cursor that cannot say "after this one" either stalls or skips.
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].timestamp.Equal(candidates[j].timestamp) {
			return candidates[i].timestamp.After(candidates[j].timestamp)
		}
		return candidates[i].requestID > candidates[j].requestID
	})
	return candidates
}

// cursorFor encodes a position in the scan order: the timestamp and request ID
// of the next trace to look at.
//
// It is a keyset cursor rather than an index because the ring moves underneath
// a scan — traces arrive at the head and the oldest are evicted between calls —
// and an index into a snapshot would silently point at a different trace by the
// time it came back.
func cursorFor(rec *Recorder) string {
	return strconv.FormatInt(rec.timestamp.UnixNano(), 10) + ":" + rec.requestID
}

// resumeAt returns the index the cursor points at, or the start of the order
// when there is no cursor.
//
// A cursor whose trace has been evicted resumes at the first trace that sorts
// at or after it rather than giving up. Returning "not found" would end a scan
// half way and report it as complete, which is the failure this is written to
// avoid — the ring evicts constantly while a deploy runs, and the oldest
// entries are exactly the ones a long scan is still working towards.
func resumeAt(candidates []*Recorder, cursor string) int {
	if cursor == "" {
		return 0
	}
	nanos, id, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0
	}
	ts, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return 0
	}
	target := time.Unix(0, ts)
	return sort.Search(len(candidates), func(i int) bool {
		rec := candidates[i]
		if !rec.timestamp.Equal(target) {
			return !rec.timestamp.After(target)
		}
		return rec.requestID <= id
	})
}

// searchableFields is the set of fields one trace can be searched through, taken as
// slice headers rather than copies.
//
// Nothing here is copied because nothing here is mutated after it is recorded:
// AddHop appends a Hop whose bodies it has already bounded and never touches
// again, and AddLog appends a LogEntry whose Fields map is built fresh by
// ZapFieldsToMap. Appending may reallocate the backing array, which leaves this
// snapshot pointing at the old one — still valid, just missing whatever arrived
// mid-scan, which is the right answer for a search over a live system.
//
// If that invariant ever changes, this becomes a data race. It is stated on
// AddHop for that reason.
type searchableFields struct {
	summary  Summary
	hops     []Hop
	logs     []LogEntry
	reqBody  []byte
	respBody []byte
}

// snapshot takes the searchable view of a trace under one brief read lock. The
// scanning happens outside it: holding the recorder's lock across a scan of 8
// MiB would block the AddHop of the very deploy being searched for.
func (r *Recorder) snapshot() searchableFields {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return searchableFields{
		summary:  r.summaryLocked(),
		hops:     r.entry.Hops,
		logs:     r.entry.LogEntries,
		reqBody:  r.entry.RequestBody,
		respBody: r.entry.ResponseBody,
	}
}

// scanTrace looks for needle in one trace, returning the first match, the bytes
// it read, and whether the caller's context ended the scan part way.
//
// The cancellation check is per hop rather than per trace: a single trace may
// hold 8 MiB across hundreds of hops, so checking only between traces would let
// an abandoned search run for a noticeable fraction of a second after the
// client had gone.
func scanTrace(ctx context.Context, rec *Recorder, needle []byte) (*Match, int, bool) {
	snap := rec.snapshot()
	cost := 0

	// Log entries first: they are the smallest and the likeliest to hold a
	// human-readable statement of what went wrong.
	for _, entry := range snap.logs {
		cost += len(entry.Message)
		if at := indexFoldString(entry.Message, needle); at >= 0 {
			return newMatch(rec, snap, MatchLog, "", logLabel(entry), []byte(entry.Message), at, len(needle)), cost, false
		}
		for key, value := range entry.Fields {
			text, ok := value.(string)
			if !ok {
				continue
			}
			cost += len(text)
			if at := indexFoldString(text, needle); at >= 0 {
				return newMatch(rec, snap, MatchLog, "", logLabel(entry)+" "+key, []byte(text), at, len(needle)), cost, false
			}
		}
	}

	for i := range snap.hops {
		if i%16 == 0 && ctx.Err() != nil {
			return nil, cost, true
		}
		hop := &snap.hops[i]
		if at := indexFoldString(hop.Error, needle); at >= 0 {
			cost += len(hop.Error)
			return newMatch(rec, snap, MatchHopError, hop.ID, hopLabel(hop), []byte(hop.Error), at, len(needle)), cost, false
		}
		cost += len(hop.Error)

		for _, candidate := range []struct {
			field MatchField
			body  []byte
		}{
			{MatchHopResponse, hop.ResponseBody},
			{MatchHopRequest, hop.RequestBody},
		} {
			cost += len(candidate.body)
			if at := indexFold(candidate.body, needle); at >= 0 {
				return newMatch(rec, snap, candidate.field, hop.ID, hopLabel(hop), candidate.body, at, len(needle)), cost, false
			}
		}
	}

	for _, candidate := range []struct {
		field MatchField
		body  []byte
	}{
		{MatchResponseBody, snap.respBody},
		{MatchRequestBody, snap.reqBody},
	} {
		cost += len(candidate.body)
		if at := indexFold(candidate.body, needle); at >= 0 {
			return newMatch(rec, snap, candidate.field, "", entryLabel(snap.summary), candidate.body, at, len(needle)), cost, false
		}
	}

	return nil, cost, false
}

func logLabel(entry LogEntry) string {
	if entry.Level == "" {
		return entry.Message
	}
	return entry.Level + " · " + entry.Message
}

func hopLabel(hop *Hop) string {
	label := hop.Service
	if hop.Operation != "" {
		label += "." + hop.Operation
	}
	if hop.ResponseStatus > 0 {
		label += " → " + strconv.Itoa(hop.ResponseStatus)
	}
	return label
}

func entryLabel(summary Summary) string {
	if summary.Operation != "" {
		return summary.Service + "." + summary.Operation
	}
	return summary.Method + " " + summary.Path
}

// newMatch builds a match in a byte slice that may or may not be text. length
// is the needle's, which is the matched span's — case folding never changes it
// for the ASCII these queries are.
func newMatch(rec *Recorder, snap searchableFields, field MatchField, hopID, label string, body []byte, at, length int) *Match {
	match := &Match{
		RequestID: rec.requestID,
		Field:     field,
		HopID:     hopID,
		Label:     label,
		Summary:   snap.summary,
	}
	if !utf8.Valid(body) {
		match.Binary = true
		match.Offset = at
		return match
	}
	match.Before, match.Text, match.After = excerptAt(body, at, length)
	return match
}

// indexFold reports the index of needle in haystack, ignoring ASCII case.
// needle must already be lowercase.
//
// It allocates nothing, which is the whole point: the obvious implementation —
// bytes.Contains(bytes.ToLower(body), needle) — copies every body it looks at,
// and the bodies here run to a megabyte each.
func indexFold(haystack, needle []byte) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	first := needle[0]
	upper := first
	if first >= 'a' && first <= 'z' {
		upper = first - ('a' - 'A')
	}

	for i := 0; i+len(needle) <= len(haystack); {
		// Skip to the next byte that could start a match. Two IndexByte scans
		// are still far faster than stepping byte by byte in Go.
		rest := haystack[i : len(haystack)-len(needle)+1]
		lowerAt := bytes.IndexByte(rest, first)
		next := lowerAt
		if upper != first {
			if upperAt := bytes.IndexByte(rest, upper); upperAt >= 0 && (next < 0 || upperAt < next) {
				next = upperAt
			}
		}
		if next < 0 {
			return -1
		}
		i += next
		if equalFoldASCII(haystack[i:i+len(needle)], needle) {
			return i
		}
		i++
	}
	return -1
}

// indexFoldString is indexFold over a string, for the fields a trace holds as
// strings rather than bytes — hop errors and log messages.
//
// It exists rather than the caller writing indexFold([]byte(s), needle) because
// that conversion copies the string, once per hop, on a path whose whole claim
// is that it scans without copying. A trace with three hundred hops would have
// paid three hundred allocations to look at fields that are usually empty.
func indexFoldString(haystack string, needle []byte) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	first := needle[0]
	upper := first
	if first >= 'a' && first <= 'z' {
		upper = first - ('a' - 'A')
	}

	for i := 0; i+len(needle) <= len(haystack); {
		rest := haystack[i : len(haystack)-len(needle)+1]
		next := strings.IndexByte(rest, first)
		if upper != first {
			if upperAt := strings.IndexByte(rest, upper); upperAt >= 0 && (next < 0 || upperAt < next) {
				next = upperAt
			}
		}
		if next < 0 {
			return -1
		}
		i += next
		if equalFoldASCIIString(haystack[i:i+len(needle)], needle) {
			return i
		}
		i++
	}
	return -1
}

// equalFoldASCII compares for equality, folding ASCII case only. b is already
// lowercase.
//
// Non-ASCII bytes are compared exactly. A query lowercased by strings.ToLower
// therefore matches non-ASCII text only where the text is already in that case,
// which is a documented approximation rather than a bug: full Unicode folding
// over a megabyte of body, per byte offset, is not what this is for.
func equalFoldASCII(a, b []byte) bool {
	for i := range b {
		if foldASCII(a[i]) != b[i] {
			return false
		}
	}
	return true
}

// equalFoldASCIIString is equalFoldASCII over a string haystack.
func equalFoldASCIIString(a string, b []byte) bool {
	for i := range b {
		if foldASCII(a[i]) != b[i] {
			return false
		}
	}
	return true
}

// foldASCII lowercases an ASCII letter and leaves every other byte alone, so a
// non-ASCII byte is compared exactly. See equalFoldASCII.
func foldASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// excerptAt splits text around the match at offset for rendering: the run-up,
// the matched span itself, and what follows.
//
// The boundaries are snapped to rune starts. Slicing a UTF-8 body at an
// arbitrary byte offset otherwise yields a leading or trailing replacement
// character, which looks like corruption in the payload rather than an artefact
// of the excerpt.
func excerptAt(body []byte, at, length int) (before, matched, after string) {
	start := at - excerptRadius
	if start < 0 {
		start = 0
	}
	end := at + length + excerptRadius
	if end > len(body) {
		end = len(body)
	}
	start = alignRuneStart(body, start)
	end = alignRuneStart(body, end)
	matchEnd := at + length
	if matchEnd > len(body) {
		matchEnd = len(body)
	}
	return string(body[start:at]), string(body[at:matchEnd]), string(body[matchEnd:end])
}

// alignRuneStart moves an index back to the start of the rune it lands inside.
func alignRuneStart(body []byte, i int) int {
	for i > 0 && i < len(body) && !utf8.RuneStart(body[i]) {
		i--
	}
	return i
}
