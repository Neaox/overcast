package scenario

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestClipCapsAFieldAndSaysSo. Every failure ends up in one single-line NDJSON
// `error` that the dashboard renders and the report tooling diffs, so an
// uncapped field turns one bad test into a megabyte of transcript.
func TestClipCapsAFieldAndSaysSo(t *testing.T) {
	short := strings.Repeat("a", maxRendered)
	if got := clip(short); got != short {
		t.Errorf("a value inside the cap must be untouched, got %d bytes", len(got))
	}

	long := strings.Repeat("b", maxRendered*3)
	got := clip(long)
	if len(got) > maxRendered+64 {
		t.Errorf("clipped value is %d bytes, want about %d", len(got), maxRendered)
	}
	if !strings.HasPrefix(got, strings.Repeat("b", 64)) {
		t.Error("the start of the value must survive — it is what identifies it")
	}
	if !strings.Contains(got, "bytes elided") {
		t.Errorf("a clipped value must say it was clipped: %q", got[len(got)-40:])
	}
}

// TestClipCutsOnARuneBoundary: the result is read by a human and re-encoded as
// JSON on its way to the NDJSON line, so half a rune is not acceptable.
func TestClipCutsOnARuneBoundary(t *testing.T) {
	// Three-byte runes, so a cut at maxRendered lands mid-rune unless it is
	// moved back.
	got := clip(strings.Repeat("→", maxRendered))
	if !utf8.ValidString(got) {
		t.Errorf("clip produced invalid UTF-8: %q", got[maxRendered-8:maxRendered+8])
	}
}

// TestQuoteFoldsAndCapsTheCLIsOwnText. An `aws` invocation that dies before it
// reaches the wire can put a whole Python traceback on stderr, and quote is
// where that text becomes a failure message's actual value.
func TestQuoteFoldsAndCapsTheCLIsOwnText(t *testing.T) {
	multiline := "aws widgets get-thing: exit status 255:\n  Traceback (most recent call last):\n    " +
		strings.Repeat("File \"/usr/lib/aws/botocore/x.py\", line 1, in y\n      raise\n", 400)
	got := quote(multiline)
	if strings.Contains(got, "\n") {
		t.Error("quote must fold the text onto one line — the NDJSON error field is one line")
	}
	// %q escapes on top of the cap — every quote in a traceback becomes two
	// bytes, and the elision marker's "…" becomes six — so the bound is on the
	// order of the cap, not the cap itself. What it has to catch is the
	// uncapped case, which was the whole traceback.
	if len(got) > maxRendered*2 {
		t.Errorf("quoted value is %d bytes, want on the order of %d", len(got), maxRendered)
	}
	if !strings.Contains(got, "aws widgets get-thing") {
		t.Error("the head of the message must survive")
	}
}

// TestListClauseFailureCapsTheListItPrints is the same cap on the other field
// that can grow without limit: a listContains failure prints the list it
// searched, and a real ListPolicies or ListQueues in a busy account is long.
func TestListClauseFailureCapsTheListItPrints(t *testing.T) {
	items := make([]any, 0, 5000)
	for i := 0; i < 5000; i++ {
		items = append(items, fmt.Sprintf("arn:aws:widgets:us-east-1:123456789012:thing/thing-%04d", i))
	}

	b, fake, rg := fixture(t, scenarioFile(lifecycle("widgets-gen-thing", obj{
		"name": "ListThings", "op": "ListThings",
		"call": obj{"op": "ListThings", "params": obj{}},
		"assert": []any{obj{
			"kind": "listContains", "itemsPath": "$.Things", "where": obj{"$": "absent"},
		}},
	})))
	fake.script["list-things"] = []fakeResult{ok(obj{"Things": items})}

	err := runOneTest(t, b, rg, "ListThings")
	if err == nil {
		t.Fatal("want a failure")
	}
	if len(err.Error()) > maxRendered*2 {
		t.Errorf("failure message is %d bytes; the list it searched must be capped", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "thing-0000") {
		t.Error("the start of the list must survive — it is what shows what shape the items are")
	}
	if !strings.Contains(err.Error(), "bytes elided") {
		t.Error("a capped list must say it was capped")
	}
}

// TestUnimplementedFailureReadsAsTheFailureItWraps: the sentinel must not
// change a single character of what the report shows.
func TestUnimplementedFailureReadsAsTheFailureItWraps(t *testing.T) {
	inner := errors.New("widgets-gen-probe/Probe: Probe params {}: call: expected the call to succeed, actual \"…\" (f.json call)")
	wrapped := unimplementedFailure{inner}
	if wrapped.Error() != inner.Error() {
		t.Errorf("Error() = %q, want the wrapped failure verbatim", wrapped.Error())
	}
	if !errors.Is(wrapped, inner) {
		t.Error("the wrapped failure must still be reachable")
	}
}
