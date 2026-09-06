package scenario

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

// This file's counterpart is compat/suites/cli/internal/scenario/failure.go:
// the two build the same six-field failure message (below) for their own
// backend and are not byte-identical, but a change to the message shape here
// usually needs a matching change there — change both or neither.

// Debuggability is the generated backend's whole cost, and it is paid here:
// one helper builds every failure message and every clause uses it, so a
// generated failure carries as much as a hand-written one would.
//
// compat/model/README.md § Failure messages fixes the six fields and their
// order:
//
//  1. group/test
//  2. the operation — of the primary call, or of the clause's own call
//  3. the exact params JSON sent, after evaluating every expression
//  4. the assertion kind and, for checks/where, the path
//  5. expected vs actual
//  6. the scenario file and the step index
//
// Rendered:
//
//	sqs-gen-queue/SetQueueAttributes: GetQueueAttributes params {"AttributeNames":["All"],"QueueUrl":"http://…"}: readback equals at $.Attributes.VisibilityTimeout: expected "60", actual "30" (compat/model/scenarios/sqs.json assert[0].assert)
//
// The wording avoids every phrase harness.LooksUnimplemented matches on, so
// this package's own prose can never turn an assertion failure into a false
// "unimplemented". The SDK's error text is quoted verbatim where it is the
// actual value, which is what lets a genuine 501 still be classified — by the
// sentinel, not by the message.
type failure struct {
	group    string
	test     string
	op       string
	params   string
	kind     string
	path     string
	expected string
	actual   string
	file     string
	step     string
}

func (f *failure) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s: %s", f.group, f.test, f.op)
	if f.params != "" {
		fmt.Fprintf(&b, " params %s", f.params)
	}
	fmt.Fprintf(&b, ": %s", f.kind)
	if f.path != "" {
		fmt.Fprintf(&b, " at %s", f.path)
	}
	fmt.Fprintf(&b, ": expected %s, actual %s (%s %s)", f.expected, f.actual, f.file, f.step)
	return b.String()
}

// ComposedFailure marks this message as this package's own, so
// harness.IsUnimplemented never runs its "501" substring test over it: field 3
// is the params JSON, and a run id or a port number in there says nothing
// about the status. A 501 is stated by the sentinel instead — see failedCall.
func (f *failure) ComposedFailure() {}

// observed is a response together with the call that produced it, so a clause
// that reads the primary response and a clause that makes its own call both
// name the right operation and the right params in field 2 and field 3.
type observed struct {
	op     string
	params string
	body   any
	// ok is false when no call succeeded — the primary call of a test that
	// expects an error. A clause that reads the primary response then has
	// nothing to read, and says so rather than asserting against an empty
	// document.
	ok bool
}

// fail builds a failure for one step of one test.
func (e *execution) fail(obs observed, step, kind, path, expected, actual string) error {
	return &failure{
		group:    e.group.Name,
		test:     e.test,
		op:       obs.op,
		params:   obs.params,
		kind:     kind,
		path:     path,
		expected: expected,
		actual:   actual,
		file:     e.group.File,
		step:     step,
	}
}

// failedCall reports a call that should have succeeded. The SDK's error text
// is quoted verbatim as the actual value, so the reader sees what the SDK said.
//
// Classification is decided here rather than left to the message: this is the
// one place holding the *raw* SDK error, and a composed failure message is not
// something harness.LooksUnimplemented may be pointed at — it embeds the params
// JSON, where a run id or a port puts a "501" that means nothing. So a 501 is
// stated by wrapping harness.ErrUnimplemented, and every other failure carries
// no sentinel and is a plain fail.
func (e *execution) failedCall(obs observed, step string, sdkErr error) error {
	f := e.fail(obs, step, "call", "", "the call to succeed", quote(sdkErr.Error()))
	if isUnimplementedResponse(sdkErr) {
		return unimplementedFailure{f}
	}
	return f
}

// unimplementedFailure is a failure the emulator answered with 501. It reads
// as the failure it wraps and unwraps to both it and the sentinel, so the
// message in the NDJSON `error` field is unchanged and harness.IsUnimplemented
// still classifies the test as unimplemented.
type unimplementedFailure struct{ err error }

func (u unimplementedFailure) Error() string { return u.err.Error() }

func (u unimplementedFailure) Unwrap() []error { return []error{u.err, harness.ErrUnimplemented} }

// quote renders a string as a failure message's expected or actual value. An
// SDK error's text can be multi-line, so it is folded onto one line: the
// NDJSON `error` field is read as a single line by the report tooling. It is
// capped too — a transport failure can carry a long chain of wrapped causes.
func quote(s string) string {
	return fmt.Sprintf("%q", clip(strings.Join(strings.Fields(s), " ")))
}

// maxRendered caps one field of one failure message. Every failure ends up in
// a single-line NDJSON `error` that the dashboard renders and the report
// tooling diffs, so a field running to megabytes costs far more than the
// diagnosis it buys. A few KiB is enough to identify a wrong value and to see
// the start of the list or the message it came from.
const maxRendered = 4096

// clip trims a rendered value to maxRendered bytes and says how much it
// dropped, so the reader knows the value is not all of what was there. The cut
// is moved back to a rune boundary: the result is still read by a human and
// still goes through encoding/json on its way to the NDJSON line.
func clip(s string) string {
	if len(s) <= maxRendered {
		return s
	}
	cut := maxRendered
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s… (%d bytes elided)", s[:cut], len(s)-cut)
}

// renderList prints the list a membership check searched. It is the actual
// value of the failure, so it is printed rather than summarised — a generated
// failure that says only "no match" cannot be diagnosed without re-running —
// but it is capped, for the same reason every other field is.
func renderList(list []any) string {
	if len(list) == 0 {
		return "an empty list"
	}
	return clip(render(list))
}

// renderWhereExpected prints a where list for a failure message, in path order.
func renderWhereExpected(where []WhereEntry, values []any) string {
	parts := make([]string, 0, len(where))
	for i, entry := range where {
		parts = append(parts, fmt.Sprintf("%s=%s", entry.Path, render(values[i])))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// acceptedCodes renders both halves of an error clause for a failure message.
func acceptedCodes(want *ErrorSpec) string {
	if want.Shape == want.Code {
		return fmt.Sprintf("error %q", want.Shape)
	}
	return fmt.Sprintf("error %q or %q", want.Shape, want.Code)
}
