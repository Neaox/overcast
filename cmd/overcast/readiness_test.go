package main

import (
	"bytes"
	"net"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// moduleWaitPatterns are the log-line wait strategies of the three official
// LocalStack Testcontainers modules that block on a log line instead of an
// HTTP probe, transcribed from their source (#1546).
//
// Java is the strict one and the reason the marker is a bare line: it applies
// Pattern.matches to a whole log frame with (?s) DOTALL, so the frame has to
// END with "Ready.\n". Go's regexp has no free-standing (?s).*X\z form that
// reads the same, so it is spelled out as an anchored pattern here.
var moduleWaitPatterns = []struct {
	module  string
	source  string
	pattern *regexp.Regexp
}{
	{
		module:  "java org.testcontainers:localstack",
		source:  `Wait.forLogMessage(".*Ready\\.\n", 1), matched with Pattern.matches("(?s).*Ready\\.\n") against one frame`,
		pattern: regexp.MustCompile(`(?s)\A.*Ready\.\n\z`),
	},
	{
		module:  "python testcontainers.community.localstack",
		source:  `wait_for_logs(self, r"Ready\.\n") -> re.compile(pattern, re.MULTILINE).search`,
		pattern: regexp.MustCompile(`(?m)Ready\.\n`),
	},
	{
		module:  "node @testcontainers/localstack",
		source:  `Wait.forLogMessage("Ready", 1) -> substring match per line`,
		pattern: regexp.MustCompile(`Ready`),
	},
}

// TestEmitReady_marksReadinessOnceForEveryModule pins the contract #1546 rests
// on: one marker line, on the stream the modules read, matching each module's
// own wait pattern, written only once every listener handed in is bound.
func TestEmitReady_marksReadinessOnceForEveryModule(t *testing.T) {
	// Given: two bound listeners, as OVERCAST_LISTEN naming two addresses
	// produces
	lns := make([]net.Listener, 0, 2)
	for range 2 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { ln.Close() }) //nolint:errcheck
		lns = append(lns, ln)
	}

	// When: readiness is announced
	core, logs := observer.New(zapcore.InfoLevel)
	var out bytes.Buffer
	emitReady(zap.New(core), &out, lns)

	// Then: exactly one marker line, and nothing else on that stream — a
	// second "Ready." would satisfy a `times: 1` wait early on a restart, and
	// anything else on the stream is a frame the Java matcher would reject.
	got := out.String()
	if n := strings.Count(got, readyMarker); n != 1 {
		t.Fatalf("emitReady wrote %d %q markers, want exactly 1; output: %q", n, readyMarker, got)
	}
	if got != readyMarker+"\n" {
		t.Fatalf("emitReady wrote %q, want %q — the marker must be a line of its own", got, readyMarker+"\n")
	}

	// Then: every module's own wait pattern matches that line.
	for _, m := range moduleWaitPatterns {
		if !m.pattern.MatchString(got) {
			t.Errorf("%s would not start: %s does not match %q", m.module, m.source, got)
		}
	}

	// Then: the marker is preceded by a structured line naming the addresses
	// that are bound, so a reader who greps "Ready." finds out what it means
	// and which sockets it is claiming.
	entries := logs.FilterMessage("overcast ready").All()
	if len(entries) != 1 {
		t.Fatalf("logged %d \"overcast ready\" entries, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["marker"] != readyMarker {
		t.Errorf("marker field = %v, want %q", fields["marker"], readyMarker)
	}
	// ContextMap flattens through zapcore's reflected encoder, so a
	// zap.Strings field comes back as []any.
	addrs, ok := fields["addrs"].([]any)
	if !ok || len(addrs) != len(lns) {
		t.Fatalf("addrs field = %#v, want the %d bound addresses", fields["addrs"], len(lns))
	}
	for i, ln := range lns {
		addr, _ := addrs[i].(string)
		if addr != ln.Addr().String() {
			t.Errorf("addrs[%d] = %q, want the bound address %q", i, addrs[i], ln.Addr())
		}
		// Bound, not merely constructed: the address is dialable now.
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Errorf("addrs[%d] = %q was announced ready but is not accepting: %v", i, addrs[i], err)
			continue
		}
		conn.Close() //nolint:errcheck
	}
}

// TestEmitReady_markerIsNotSatisfiedByStructuredLogging is the negative half:
// it records why the marker is a plain write rather than a zap message. A JSON
// line carrying the same text ends in "}" and matches neither Java's nor
// Python's pattern, so folding it into the structured log would look correct
// and time every one of those suites out.
func TestEmitReady_markerIsNotSatisfiedByStructuredLogging(t *testing.T) {
	jsonLine := `{"level":"info","ts":1788314614.6483681,"caller":"overcast/cmd_serve.go:396","msg":"Ready."}` + "\n"

	for _, m := range moduleWaitPatterns {
		if m.module == "node @testcontainers/localstack" {
			// Node's is a bare substring, so it alone would have been
			// satisfied. Pinned so a future reader does not "simplify" the
			// marker away on the strength of Node passing.
			if !m.pattern.MatchString(jsonLine) {
				t.Errorf("%s: expected the substring match to be satisfied by %q", m.module, jsonLine)
			}
			continue
		}
		if m.pattern.MatchString(jsonLine) {
			t.Errorf("%s: %q unexpectedly matched — the plain marker may no longer be necessary, re-check the module source", m.module, jsonLine)
		}
	}
}
