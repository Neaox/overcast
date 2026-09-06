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
	for op, scope := range describeIDScopes {
		note, ok := notes[op]
		if !ok {
			t.Errorf("%s resolves an ID list but declares no capability", op)
			continue
		}
		for _, code := range []string{scope.notFound, scope.malformed} {
			if !strings.Contains(note, code) {
				t.Errorf("%s note does not name %s:\n  %s\n"+
					"add it to capabilities_dev.go and re-run capgen", op, code, note)
			}
		}
	}
}
