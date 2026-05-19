package dynamodb

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

func TestRawOps_matchLegacyOperationRegistry(t *testing.T) {
	// Given: a DynamoDB handler with the legacy operation registry populated.
	h := &Handler{}
	h.initOps()

	// When: the raw operation manifest is built.
	ops := h.rawOps()

	// Then: every legacy operation is represented exactly once.
	if len(ops) != len(h.ops) {
		t.Fatalf("raw ops len = %d, legacy ops len = %d", len(ops), len(h.ops))
	}
	for name := range h.ops {
		op, ok := ops[name]
		if !ok {
			t.Fatalf("missing raw op %q", name)
		}
		if op.Name() != name {
			t.Fatalf("raw op %q has Name() %q", name, op.Name())
		}
	}
}

func TestTypedOps_migratesListTablesAndKeepsRawFallbacks(t *testing.T) {
	// Given: a DynamoDB handler with the legacy operation registry populated.
	h := &Handler{}
	h.initOps()

	// When: the typed operation manifest is built.
	ops := h.typedOps()

	// Then: every legacy operation is still represented.
	if len(ops) != len(h.ops) {
		t.Fatalf("typed ops len = %d, legacy ops len = %d", len(ops), len(h.ops))
	}
	for name := range h.ops {
		if _, ok := ops[name]; !ok {
			t.Fatalf("missing typed op %q", name)
		}
	}

	// And: all registered operations are real typed operations, not raw wrappers.
	for name, operation := range ops {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}
