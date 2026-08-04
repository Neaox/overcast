package serviceutil

// recordlock.go — serialising the read-modify-write of one stored record.

import "sync"

// recordLockStripes is how many mutexes a RecordLocks spreads records over.
const recordLockStripes = 64

// RecordLocks serialises the read-modify-write of individual stored records.
//
// A record is stored as one blob, so every writer reads the whole record, edits
// it and writes the whole record back: two of them overlapping means the second
// silently discards the first's edit. In an emulator that is not hypothetical —
// every service with a background lifecycle transition has a second writer
// running against the same records its API handlers write, and neither knows
// about the other. What gets discarded is whichever edit landed first: a
// property change applied while a health check was dialling, or the status
// write that a modify then rolled back.
//
// Holding one of these across the read and the write is what makes them one
// step. It has to cover both: releasing between them is the race.
//
// Striped rather than a mutex per record, because record identity churns — a
// long-lived process replaces its instances, clusters and tasks many times over
// — so a map keyed by record would grow for the life of the process. Two
// records sharing a stripe serialise needlessly, which costs nothing here: a
// critical section is one read and one write of a single record, with anything
// slow (a Docker call, a dial) deliberately left outside it.
//
// The zero value is ready to use, so a handler assembled without a constructor,
// as several tests do, is safe to lock.
type RecordLocks struct {
	stripes [recordLockStripes]sync.Mutex
}

// Lock takes the lock covering key and returns the function that releases it,
// for use as:
//
//	defer h.instanceLocks.Lock(id)()
//
// Callers that key records by region must include the region in key: the same
// name in two regions is two records.
func (l *RecordLocks) Lock(key string) func() {
	mu := &l.stripes[recordLockStripe(key)]
	mu.Lock()
	return mu.Unlock
}

// recordLockStripe picks a key's stripe by FNV-1a. Hand-rolled rather than
// seeded (hash/maphash) so a zero-value RecordLocks needs no initialisation.
func recordLockStripe(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	sum := uint32(offset32)
	for i := 0; i < len(key); i++ {
		sum ^= uint32(key[i])
		sum *= prime32
	}
	return sum % recordLockStripes
}
