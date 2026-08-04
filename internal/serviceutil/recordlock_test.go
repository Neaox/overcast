package serviceutil

import (
	"sync"
	"testing"
	"time"
)

// A record's writers are only serialised if the lock covers the read and the
// write together. This is the property every caller depends on: the store is
// safe for concurrent access on its own, and still loses edits without it.
func TestRecordLocks_serialisesReadModifyWrite(t *testing.T) {
	// Given: a "record" that every writer reads, edits and writes back, with a
	// gap in between wide enough for another writer to fit through
	var locks RecordLocks // zero value, as a handler built without a constructor has
	var record int
	const writers = 64

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer locks.Lock("some/record")()

			got := record
			time.Sleep(time.Microsecond) // the dial, the Docker call, the edit
			record = got + 1
		}()
	}
	wg.Wait()

	// Then: not one edit was lost
	if record != writers {
		t.Errorf("record = %d after %d writers, want %d — %d edits were overwritten",
			record, writers, writers, writers-record)
	}
}

// Records are independent: one writer holding its own record must not stop a
// writer of a different one. Striping means two keys can share a mutex, so this
// asserts the far weaker thing that actually matters — that a second key can be
// taken and released while the first is held, for at least some pair of keys.
func TestRecordLocks_differentRecordsDoNotBlockEachOther(t *testing.T) {
	var locks RecordLocks
	release := locks.Lock("record/one")
	defer release()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Try enough keys that at least one lands on a different stripe.
		for _, key := range []string{"record/two", "record/three", "record/four"} {
			locks.Lock(key)()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("locking other records blocked on a lock held for an unrelated one")
	}
}
