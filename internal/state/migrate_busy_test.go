//go:build !nosqlite

package state

// Migration's first read, when several connections open the same new database
// at once.
//
// A transient SQLITE_BUSY on an ordinary read is absorbed — hybrid.go retries
// the busy/locked class before giving up, and docs/dev/storage-backends.md
// § Storage pressure describes that as the contract. Migration ran outside it:
// `PRAGMA user_version` is the first statement RunMigrations issues, and a lock
// held at that instant failed the whole store with
// `migrate: state: read user_version: database is locked`.
//
// The contention is not a held write transaction — WAL readers do not block on
// a writer, so that never reproduces. It is *opening* a fresh file: switching a
// new database to WAL takes an exclusive lock, so connections racing to
// initialise the same path collide. Eleven of twelve concurrent opens failed
// this way before the retry, which is why it surfaced as an intermittent CI
// failure in tests/integration/lambda rather than a permanent one.

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

// openRacingSQLite opens path with the DSN sqlite.go uses, including the WAL
// pragma whose exclusive lock is what the connections contend over.
func openRacingSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=synchronous(normal)")
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestReadUserVersion_survivesConcurrentFreshOpens is the regression. Without
// the retry this fails most of the time rather than always, which is exactly
// what made it expensive: a red CI run on an unrelated branch, green on re-run.
func TestReadUserVersion_survivesConcurrentFreshOpens(t *testing.T) {
	// Given: one new database path
	path := t.TempDir() + "/state.db"

	// When: twelve connections initialise and read it at once
	const conns = 12
	var wg sync.WaitGroup
	errs := make(chan error, conns)
	for range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := readUserVersion(context.Background(), openRacingSQLite(t, path)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	// Then: every one of them read the version
	var failed int
	for err := range errs {
		if failed == 0 {
			t.Errorf("readUserVersion failed under concurrent opens: %v — this is the "+
				"lock every other read in the package waits through", err)
		}
		failed++
	}
	if failed > 0 {
		t.Errorf("%d of %d concurrent opens failed", failed, conns)
	}
}

// TestReadUserVersion_stopsWhenTheContextEnds: the wait is bounded by the
// caller's context as well as by its own budget, so a shutdown during startup
// does not sit out the full window.
func TestReadUserVersion_stopsWhenTheContextEnds(t *testing.T) {
	// Given: a database and a context that is already done
	db := openRacingSQLite(t, t.TempDir()+"/state.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When: the version is read through it
	start := time.Now()
	_, _ = readUserVersion(ctx, db)

	// Then: it returned promptly rather than retrying to the budget
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v with a cancelled context, want prompt — the retry must not "+
			"outlive the caller", elapsed)
	}
}

// TestReadUserVersion_returnsNonLockErrorsImmediately: the retry is for the
// busy class only. A closed database is not going to start working, and
// spending the budget on it would turn a clear failure into a slow one.
func TestReadUserVersion_returnsNonLockErrorsImmediately(t *testing.T) {
	// Given: a database that is closed
	db, err := sql.Open("sqlite", t.TempDir()+"/state.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Close()

	// When: the runner reads through it
	start := time.Now()
	if _, err := readUserVersion(context.Background(), db); err == nil {
		t.Fatal("readUserVersion succeeded against a closed database")
	}

	// Then: it failed at once
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to report a non-transient error, want immediate", elapsed)
	}
}
