package lambda

// store_code_atomicity_test.go — pins the deployment-package commit contract:
// the package bytes and the AWS-observable generation (CodeSha256, RevisionId,
// source metadata) become visible together, or not at all. A failed
// function-record write must leave the previous generation intact and readable,
// including on a later cold start, and must not strand the package it wrote.
//
// Every case runs against both state backends — MemoryStore and, when SQLite is
// compiled in, a real SQLiteStore — because the guarantee lives in lambdaStore's
// write ordering, not in either backend.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

var errRecordWriteFailed = errors.New("state: injected function-record write failure")

// recordWriteFailStore fails the next nsFunctions write, leaving every other
// namespace — the deployment package among them — writing normally. That is
// exactly the partial failure #656 describes: the package commits, the record
// that makes it observable does not.
type recordWriteFailStore struct {
	state.Store
	mu    sync.Mutex
	armed bool
}

func (s *recordWriteFailStore) arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

func (s *recordWriteFailStore) Set(ctx context.Context, namespace, key, value string) error {
	s.mu.Lock()
	fail := s.armed && namespace == nsFunctions
	if fail {
		s.armed = false
	}
	s.mu.Unlock()
	if fail {
		return errRecordWriteFailed
	}
	return s.Store.Set(ctx, namespace, key, value)
}

// newCodeAtomicityStores returns one backing store per state backend this build
// provides. Under -tags nosqlite the map degrades to memory only rather than the
// file being tagged out — see tests/AGENTS.md § Parity tests.
//
// The hybrid backend is here because it is the one whose read semantics depend
// on the namespace's tier: "lambda:function-code" is TierCached, so a package
// is never seeded into memory and every read of one resolves through the
// pending-write overlay and SQLite rather than the in-memory map that serves
// the function record. The guarantee has to hold across that split, not just
// within one uniform backend. Writes are given an hour-long flush interval so
// the packages stay in the overlay for the duration of a test — the flushed
// half of the split is covered by store_code_hybrid_test.go.
func newCodeAtomicityStores(t *testing.T) map[string]state.Store {
	t.Helper()

	stores := map[string]state.Store{"memory": state.NewMemoryStore()}
	if !config.SQLiteSupported() {
		return stores
	}
	sqlStore, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("state.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlStore.Close(); err != nil {
			t.Logf("sqlStore.Close: %v", err)
		}
	})
	stores["sql"] = sqlStore

	hybridStore, err := state.NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("state.NewHybridStore: %v", err)
	}
	t.Cleanup(func() {
		if err := hybridStore.Close(); err != nil {
			t.Logf("hybridStore.Close: %v", err)
		}
	})
	if err := hybridStore.WaitReady(context.Background()); err != nil {
		t.Fatalf("hybrid WaitReady: %v", err)
	}
	stores["hybrid"] = hybridStore
	return stores
}

// atomicityFixture wires a lambdaStore over a fault-injecting wrapper so a test
// can fail the record write of one specific put.
type atomicityFixture struct {
	ls    *lambdaStore
	inner state.Store
	fault *recordWriteFailStore
}

func newAtomicityFixture(t *testing.T, backing state.Store) atomicityFixture {
	t.Helper()
	fault := &recordWriteFailStore{Store: backing}
	return atomicityFixture{ls: newLambdaStore(fault, "us-east-1", clock.New()), inner: backing, fault: fault}
}

// seedFunction creates a coherent first generation through the create path.
func (f atomicityFixture) seedFunction(t *testing.T, zip []byte) {
	t.Helper()
	fn := &Function{
		Name:       "atomic-fn",
		ARN:        "arn:aws:lambda:us-east-1:000000000000:function:atomic-fn",
		State:      "Active",
		RevisionId: "revision-1",
	}
	fn.setCode(zip)
	created, aerr := f.ls.createFunction(context.Background(), fn)
	if aerr != nil || !created {
		t.Fatalf("seed createFunction: created=%v err=%v", created, aerr)
	}
}

// updateCode runs the UpdateFunctionCode shape of mutation: new bytes, new
// RevisionId, new source metadata, all in one commit.
func (f atomicityFixture) updateCode(zip []byte, revision string) *protocol.AWSError {
	_, _, aerr := f.ls.mutateFunction(context.Background(), "atomic-fn", func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(zip)
		current.RevisionId = revision
		current.CodeS3Bucket = "updated-bucket"
		current.CodeS3Key = "updated-key"
		return true, nil
	})
	return aerr
}

// storedPackages returns every persisted package key for the seeded function.
func (f atomicityFixture) storedPackages(t *testing.T) []string {
	t.Helper()
	kvs, err := f.inner.Scan(context.Background(), nsFunctionCode, "")
	if err != nil {
		t.Fatalf("scan packages: %v", err)
	}
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
	}
	return keys
}

// assertGeneration reads the function back through a freshly built store — the
// cold-start path — and asserts the record and the bytes are the same
// generation.
func (f atomicityFixture) assertGeneration(t *testing.T, wantZip []byte, wantRevision string) {
	t.Helper()
	coldStart := newLambdaStore(f.inner, "us-east-1", clock.New())
	fn, aerr := coldStart.getFunction(context.Background(), "atomic-fn")
	if aerr != nil || fn == nil {
		t.Fatalf("getFunction: fn=%v err=%v", fn, aerr)
	}
	if fn.RevisionId != wantRevision {
		t.Fatalf("RevisionId = %q, want %q", fn.RevisionId, wantRevision)
	}
	if fn.CodeHash != codeHashOf(wantZip) || fn.CodeSize != int64(len(wantZip)) {
		t.Fatalf("record generation = hash %q size %d, want hash %q size %d",
			fn.CodeHash, fn.CodeSize, codeHashOf(wantZip), len(wantZip))
	}
	if aerr := coldStart.loadFunctionCode(context.Background(), fn); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}
	if !bytes.Equal(fn.CodeZip, wantZip) {
		t.Fatalf("stored package = %q, want %q — the bytes and the observable generation disagree", fn.CodeZip, wantZip)
	}
}

func TestUpdateFunctionCode_recordWriteFailureKeepsPriorGeneration(t *testing.T) {
	for name, backing := range newCodeAtomicityStores(t) {
		t.Run(name, func(t *testing.T) {
			// Given: a function whose first generation committed coherently.
			f := newAtomicityFixture(t, backing)
			first := []byte("first-generation-package")
			f.seedFunction(t, first)

			// When: the package write succeeds but the function record write fails.
			f.fault.arm()
			aerr := f.updateCode([]byte("second-generation-package"), "revision-2")

			// Then: the caller is told the update failed…
			if aerr == nil {
				t.Fatal("mutateFunction reported success despite a failed function-record write")
			}
			// …and a cold start still sees the previous generation, whole.
			f.assertGeneration(t, first, "revision-1")
			// …with no stranded package left behind by the failed attempt.
			if keys := f.storedPackages(t); len(keys) != 1 {
				t.Fatalf("stored packages = %v, want exactly the referenced generation", keys)
			}
		})
	}
}

func TestUpdateFunctionCode_retryAfterRecordWriteFailure(t *testing.T) {
	for name, backing := range newCodeAtomicityStores(t) {
		t.Run(name, func(t *testing.T) {
			// Given: an update that failed at the function-record write.
			f := newAtomicityFixture(t, backing)
			f.seedFunction(t, []byte("first-generation-package"))
			second := []byte("second-generation-package")
			f.fault.arm()
			if aerr := f.updateCode(second, "revision-2"); aerr == nil {
				t.Fatal("mutateFunction reported success despite a failed function-record write")
			}

			// When: the client retries the same update.
			if aerr := f.updateCode(second, "revision-2"); aerr != nil {
				t.Fatalf("retry: %v", aerr)
			}

			// Then: the retry commits one coherent generation…
			f.assertGeneration(t, second, "revision-2")
			// …and collects the package the failed attempt wrote.
			if keys := f.storedPackages(t); len(keys) != 1 {
				t.Fatalf("stored packages = %v, want exactly the referenced generation", keys)
			}
		})
	}
}

func TestCreateFunction_recordWriteFailureLeavesNothingBehind(t *testing.T) {
	for name, backing := range newCodeAtomicityStores(t) {
		t.Run(name, func(t *testing.T) {
			// Given: an empty store and a create whose record write will fail.
			f := newAtomicityFixture(t, backing)
			fn := &Function{
				Name:       "atomic-fn",
				ARN:        "arn:aws:lambda:us-east-1:000000000000:function:atomic-fn",
				State:      "Active",
				RevisionId: "revision-1",
			}
			fn.setCode([]byte("create-package"))

			// When: CreateFunction fails after the package write.
			f.fault.arm()
			created, aerr := f.ls.createFunction(context.Background(), fn)

			// Then: the create is reported as failed…
			if aerr == nil || created {
				t.Fatalf("createFunction: created=%v err=%v, want a reported failure", created, aerr)
			}
			// …no function exists…
			existing, getErr := f.ls.getFunction(context.Background(), "atomic-fn")
			if getErr != nil {
				t.Fatalf("getFunction: %v", getErr)
			}
			if existing != nil {
				t.Fatal("a failed CreateFunction left a function record behind")
			}
			// …and no package is stranded under the function's name.
			if keys := f.storedPackages(t); len(keys) != 0 {
				t.Fatalf("stored packages = %v, want none after a failed create", keys)
			}

			// And: a retry creates one coherent generation.
			retry := &Function{
				Name:       "atomic-fn",
				ARN:        fn.ARN,
				State:      "Active",
				RevisionId: "revision-1",
			}
			retry.setCode([]byte("create-package"))
			if created, aerr := f.ls.createFunction(context.Background(), retry); aerr != nil || !created {
				t.Fatalf("retry createFunction: created=%v err=%v", created, aerr)
			}
			f.assertGeneration(t, []byte("create-package"), "revision-1")
		})
	}
}

func TestLoadFunctionCode_generationCollectedAfterUpdate(t *testing.T) {
	for name, backing := range newCodeAtomicityStores(t) {
		t.Run(name, func(t *testing.T) {
			// Given: a cold start holding a record snapshot, and a code update
			// that commits — and collects that snapshot's package — before the
			// package is materialized.
			f := newAtomicityFixture(t, backing)
			f.seedFunction(t, []byte("first-generation-package"))
			stale, aerr := f.ls.getFunction(context.Background(), "atomic-fn")
			if aerr != nil || stale == nil {
				t.Fatalf("getFunction: fn=%v err=%v", stale, aerr)
			}
			second := []byte("second-generation-package")
			if aerr := f.updateCode(second, "revision-2"); aerr != nil {
				t.Fatalf("updateCode: %v", aerr)
			}

			// When: the stale snapshot loads its package.
			if aerr := f.ls.loadFunctionCode(context.Background(), stale); aerr != nil {
				t.Fatalf("loadFunctionCode: %v", aerr)
			}

			// Then: it materializes the current generation — bytes and identity
			// together — rather than starting a container with no package.
			if !bytes.Equal(stale.CodeZip, second) {
				t.Fatalf("loaded package = %q, want the current generation %q", stale.CodeZip, second)
			}
			if stale.CodeHash != codeHashOf(second) || stale.CodeSize != int64(len(second)) {
				t.Fatalf("loaded identity = hash %q size %d, want hash %q size %d",
					stale.CodeHash, stale.CodeSize, codeHashOf(second), len(second))
			}
		})
	}
}

func TestLoadFunctionCode_packageStoredBeforeGenerationAddressing(t *testing.T) {
	for name, backing := range newCodeAtomicityStores(t) {
		t.Run(name, func(t *testing.T) {
			// Given: a record and package persisted under the pre-generation
			// key layout, where the package key was the function name alone.
			f := newAtomicityFixture(t, backing)
			legacy := []byte("legacy-generation-package")
			fn := &Function{
				Name:       "atomic-fn",
				ARN:        "arn:aws:lambda:us-east-1:000000000000:function:atomic-fn",
				State:      "Active",
				RevisionId: "revision-1",
			}
			fn.setCode(legacy)
			record := *fn
			record.CodeZip = nil
			raw, err := json.Marshal(&record)
			if err != nil {
				t.Fatalf("marshal legacy record: %v", err)
			}
			key := serviceutil.RegionKey("us-east-1", fn.Name)
			ctx := context.Background()
			if err := backing.Set(ctx, nsFunctions, key, string(raw)); err != nil {
				t.Fatalf("seed legacy record: %v", err)
			}
			if err := backing.Set(ctx, nsFunctionCode, key, base64.StdEncoding.EncodeToString(legacy)); err != nil {
				t.Fatalf("seed legacy package: %v", err)
			}

			// When: the function is read and its package materialized.
			f.assertGeneration(t, legacy, "revision-1")

			// And: the next code update supersedes the legacy key rather than
			// leaving it resident forever.
			second := []byte("second-generation-package")
			if aerr := f.updateCode(second, "revision-2"); aerr != nil {
				t.Fatalf("updateCode: %v", aerr)
			}
			f.assertGeneration(t, second, "revision-2")
			if keys := f.storedPackages(t); len(keys) != 1 {
				t.Fatalf("stored packages = %v, want exactly the referenced generation", keys)
			}
		})
	}
}
