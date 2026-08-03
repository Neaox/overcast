package stepfunctions

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

// TestTypedOps_matchLegacyOperationRegistry checks the two dispatch tables
// describe the same operation set, so CBOR and JSON callers always reach the
// same behaviour — and so cmd/capgen, which reads the legacy map, sees every
// operation the service serves. Execution-plane operations satisfy this through
// typedJSONHandler rather than a second implementation.
func TestTypedOps_matchLegacyOperationRegistry(t *testing.T) {
	h := &Handler{}
	h.initOps()

	if len(h.typedOp) != len(h.ops) {
		t.Fatalf("typed ops len = %d, legacy ops len = %d", len(h.typedOp), len(h.ops))
	}
	for name := range h.ops {
		operation, ok := h.typedOp[name]
		if !ok {
			t.Fatalf("missing typed op %q", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed op %q has Name() %q", name, operation.Name())
		}
	}

	for name, operation := range h.typedOp {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}
