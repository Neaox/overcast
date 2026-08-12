package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func testSlot(id string) *slot {
	return &slot{rec: NewRecorder(id, time.Unix(0, 0), http.MethodGet, "/", "h", "", http.Header{})}
}

func TestRing_pushFillsThenEvictsOldest(t *testing.T) {
	// Given: a ring with room for three
	r := newRing(3)

	// When: it is filled exactly
	for i := 0; i < 3; i++ {
		if evicted := r.push(testSlot("s-" + strconv.Itoa(i))); evicted != nil {
			t.Fatalf("push %d evicted %q while the ring had room", i, evicted.rec.requestID)
		}
	}
	if r.len() != 3 {
		t.Fatalf("len = %d, want 3", r.len())
	}

	// Then: the next push reports the oldest as evicted, and length holds
	evicted := r.push(testSlot("s-3"))
	if evicted == nil || evicted.rec.requestID != "s-0" {
		t.Fatalf("evicted = %v, want s-0", evicted)
	}
	if r.len() != 3 {
		t.Errorf("len = %d, want 3 after wrap", r.len())
	}
}

// Position tracking age is the property the whole split exists to buy: it is
// what makes newest-first iteration and (in a later phase) the age cull O(1)
// rather than a scan.
func TestRing_positionTracksAgeAcrossWrap(t *testing.T) {
	// Given: a ring pushed well past its capacity
	r := newRing(4)
	for i := 0; i < 10; i++ {
		r.push(testSlot("s-" + strconv.Itoa(i)))
	}

	// Then: index 0 is the oldest survivor and index len-1 the newest
	want := []string{"s-6", "s-7", "s-8", "s-9"}
	for i, id := range want {
		if got := r.at(i).rec.requestID; got != id {
			t.Errorf("at(%d) = %q, want %q", i, got, id)
		}
	}
}

// A zero-capacity ring stores nothing, rather than quietly keeping the most
// recent entry. A population that is switched off — pinning, say — should
// retain none of its traces, not one of them.
func TestRing_zeroCapacityStoresNothing(t *testing.T) {
	r := newRing(0)
	if r.len() != 0 || r.cap() != 0 {
		t.Fatalf("len/cap = %d/%d, want 0/0", r.len(), r.cap())
	}

	// The pushed slot comes straight back as displaced, so the caller knows it
	// was not kept — and nothing divides by zero on the way.
	s := testSlot("only")
	if evicted := r.push(s); evicted != s {
		t.Errorf("push returned %v, want the slot straight back", evicted)
	}
	if r.len() != 0 {
		t.Errorf("len = %d after pushing to a zero-capacity ring, want 0", r.len())
	}
	if r.oldest() != nil || r.popOldest() != nil {
		t.Error("a zero-capacity ring reported an entry")
	}
}

// A single-slot ring is the smallest one that stores anything.
func TestRing_singleSlotKeepsTheNewest(t *testing.T) {
	r := newRing(1)
	if evicted := r.push(testSlot("first")); evicted != nil {
		t.Errorf("first push evicted %v", evicted)
	}
	if evicted := r.push(testSlot("second")); evicted == nil || evicted.rec.requestID != "first" {
		t.Errorf("evicted = %v, want first", evicted)
	}
	if r.len() != 1 {
		t.Errorf("len = %d, want 1", r.len())
	}
}
