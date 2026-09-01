//go:build !nosqlite

package lambda

// store_code_hybrid_test.go — the deployment-package commit contract across the
// hybrid backend's flush boundary.
//
// "lambda:function-code" is TierCached (internal/state/tier.go), so a package is
// never seeded into memory: a read of one resolves from the pending-write
// overlay if the write has not been flushed yet, and from SQLite once it has,
// while the function record beside it is TierHot and served from memory
// throughout. Every guarantee store_code_atomicity_test.go pins therefore spans
// two sources that move independently, and the interesting states are the ones
// where a generation has crossed the boundary and its successor has not.
//
// store_code_atomicity_test.go runs the whole suite against a hybrid store whose
// writes all sit in the overlay. This file covers the other side: a flushed
// generation being superseded, collected, and then resolved by a genuine cold
// start against the same data directory.
//
// SQLite-only by construction — there is no memory half to preserve, so the file
// carries the build tag rather than degrading a backend map (tests/AGENTS.md
// § Build-tag-sensitive tests).

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const hybridCodeFunctionARN = "arn:aws:lambda:us-east-1:000000000000:function:atomic-fn"

// openHybridBacking opens a hybrid store over dir with a flush interval long
// enough that nothing is written to SQLite except when a test asks for it, so
// each test controls exactly which generation has crossed the flush boundary.
func openHybridBacking(t *testing.T, dir string) *state.HybridStore {
	t.Helper()
	s, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("state.NewHybridStore: %v", err)
	}
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("hybrid WaitReady: %v", err)
	}
	return s
}

// seedHybridFunction creates a first coherent generation through the create path.
func seedHybridFunction(t *testing.T, ls *lambdaStore, zip []byte) {
	t.Helper()
	fn := &Function{
		Name:       "atomic-fn",
		ARN:        hybridCodeFunctionARN,
		State:      "Active",
		RevisionId: "revision-1",
	}
	fn.setCode(zip)
	created, aerr := ls.createFunction(context.Background(), fn)
	if aerr != nil || !created {
		t.Fatalf("seed createFunction: created=%v err=%v", created, aerr)
	}
}

// updateHybridCode runs the UpdateFunctionCode shape of mutation.
func updateHybridCode(ls *lambdaStore, zip []byte, revision string) *protocol.AWSError {
	_, _, aerr := ls.mutateFunction(context.Background(), "atomic-fn", func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(zip)
		current.RevisionId = revision
		return true, nil
	})
	return aerr
}

// hybridPackageKeys lists every package key the store currently reports for the
// seeded function, as pruneFunctionPackages sees them.
func hybridPackageKeys(t *testing.T, store state.Store) []string {
	t.Helper()
	kvs, err := store.Scan(context.Background(), nsFunctionCode, "")
	if err != nil {
		t.Fatalf("scan packages: %v", err)
	}
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
	}
	return keys
}

func hybridPackageKey(zip []byte) string {
	return serviceutil.RegionKey("us-east-1", functionCodeKey("atomic-fn", codeHashOf(zip)))
}

func TestUpdateFunctionCode_hybridCollectsFlushedGenerationAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Given: a function whose first generation has been flushed, so the package
	// pruneFunctionPackages must find is behind the SQLite read path rather
	// than in the pending overlay.
	backing := openHybridBacking(t, dir)
	closed := false
	defer func() {
		if !closed {
			backing.Close()
		}
	}()
	ls := newLambdaStore(backing, "us-east-1", clock.New())
	first := []byte("first-generation-package")
	seedHybridFunction(t, ls, first)
	if err := backing.Flush(ctx); err != nil {
		t.Fatalf("flush first generation: %v", err)
	}
	if keys := hybridPackageKeys(t, backing); len(keys) != 1 || keys[0] != hybridPackageKey(first) {
		t.Fatalf("stored packages after the first generation = %v, want %v", keys, []string{hybridPackageKey(first)})
	}

	// When: a second generation commits while its own writes are still only in
	// the overlay.
	second := []byte("second-generation-package")
	if aerr := updateHybridCode(ls, second, "revision-2"); aerr != nil {
		t.Fatalf("updateCode: %v", aerr)
	}

	// Then: the Scan behind the collection saw both sources — the flushed
	// generation in SQLite and the unflushed one in the overlay — and left
	// exactly the generation the committed record names.
	if keys := hybridPackageKeys(t, backing); len(keys) != 1 || keys[0] != hybridPackageKey(second) {
		t.Fatalf("stored packages after the update = %v, want %v", keys, []string{hybridPackageKey(second)})
	}

	// And: after the collection is flushed and the process restarts, a cold
	// start against the same data directory resolves the current generation —
	// bytes and identity together.
	if err := backing.Flush(ctx); err != nil {
		t.Fatalf("flush second generation: %v", err)
	}
	if err := backing.Close(); err != nil {
		t.Fatalf("close backing: %v", err)
	}
	closed = true

	reopened := openHybridBacking(t, dir)
	defer reopened.Close()
	if keys := hybridPackageKeys(t, reopened); len(keys) != 1 || keys[0] != hybridPackageKey(second) {
		t.Fatalf("stored packages after restart = %v, want %v", keys, []string{hybridPackageKey(second)})
	}
	coldStart := newLambdaStore(reopened, "us-east-1", clock.New())
	fn, aerr := coldStart.getFunction(ctx, "atomic-fn")
	if aerr != nil || fn == nil {
		t.Fatalf("getFunction after restart: fn=%v err=%v", fn, aerr)
	}
	if fn.RevisionId != "revision-2" || fn.CodeHash != codeHashOf(second) {
		t.Fatalf("record after restart = revision %q hash %q, want revision-2 / %q", fn.RevisionId, fn.CodeHash, codeHashOf(second))
	}
	if aerr := coldStart.loadFunctionCode(ctx, fn); aerr != nil {
		t.Fatalf("loadFunctionCode after restart: %v", aerr)
	}
	if !bytes.Equal(fn.CodeZip, second) {
		t.Fatalf("package after restart = %q, want %q", fn.CodeZip, second)
	}
}

func TestUpdateFunctionCode_hybridRecordWriteFailureKeepsFlushedGeneration(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Given: a first generation that has been flushed to SQLite.
	backing := openHybridBacking(t, dir)
	defer backing.Close()
	fault := &recordWriteFailStore{Store: backing}
	ls := newLambdaStore(fault, "us-east-1", clock.New())
	first := []byte("first-generation-package")
	seedHybridFunction(t, ls, first)
	if err := backing.Flush(ctx); err != nil {
		t.Fatalf("flush first generation: %v", err)
	}

	// When: the package write of the next generation succeeds but the function
	// record write fails.
	fault.arm()
	if aerr := updateHybridCode(ls, []byte("second-generation-package"), "revision-2"); aerr == nil {
		t.Fatal("mutateFunction reported success despite a failed function-record write")
	}

	// Then: the flushed generation is still whole, and a reader that holds no
	// state of its own materializes it…
	reader := newLambdaStore(backing, "us-east-1", clock.New())
	fn, aerr := reader.getFunction(ctx, "atomic-fn")
	if aerr != nil || fn == nil {
		t.Fatalf("getFunction: fn=%v err=%v", fn, aerr)
	}
	if fn.RevisionId != "revision-1" || fn.CodeHash != codeHashOf(first) {
		t.Fatalf("record = revision %q hash %q, want revision-1 / %q", fn.RevisionId, fn.CodeHash, codeHashOf(first))
	}
	if aerr := reader.loadFunctionCode(ctx, fn); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}
	if !bytes.Equal(fn.CodeZip, first) {
		t.Fatalf("package = %q, want the previous generation %q", fn.CodeZip, first)
	}

	// …and the package the failed attempt wrote into the overlay was taken
	// back out, so nothing unreferenced survives to be flushed.
	if keys := hybridPackageKeys(t, backing); len(keys) != 1 || keys[0] != hybridPackageKey(first) {
		t.Fatalf("stored packages = %v, want only the referenced generation %v", keys, []string{hybridPackageKey(first)})
	}
}
