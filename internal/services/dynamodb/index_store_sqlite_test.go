//go:build !nosqlite

package dynamodb

// The sqlItemBackend-only half of index_store_test.go, split out because it
// asserts on *sql.Tx atomicity — a guarantee that only exists in a build with
// SQLite compiled in. Under -tags nosqlite, state.NewSQLiteStore is a stub
// that always errors (internal/state/sqlite_hybrid_nosqlite.go) and
// newTestItemBackends has no "sql" entry at all. The parity tests in
// index_store_test.go and item_store_test.go stay untagged and degrade to
// memory-only instead — see newTestItemBackends and tests/AGENTS.md
// § Build-tag-sensitive tests.

import (
	"context"
	"testing"
)

// TestSQLItemBackend_PutWithIndexMutations_TxRollsBackOnFailure is
// dynamodb-gsi-design.md section 7's risk-flagged focused test: a failure
// partway through applying a write's index mutations must leave neither the
// base item nor any of that write's index rows committed — the *sql.Tx
// this phase adds to sqlItemBackend (previously bare, untransacted
// ExecContext calls) is what makes this true instead of leaving a
// partially-applied write behind.
//
// The failure is induced deterministically: the second index mutation's
// projected item contains a value json.Marshal cannot encode (a channel),
// so applyIndexMutationsTx fails after the first mutation has already been
// executed (but not committed) inside the same transaction as the base item
// write.
func TestSQLItemBackend_PutWithIndexMutations_TxRollsBackOnFailure(t *testing.T) {
	backend, ok := newTestItemBackends(t)["sql"].(*sqlItemBackend)
	if !ok {
		t.Fatal("expected the \"sql\" backend from newTestItemBackends to be a *sqlItemBackend")
	}
	ctx := context.Background()

	mutations := []indexMutation{
		{
			indexName: "gsi-ok", op: indexMutationUpsert,
			indexHash: "h1", baseHash: "b1",
			item: Item{"a": attrValue{"S": "ok"}},
		},
		{
			indexName: "gsi-bad", op: indexMutationUpsert,
			indexHash: "h1", baseHash: "b1",
			item: Item{"bad": attrValue{"X": make(chan int)}}, // json.Marshal fails on this
		},
	}

	err := backend.putWithIndexMutations(ctx, "T", "b1", "", Item{"pk": attrValue{"S": "b1"}}, mutations)
	if err == nil {
		t.Fatal("expected an error from the unmarshalable second index mutation")
	}

	// Then: the base item write in the same transaction must not have
	// committed either — this is the atomicity guarantee, not just "the
	// bad mutation didn't apply."
	if _, found, getErr := backend.get(ctx, "T", "b1", ""); getErr != nil {
		t.Fatalf("get after rollback: %v", getErr)
	} else if found {
		t.Fatal("base item must not be committed when the transaction rolled back")
	}

	// And: the FIRST (individually valid) index mutation must not have
	// committed either — proving this is a real transaction, not
	// statement-by-statement auto-commit.
	n, err := backend.countIndexEntries(ctx, "T", "gsi-ok")
	if err != nil {
		t.Fatalf("countIndexEntries after rollback: %v", err)
	}
	if n != 0 {
		t.Fatalf("gsi-ok index entry must not be committed when the transaction rolled back, got %d rows", n)
	}
}
