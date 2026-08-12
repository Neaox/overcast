package trace

// slot is one retained trace, held by the rings rather than the Recorder
// itself. The indirection buys two things:
//
//   - Re-registering a request ID replaces the recorder in one place. The rings
//     hold the same *slot, so nothing has to be found and rewritten inside
//     them, and the index and the rings cannot disagree about which Recorder a
//     request ID means.
//   - A later phase pins a failed trace by putting it in a second ring. Two
//     rings referencing one slot is free; two rings holding two copies of a
//     pointer that must stay in step is not.
type slot struct {
	rec *Recorder
	// Ring membership. A slot leaves the live ring when it is evicted or
	// culled, and may survive on the pinned ring — so the index entry goes
	// only when both are false. Tracking it here rather than inferring it is
	// what lets one trace be reachable from two rings without the two
	// disagreeing about whether it still exists.
	inLive   bool
	inPinned bool
	// bytes is this trace's contribution to the buffer's retained total,
	// sampled by Settle once the response is recorded. It is held here rather
	// than recomputed on eviction so that what is subtracted is exactly what
	// was added, however the recorder has changed in between.
	bytes int64
}

// ring is a fixed-capacity, insertion-ordered ring of slots.
//
// Its one eviction rule is "advance the head", which is what makes position
// track age: at(0) is always the oldest survivor and at(len-1) the newest.
// Every ordered operation the buffer needs — newest-first listing, and the age
// cull a later phase adds — follows from that and costs nothing to maintain.
//
// It is deliberately not concurrency-safe. The Buffer holds one lock across all
// of its rings; a per-ring lock would be a second lock ordering to get wrong.
type ring struct {
	slots []*slot
	head  int // index of the oldest occupied slot
	n     int // number of occupied slots
}

// newRing creates a ring holding at most capacity slots. A capacity of zero is
// legal and means "stores nothing" — which is what a disabled population wants,
// rather than a one-slot ring quietly retaining the most recent of them.
func newRing(capacity int) *ring {
	if capacity < 0 {
		capacity = 0
	}
	return &ring{slots: make([]*slot, capacity)}
}

func (r *ring) len() int { return r.n }

func (r *ring) cap() int { return len(r.slots) }

// push stores s as the newest entry and returns the slot it displaced, or nil
// while the ring still has room. The caller owns whatever the eviction means —
// the ring itself knows nothing about indexes or pinning.
func (r *ring) push(s *slot) *slot {
	if len(r.slots) == 0 {
		// Nowhere to put it: the caller's entry is displaced immediately.
		return s
	}
	if r.n < len(r.slots) {
		r.slots[(r.head+r.n)%len(r.slots)] = s
		r.n++
		return nil
	}
	evicted := r.slots[r.head]
	r.slots[r.head] = s
	r.head = (r.head + 1) % len(r.slots)
	return evicted
}

// at returns the i-th slot counting from the oldest. Callers must keep i within
// len(); the buffer only ever walks the range it just read.
func (r *ring) at(i int) *slot {
	return r.slots[(r.head+i)%len(r.slots)]
}

// oldest returns the slot that would be evicted next, or nil when empty.
func (r *ring) oldest() *slot {
	if r.n == 0 {
		return nil
	}
	return r.slots[r.head]
}

// popOldest removes and returns the oldest slot, or nil when empty.
//
// It is the cull's only way of removing anything, and deliberately so: age
// order is positional, so taking from the head is the one removal that leaves
// the ring intact.
func (r *ring) popOldest() *slot {
	if r.n == 0 {
		return nil
	}
	s := r.slots[r.head]
	r.slots[r.head] = nil
	r.head = (r.head + 1) % len(r.slots)
	r.n--
	return s
}

// grow enlarges the ring towards `to`, preserving age order.
//
// A ring starts at its floor and grows only when a burst actually arrives, so
// an emulator nobody is hammering never allocates for a burst that never comes.
// Growth copies in age order and resets the head — the one moment position is
// rebuilt rather than maintained.
func (r *ring) grow(to int) {
	if to <= len(r.slots) {
		return
	}
	next := make([]*slot, to)
	for i := 0; i < r.n; i++ {
		next[i] = r.at(i)
	}
	r.slots, r.head = next, 0
}
