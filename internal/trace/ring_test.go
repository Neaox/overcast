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

func TestRing_emptyAndSingleSlot(t *testing.T) {
	// A zero or negative capacity must still yield a usable ring rather than
	// panicking on a modulo by zero.
	r := newRing(0)
	if r.len() != 0 {
		t.Fatalf("len = %d, want 0", r.len())
	}
	if evicted := r.push(testSlot("only")); evicted != nil {
		t.Errorf("first push evicted %v", evicted)
	}
	if evicted := r.push(testSlot("next")); evicted == nil || evicted.rec.requestID != "only" {
		t.Errorf("evicted = %v, want only", evicted)
	}
	if r.len() != 1 {
		t.Errorf("len = %d, want 1", r.len())
	}
}
