package logs

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

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
