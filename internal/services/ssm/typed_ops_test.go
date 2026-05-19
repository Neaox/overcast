package ssm

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	h := &Handler{}
	h.initOps()

	if len(h.typedOp) != len(h.ops) {
		t.Fatalf("typed op count = %d, want legacy count %d", len(h.typedOp), len(h.ops))
	}
	for name := range h.ops {
		operation, ok := h.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
		if _, raw := operation.(*op.Raw); raw {
			t.Fatalf("typed operation %s still uses raw adapter", name)
		}
	}
}
