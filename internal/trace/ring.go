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

// newRing creates a ring holding at most capacity slots. A capacity below one
// is raised to one: every caller wants somewhere to put the entry it has, and a
// zero-length backing array would divide by zero on the first push.
func newRing(capacity int) *ring {
	if capacity < 1 {
		capacity = 1
	}
	return &ring{slots: make([]*slot, capacity)}
}

func (r *ring) len() int { return r.n }

func (r *ring) cap() int { return len(r.slots) }

// push stores s as the newest entry and returns the slot it displaced, or nil
// while the ring still has room. The caller owns whatever the eviction means —
// the ring itself knows nothing about indexes or pinning.
func (r *ring) push(s *slot) *slot {
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
