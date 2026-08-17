//go:build dev

package ec2

// filters_dev_test.go — the capability declarations and the filter declarations
// are two statements of the same fact, and this is what keeps them one.
//
// The declarations live behind the `dev` tag, so this test does too. Run it
// with `go test -tags dev ./internal/services/ec2/...`; CI's
// `Test suite (-tags slim,dev)` job is where it runs otherwise.

import (
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/capabilities"
)

// noFilterParameter names the EC2 describes AWS gives no Filters parameter at
// all, and which therefore declare no filterSpec. Anything else that describes
// resources has one, or the test below says so.
var noFilterParameter = map[string]bool{
	"DescribeAccountAttributes": true,
	"DescribeVpcAttribute":      true,
}

// filterNote is the sentence an operation's capability note must end with: the
// filters it actually implements, from the same declaration its handler filters
// with.
//
// capgen reads the capability declarations out of the AST, so a note has to be
// a string literal and cannot call this. TestCapabilityNotesNameTheFiltersOnly
// closes the gap the other way, holding each literal to what its spec declares
// and printing the note to paste when a filter is added.
func filterNote(op string) string {
	return "Filters: " + filterList(supportedFilters[op])
}

func ec2Capabilities(t *testing.T) []capabilities.Capability {
	t.Helper()
	caps := capabilities.Default.ForService("ec2")
	if len(caps) == 0 {
		t.Fatal("no ec2 capabilities declared")
	}
	return caps
}

// A note that advertises a filter the handler does not apply is the same
// promise the emulator broke in #1032, made in documentation instead of code.
// The note is held to the declaration, not to the reviewer's memory.
func TestCapabilityNotesNameTheFiltersOnly(t *testing.T) {
	for _, c := range ec2Capabilities(t) {
		names, declared := supportedFilters[c.Operation]
		if !declared {
			continue
		}
		want := filterNote(c.Operation)
		if !strings.HasSuffix(c.Notes, want) {
			t.Errorf("%s note is %q\nwant it to end with %q (it implements %v)",
				c.Operation, c.Notes, want, names)
		}
	}
}

// The rule is only "every describe" while every describe declares a set. A new
// one that forgets is the old bug returning under a new operation name, so it
// fails here rather than in a user's find-or-create script.
func TestEveryDescribeDeclaresItsFilters(t *testing.T) {
	for _, c := range ec2Capabilities(t) {
		if !strings.HasPrefix(c.Operation, "Describe") || c.Status != capabilities.StatusSupported {
			continue
		}
		if noFilterParameter[c.Operation] {
			if _, declared := supportedFilters[c.Operation]; declared {
				t.Errorf("%s declares a filterSpec but is listed as taking no Filters parameter", c.Operation)
			}
			continue
		}
		if _, declared := supportedFilters[c.Operation]; !declared {
			t.Errorf("%s declares no filterSpec, so it cannot refuse a filter it does not implement; "+
				"declare one in filters.go, or add it to noFilterParameter if AWS gives it no Filters parameter",
				c.Operation)
		}
	}
}
