package trace

import "time"

// dropReason says which retention rule reclaimed a trace. It exists so the UI
// can answer "where did it go?" with something better than silence: "we ran out
// of room" and "you left it too long" are different answers, and only one of
// them means raising a limit would have helped.
type dropReason int

const (
	dropCapacity  dropReason = iota // the live ring was full
	dropAged                        // older than the retention window
	dropBytes                       // over the byte budget
	dropPinnedCap                   // a pinned failure displaced by newer ones
	dropInternal                    // internal polling recycling itself
)

// RetentionStats is what the buffer is holding and what it has let go.
//
// It is served by the trace count endpoint and rendered at the end of the trace
// list, because a list that simply stops is indistinguishable from a bug: the
// reader cannot tell whether their request was never traced, or traced and
// reclaimed, or is still there and merely filtered out.
type RetentionStats struct {
	// Occupancy, per ring.
	Live     int `json:"live"`
	Pinned   int `json:"pinned"`
	Internal int `json:"internal"`

	// Count and Capacity keep their existing meanings: everything retained,
	// and the most that could be.
	Count    int `json:"count"`
	Capacity int `json:"capacity"`

	Floor       int           `json:"floor"`
	Ceiling     int           `json:"ceiling"`
	Window      time.Duration `json:"window"`
	PinnedLimit int           `json:"pinnedLimit"`

	Bytes       int64 `json:"bytes"`
	BytesBudget int64 `json:"bytesBudget"`

	// OldestRetained is how far back what you are looking at goes. Zero when
	// nothing is retained.
	OldestRetained time.Time `json:"oldestRetained,omitempty"`

	Dropped DroppedCounts `json:"dropped"`
}

// DroppedCounts is why traces are no longer here, split by the rule that
// reclaimed them. Internal polling is counted separately because it recycles
// itself constantly and would otherwise swamp the numbers a reader cares about.
type DroppedCounts struct {
	Capacity  uint64 `json:"capacity"`
	Aged      uint64 `json:"aged"`
	Bytes     uint64 `json:"bytes"`
	PinnedCap uint64 `json:"pinnedCap"`
	Internal  uint64 `json:"internal"`
}

// Stats reports current occupancy and the running drop counts.
func (b *Buffer) Stats() RetentionStats {
	if b == nil {
		return RetentionStats{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	s := RetentionStats{
		Live:        b.live.len(),
		Pinned:      b.pinned.len(),
		Internal:    b.internal.len(),
		Count:       len(b.index),
		Capacity:    b.live.cap() + b.pinned.cap() + b.internal.cap(),
		Floor:       b.policy.Floor,
		Ceiling:     b.policy.Ceiling,
		Window:      b.policy.Window,
		PinnedLimit: b.policy.Pinned,
		Bytes:       b.bytes,
		BytesBudget: b.policy.Bytes,
		Dropped:     b.dropped,
	}
	// The oldest thing retained is the head of whichever ring reaches furthest
	// back — usually pinned, since pinning is what outlives the window.
	for _, r := range [...]*ring{b.live, b.pinned, b.internal} {
		sl := r.oldest()
		if sl == nil || sl.rec == nil {
			continue
		}
		if s.OldestRetained.IsZero() || sl.rec.timestamp.Before(s.OldestRetained) {
			s.OldestRetained = sl.rec.timestamp
		}
	}
	return s
}

// countDrop records a reclaimed trace. Callers must hold b.mu.
func (b *Buffer) countDrop(reason dropReason) {
	switch reason {
	case dropCapacity:
		b.dropped.Capacity++
	case dropAged:
		b.dropped.Aged++
	case dropBytes:
		b.dropped.Bytes++
	case dropPinnedCap:
		b.dropped.PinnedCap++
	case dropInternal:
		b.dropped.Internal++
	}
}
