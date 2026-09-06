package scenario

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// This file's counterpart is
// compat/suites/go-sdk/internal/scenario/failure.go: the two build the same
// six-field failure message (below) for their own backend and are not
// byte-identical, but a change to the message shape here usually needs a
// matching change there — change both or neither.

// Debuggability is the interpreter's whole cost, and it is paid here: one
// helper builds every failure message and every assertion uses it, so a
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
// The wording avoids every phrase harness.IsUnimplemented matches on, so the
// interpreter's own prose can never turn an assertion failure into a false
// "unimplemented". The CLI's error text is quoted verbatim where it is the
// actual value, which is what lets a genuine 501 still be classified.
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

// ComposedFailure marks this message as the interpreter's own, so
// harness.IsUnimplemented never runs its "501" substring test over it: field 3
// is the params JSON, and a run id or a port number in there says nothing about
// the status. A 501 is stated by the sentinel instead — see failedCall.
func (f *failure) ComposedFailure() {}

// observed is a response together with the call that produced it, so a clause
// that reads the primary response and a clause that makes its own call both
// name the right operation and the right params in field 2 and field 3.
type observed struct {
	op     string
	params string
	body   map[string]any
	// ok is false when no call succeeded — the primary call of a test that
	// expects an error. A clause that reads the primary response then has
	// nothing to read, and says so rather than asserting against an empty map.
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
		file:     e.file.Path,
		step:     step,
	}
}

// quote renders a string as a failure message's expected or actual value. CLI
// error text is multi-line, so it is folded onto one line: the NDJSON `error`
// field is read as a single line by the report tooling. It is capped too — an
// `aws` invocation that dies before it reaches the wire can put a whole Python
// traceback, or the CLI's usage text, on stderr.
func quote(s string) string {
	return fmt.Sprintf("%q", clip(strings.Join(strings.Fields(s), " ")))
}

// maxRendered caps one field of one failure message. Every failure ends up in a
// single-line NDJSON `error` that the dashboard renders and the report tooling
// diffs, so a field running to megabytes costs far more than the diagnosis it
// buys. A few KiB is enough to identify a wrong value and to see the start of
// the list or the message it came from.
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
