package conformance

import (
	"fmt"
	"net/http"
	"sort"
	"time"
)

// checkFunc is one clause of §3. Every checkFunc is self-contained: it
// calls f.Reset() first so checks never interfere with each other's state,
// regardless of run order.
type checkFunc func(f Fixture) []Violation

// checks is every clause Check runs, in the order listed in the plan's §3
// walk-through. Keep this list and the "at minimum cover" list in
// docs/plans/inert-tier-rollout.md's I0 row in sync.
var checks = []checkFunc{
	checkCreateRead,
	checkUpdateMerge,
	checkDeleteThenRead,
	checkListStable,
	checkListPaginate,
	checkRoundtripFidelity,
	checkNoFabrication,
	checkNotFound,
	checkAlreadyExists,
	checkInvalidParameter,
	checkInvalidToken,
	checkARN,
	checkTimestamps,
	checkIdempotency,
	checkVerbDefault,
}

// Check runs the whole §3 contract against f and returns every violated
// clause. A Fixture that fully satisfies §3 returns nil.
func Check(f Fixture) []Violation {
	var violations []Violation
	for _, check := range checks {
		violations = append(violations, check(f)...)
	}
	return violations
}

// ---- 3.1 operation classes --------------------------------------------

func checkCreateRead(f Fixture) []Violation {
	const clause = "3.1/create-read"
	f.Reset()
	out, werr := f.call(f.Resource.Create, f.Input(InputFull, 1))
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}
	id, ok := out[f.Resource.IDField].(string)
	if !ok || id == "" {
		return one(clause, "Create response did not carry a non-empty %q", f.Resource.IDField)
	}
	read, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: id})
	if werr != nil {
		return one(clause, "Read immediately after Create returned %s", describe(werr))
	}
	if got, _ := read[f.Resource.IDField].(string); got != id {
		return one(clause, "Read returned %q=%q, want %q", f.Resource.IDField, got, id)
	}
	return nil
}

func checkUpdateMerge(f Fixture) []Violation {
	const clause = "3.1/update-merge"
	f.Reset()
	created, werr := f.call(f.Resource.Create, f.Input(InputFull, 2))
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}
	id := created[f.Resource.IDField]
	before, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: id})
	if werr != nil {
		return one(clause, "Read before Update returned %s", describe(werr))
	}

	// LastModifiedTime has to visibly move between Create and Update, so put
	// a detectable gap in both time sources this clause can encounter.
	//
	// Advancing the injected clock is the one that matters: a §3.5-conforming
	// implementation reads clock.Clock, which no amount of real-time sleeping
	// moves, so sleeping alone would report a false violation against exactly
	// the implementations this contract mandates. The sleep stays for a
	// Fixture that declares no clock, and for a wall-clock implementation on a
	// platform whose clock resolution is coarse enough that two back-to-back
	// calls land on the same timestamp. Detecting *that* an implementation
	// reads wall-clock time is 3.5/timestamps' job, not this clause's.
	if f.Clock != nil {
		f.Clock.Add(time.Second)
	}
	time.Sleep(2 * time.Millisecond)

	patch := f.Input(InputUpdate, 2)
	if _, werr := f.call(f.Resource.Update, patch); werr != nil {
		return one(clause, "Update returned %s", describe(werr))
	}
	after, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: id})
	if werr != nil {
		return one(clause, "Read after Update returned %s", describe(werr))
	}

	var viols []Violation
	for field := range patch {
		if field == f.Resource.IDField {
			continue
		}
		if !fieldEqual(after[field], patch[field]) {
			viols = append(viols, violation(clause, "field %q was not applied: sent %v, Read after Update returned %v", field, patch[field], after[field]))
		}
	}
	for field, want := range before {
		if field == f.Resource.IDField || field == f.Resource.ModifiedTimeField {
			continue
		}
		if _, touched := patch[field]; touched {
			continue
		}
		if !fieldEqual(after[field], want) {
			viols = append(viols, violation(clause, "untouched field %q changed by Update: was %v, now %v", field, want, after[field]))
		}
	}
	if f.Resource.ModifiedTimeField != "" && fieldEqual(after[f.Resource.ModifiedTimeField], before[f.Resource.ModifiedTimeField]) {
		viols = append(viols, violation(clause, "%q was not refreshed by Update", f.Resource.ModifiedTimeField))
	}
	return viols
}

func checkDeleteThenRead(f Fixture) []Violation {
	const clause = "3.1/delete-then-read"
	f.Reset()
	created, werr := f.call(f.Resource.Create, f.Input(InputFull, 3))
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}
	id := created[f.Resource.IDField]
	if _, werr := f.call(f.Resource.Delete, map[string]any{f.Resource.IDField: id}); werr != nil {
		return one(clause, "Delete returned %s", describe(werr))
	}
	_, werr = f.call(f.Resource.Read, map[string]any{f.Resource.IDField: id})
	if werr == nil {
		return one(clause, "Read after Delete succeeded instead of returning the modeled not-found error")
	}
	if werr.Code != f.Errors.NotFound || werr.HTTPStatus != f.Errors.NotFoundStatus {
		return one(clause, "Read after Delete returned %s, want %s", describe(werr), describeCode(f.Errors.NotFound, f.Errors.NotFoundStatus))
	}
	return nil
}

func checkListStable(f Fixture) []Violation {
	const clause = "3.1/list-stable"
	f.Reset()
	var created []string
	for i := range 4 {
		out, werr := f.call(f.Resource.Create, f.Input(InputFull, 400+i))
		if werr != nil {
			return one(clause, "Create returned %s", describe(werr))
		}
		created = append(created, fmt.Sprint(out[f.Resource.IDField]))
	}

	first, werr := f.call(f.Resource.List, map[string]any{})
	if werr != nil {
		return one(clause, "List returned %s", describe(werr))
	}
	ids1 := idsFromItems(first[f.Resource.ItemsField], f.Resource.IDField)
	second, werr := f.call(f.Resource.List, map[string]any{})
	if werr != nil {
		return one(clause, "second List returned %s", describe(werr))
	}
	ids2 := idsFromItems(second[f.Resource.ItemsField], f.Resource.IDField)

	var viols []Violation
	if !stringsEqual(ids1, ids2) {
		viols = append(viols, violation(clause, "List order changed between two calls with no writes in between: %v then %v", ids1, ids2))
	}
	sorted := append([]string(nil), ids1...)
	sort.Strings(sorted)
	if !stringsEqual(ids1, sorted) {
		viols = append(viols, violation(clause, "List is not stable-sorted by %s: got %v, want %v", f.Resource.IDField, ids1, sorted))
	}
	for _, id := range created {
		if !contains(ids1, id) {
			viols = append(viols, violation(clause, "List did not include created record %q", id))
		}
	}
	return viols
}

func checkListPaginate(f Fixture) []Violation {
	const clause = "3.1/list-paginate"
	f.Reset()
	want := map[string]bool{}
	for i := range 5 {
		out, werr := f.call(f.Resource.Create, f.Input(InputFull, 300+i))
		if werr != nil {
			return one(clause, "Create returned %s", describe(werr))
		}
		want[fmt.Sprint(out[f.Resource.IDField])] = true
	}

	seen := map[string]int{}
	token, pages := "", 0
	for {
		fields := map[string]any{}
		if f.Resource.LimitField != "" {
			fields[f.Resource.LimitField] = 2
		}
		if token != "" && f.Resource.TokenRequestField != "" {
			fields[f.Resource.TokenRequestField] = token
		}
		out, werr := f.call(f.Resource.List, fields)
		if werr != nil {
			return one(clause, "List page %d returned %s", pages+1, describe(werr))
		}
		for _, id := range idsFromItems(out[f.Resource.ItemsField], f.Resource.IDField) {
			seen[id]++
		}
		pages++
		next, _ := out[f.Resource.TokenResponseField].(string)
		if next == "" || pages > 10 {
			break
		}
		token = next
	}

	var viols []Violation
	if pages < 2 {
		viols = append(viols, violation(clause, "List never paginated across %d records at page size 2 — the limit/token members are ignored", len(want)))
	}
	for id := range want {
		if seen[id] != 1 {
			viols = append(viols, violation(clause, "record %q appeared %d times across the page sequence, want exactly 1", id, seen[id]))
		}
	}
	return viols
}

// ---- 3.2 fidelity -------------------------------------------------------

func checkRoundtripFidelity(f Fixture) []Violation {
	const clause = "3.2/roundtrip-fidelity"
	f.Reset()
	in := f.Input(InputFull, 5)
	created, werr := f.call(f.Resource.Create, in)
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}
	read, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: created[f.Resource.IDField]})
	if werr != nil {
		return one(clause, "Read returned %s", describe(werr))
	}
	var viols []Violation
	for _, field := range f.Resource.RoundtripFields {
		sent, ok := in[field]
		if !ok {
			continue
		}
		got, present := read[field]
		if !present || !fieldEqual(got, sent) {
			viols = append(viols, violation(clause, "field %q was sent as %v but Read returned %v (present=%v)", field, sent, got, present))
		}
	}
	return viols
}

func checkNoFabrication(f Fixture) []Violation {
	const clause = "3.2/no-fabrication"
	if len(f.Resource.OutputOnlyFields) == 0 {
		return nil
	}
	f.Reset()
	created, werr := f.call(f.Resource.Create, f.Input(InputMinimal, 6))
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}
	read, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: created[f.Resource.IDField]})
	if werr != nil {
		return one(clause, "Read returned %s", describe(werr))
	}
	var viols []Violation
	for _, field := range f.Resource.OutputOnlyFields {
		got, present := read[field]
		if !present {
			continue
		}
		if def, hasDefault := f.Resource.Defaults[field]; hasDefault {
			if !fieldEqual(got, def) {
				viols = append(viols, violation(clause, "field %q was never sent, has no caller value, and Read returned %v instead of the modeled default %v", field, got, def))
			}
			continue
		}
		viols = append(viols, violation(clause, "field %q was never sent and has no declared default, but Read returned %v", field, got))
	}
	return viols
}

// ---- 3.3 errors -----------------------------------------------------------

func checkNotFound(f Fixture) []Violation {
	const clause = "3.3/not-found"
	f.Reset()
	_, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: "conformance-nonexistent-id"})
	if werr == nil {
		return one(clause, "Read of a nonexistent id succeeded")
	}
	if werr.Code != f.Errors.NotFound || werr.HTTPStatus != f.Errors.NotFoundStatus {
		return one(clause, "Read of a nonexistent id returned %s, want %s", describe(werr), describeCode(f.Errors.NotFound, f.Errors.NotFoundStatus))
	}
	return nil
}

func checkAlreadyExists(f Fixture) []Violation {
	const clause = "3.3/already-exists"
	f.Reset()
	in := f.Input(InputFull, 7)
	if _, werr := f.call(f.Resource.Create, in); werr != nil {
		return one(clause, "first Create returned %s", describe(werr))
	}
	_, werr := f.call(f.Resource.Create, in)
	if werr == nil {
		return one(clause, "duplicate Create (same %s) succeeded instead of returning the modeled already-exists error", f.Resource.IDField)
	}
	if werr.Code != f.Errors.AlreadyExists || werr.HTTPStatus != f.Errors.AlreadyExistsStatus {
		return one(clause, "duplicate Create returned %s, want %s", describe(werr), describeCode(f.Errors.AlreadyExists, f.Errors.AlreadyExistsStatus))
	}
	return nil
}

func checkInvalidParameter(f Fixture) []Violation {
	const clause = "3.3/invalid-parameter"
	f.Reset()
	_, werr := f.call(f.Resource.Create, f.Input(InputInvalid, 8))
	if werr == nil {
		return one(clause, "Create with a missing required field succeeded instead of returning the modeled invalid-parameter error")
	}
	if werr.Code != f.Errors.InvalidParameter || werr.HTTPStatus != f.Errors.InvalidParameterStatus {
		return one(clause, "Create with a missing required field returned %s, want %s", describe(werr), describeCode(f.Errors.InvalidParameter, f.Errors.InvalidParameterStatus))
	}
	return nil
}

func checkInvalidToken(f Fixture) []Violation {
	const clause = "3.3/invalid-token"
	if f.Resource.TokenRequestField == "" {
		return nil
	}
	f.Reset()
	for i := range 3 {
		if _, werr := f.call(f.Resource.Create, f.Input(InputFull, 500+i)); werr != nil {
			return one(clause, "Create returned %s", describe(werr))
		}
	}
	_, werr := f.call(f.Resource.List, map[string]any{f.Resource.TokenRequestField: "conformance-not-a-real-token"})
	if werr == nil {
		return one(clause, "List with a garbage continuation token succeeded instead of returning the modeled invalid-token error (it must not silently restart at page 1 — pagination-plan H1/G3)")
	}
	if werr.Code != f.Errors.InvalidToken || werr.HTTPStatus != f.Errors.InvalidTokenStatus {
		return one(clause, "List with a garbage continuation token returned %s, want %s", describe(werr), describeCode(f.Errors.InvalidToken, f.Errors.InvalidTokenStatus))
	}
	return nil
}

// ---- 3.5 identifiers, ARNs, timestamps, idempotency -----------------------

func checkARN(f Fixture) []Violation {
	const clause = "3.5/arn"
	if f.Resource.ArnField == "" {
		return nil
	}
	f.Reset()
	created, werr := f.call(f.Resource.Create, f.Input(InputFull, 9))
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}
	arn1, _ := created[f.Resource.ArnField].(string)
	if !arnPattern(f.Service).MatchString(arn1) {
		return one(clause, "Create returned ARN %q, want arn:aws:%s:<region>:<account>:<path>", arn1, f.Service)
	}
	read, werr := f.call(f.Resource.Read, map[string]any{f.Resource.IDField: created[f.Resource.IDField]})
	if werr != nil {
		return one(clause, "Read returned %s", describe(werr))
	}
	arn2, _ := read[f.Resource.ArnField].(string)
	if arn1 != arn2 {
		return one(clause, "ARN changed between Create (%q) and Read (%q)", arn1, arn2)
	}
	return nil
}

func checkTimestamps(f Fixture) []Violation {
	const clause = "3.5/timestamps"
	if f.Resource.CreationTimeField == "" || f.Clock == nil {
		return nil
	}
	fixed := testClockTime()

	f.Reset()
	f.Clock.Set(fixed)
	a, werr := f.call(f.Resource.Create, f.Input(InputFull, 10))
	if werr != nil {
		return one(clause, "Create returned %s", describe(werr))
	}

	f.Reset()
	f.Clock.Set(fixed)
	b, werr := f.call(f.Resource.Create, f.Input(InputFull, 10))
	if werr != nil {
		return one(clause, "second Create returned %s", describe(werr))
	}

	// A third Create at a *different* injected instant. Both directions are
	// needed. Same-time-equality alone passes a time.Now() handler whenever the
	// resource's timestamp is second-granular — time.RFC3339 without fractional
	// seconds, or epoch seconds, which is the common case across AWS shapes —
	// because two wall-clock reads a few hundred microseconds apart format
	// identically. Only moving the injected clock and requiring the output to
	// move with it proves the handler reads clock.Clock at all. It also catches
	// the neighbouring fabrication: a hardcoded constant timestamp, which
	// satisfies the equality check trivially.
	f.Reset()
	f.Clock.Set(fixed.Add(72 * time.Hour))
	c, werr := f.call(f.Resource.Create, f.Input(InputFull, 10))
	if werr != nil {
		return one(clause, "third Create returned %s", describe(werr))
	}

	tsA, tsB := a[f.Resource.CreationTimeField], b[f.Resource.CreationTimeField]
	tsC := c[f.Resource.CreationTimeField]
	if fieldEqual(tsA, "") || tsA == nil {
		return one(clause, "%s was empty", f.Resource.CreationTimeField)
	}
	if !fieldEqual(tsA, tsB) {
		return one(clause, "%s differed across two Creates at the same injected clock time (%v then %v) — this only happens when the handler reads time.Now() instead of the injected clock.Clock", f.Resource.CreationTimeField, tsA, tsB)
	}
	if fieldEqual(tsA, tsC) {
		return one(clause, "%s did not move when the injected clock moved 72h (%v then %v) — the handler is ignoring the injected clock.Clock, either by reading time.Now() at a granularity coarse enough to hide it or by emitting a constant", f.Resource.CreationTimeField, tsA, tsC)
	}
	return nil
}

func checkIdempotency(f Fixture) []Violation {
	const clause = "3.5/idempotency"
	if f.Resource.IdempotencyField == "" {
		return nil
	}
	f.Reset()
	in := f.Input(InputIdempotent, 11)
	first, werr := f.call(f.Resource.Create, in)
	if werr != nil {
		return one(clause, "first Create returned %s", describe(werr))
	}
	second, werr := f.call(f.Resource.Create, in)
	if werr != nil {
		return one(clause, "repeat Create with the same %s returned %s instead of the original record", f.Resource.IdempotencyField, describe(werr))
	}
	id1, id2 := fmt.Sprint(first[f.Resource.IDField]), fmt.Sprint(second[f.Resource.IDField])
	if id1 != id2 {
		return one(clause, "repeat Create with the same %s produced a different record (%q then %q) instead of returning the original", f.Resource.IdempotencyField, id1, id2)
	}
	return nil
}

// ---- 3.6 verb operations ----------------------------------------------

func checkVerbDefault(f Fixture) []Violation {
	const clause = "3.6/verb-default"
	if f.Resource.Verb == "" {
		return nil
	}
	f.Reset()
	_, werr := f.call(f.Resource.Verb, map[string]any{})
	if werr == nil {
		return one(clause, "the plain verb operation %q returned success instead of the protocol-correct NotImplemented — Tier 1 never claims to have done something it did not do", f.Resource.Verb)
	}
	if werr.Code != "NotImplemented" || werr.HTTPStatus != http.StatusNotImplemented {
		return one(clause, "the plain verb operation %q returned %s, want %s", f.Resource.Verb, describe(werr), describeCode("NotImplemented", http.StatusNotImplemented))
	}
	return nil
}

// ---- shared helpers -----------------------------------------------------

func one(clause, format string, args ...any) []Violation {
	return []Violation{violation(clause, format, args...)}
}

func describe(werr *WireError) string {
	if werr == nil {
		return "no error"
	}
	return fmt.Sprintf("%s/%d", werr.Code, werr.HTTPStatus)
}

func describeCode(code string, status int) string {
	return fmt.Sprintf("%s/%d", code, status)
}

func fieldEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func idsFromItems(raw any, idField string) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m[idField]; ok {
			ids = append(ids, fmt.Sprint(id))
		}
	}
	return ids
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
