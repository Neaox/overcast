//go:build dev

package ec2

// describe_ids_dev_test.go — the capability notes are held to what
// describe_ids.go actually raises, the same way TestCapabilityNotesNameTheFiltersOnly
// holds them to the filters an operation implements.

import (
	"strings"
	"testing"
)

// A note that says nothing about the two codes is a promise about the
// interesting half of the operation left unwritten: "does this resource still
// exist" is asked with a Describe naming the ID, and the answer arrives as the
// exception. A reader deciding whether Overcast is close enough to AWS for
// their refresh loop needs to see the codes.
func TestCapabilityNotesNameTheIDErrorCodes(t *testing.T) {
	notes := map[string]string{}
	for _, c := range ec2Capabilities(t) {
		notes[c.Operation] = c.Notes
	}
	for op, scopes := range describeIDScopes {
		note, ok := notes[op]
		if !ok {
			t.Errorf("%s resolves an ID list but declares no capability", op)
			continue
		}
		for _, scope := range scopes {
			for _, code := range []string{scope.notFound, scope.malformed} {
				// A scope with no Malformed code has nothing to promise: the
				// EC2 reference documents none for that resource, and
				// describe_ids.go answers NotFound instead.
				if code == "" {
					continue
				}
				if !strings.Contains(note, code) {
					t.Errorf("%s note does not name %s:\n  %s\n"+
						"add it to capabilities_dev.go and re-run capgen", op, code, note)
				}
			}
		}
	}
}

// ReleaseAddress is not a Describe*, so it has no scope above — but it is the
// other half of #1708 and the code it answers is just as easy to normalise
// back to the one AWS documents rather than the one it sends.
func TestCapabilityNotes_releaseAddressNamesTheAllocationCode(t *testing.T) {
	for _, c := range ec2Capabilities(t) {
		if c.Operation != "ReleaseAddress" {
			continue
		}
		if !strings.Contains(c.Notes, "InvalidAllocationID.NotFound") {
			t.Errorf("ReleaseAddress note does not name InvalidAllocationID.NotFound:\n  %s", c.Notes)
		}
		return
	}
	t.Error("ec2 declares no ReleaseAddress capability")
}
