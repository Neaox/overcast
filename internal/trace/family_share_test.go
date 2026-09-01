package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
)

// countPinnedFamily reports how much of the pinned ring is occupied by calls
// pinned alongside a failure, rather than by failures themselves.
func countPinnedFamily(b *Buffer) (family, failures int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i := 0; i < b.pinned.len(); i++ {
		s := b.pinned.at(i)
		if s == nil {
			continue
		}
		if s.viaFamily {
			family++
			continue
		}
		failures++
	}
	return family, failures
}

// One noisy deploy must not spend the failure budget on its own calls.
//
// Pinning a failure also pins the calls it made, and a CDK deploy makes
// thousands. Those calls occupy pinned slots, so without a share limit a single
// deploy fills the ring and every failure that arrives afterwards pushes an
// older failure out — the family displacing failures a step later, rather than
// at the moment it was pinned.
func TestFamilyShare_oneDeployCannotSpendTheFailureBudget(t *testing.T) {
	// Given: room for ten pinned traces
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 100, Ceiling: 100, Window: time.Hour, Pinned: 10}, clk)
	base := clk.Now()

	// When: a failed deploy dispatches far more calls than the ring could hold
	deployWithFailure(buf, base, 50)
	for i := 0; i < 200; i++ {
		addAt(buf, "later-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Second), http.StatusOK)
	}

	// Then: its calls take at most half the ring, so the rest stays available
	// for failures — which is what somebody actually comes back for
	family, _ := countPinnedFamily(buf)
	if family > 5 {
		t.Errorf("a single deploy's calls occupy %d of 10 pinned slots, want at most 5", family)
	}
	if family == 0 {
		t.Error("no calls were pinned at all, so this test proves nothing")
	}
}

// The share limit is what keeps room for later failures, so they must actually
// survive a deploy that would otherwise have filled the ring.
func TestFamilyShare_laterFailuresStillFit(t *testing.T) {
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 100, Ceiling: 100, Window: time.Hour, Pinned: 10}, clk)
	base := clk.Now()

	// Given: a failed deploy with a large family
	deployWithFailure(buf, base, 50)

	// When: four more unrelated failures arrive afterwards
	for i := 0; i < 4; i++ {
		addAt(buf, "fail-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Minute), http.StatusInternalServerError)
	}
	for i := 0; i < 200; i++ {
		addAt(buf, "later-"+strconv.Itoa(i), base.Add(time.Duration(i+10)*time.Minute), http.StatusOK)
	}

	// Then: every one of them is still retained. Failures compete with
	// failures; they do not compete with another failure's call log.
	for i := 0; i < 4; i++ {
		id := "fail-" + strconv.Itoa(i)
		if _, ok := buf.Get(id); !ok {
			t.Errorf("%s was evicted — a deploy's calls crowded out a later failure", id)
		}
	}
	if _, ok := buf.Get("deploy"); !ok {
		t.Error("the failed deploy itself was evicted")
	}
}

// The accounting has to come back down when family members leave the ring, or
// the share ratchets shut and no family is ever pinned again.
func TestFamilyShare_accountingIsReleased(t *testing.T) {
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 50, Ceiling: 50, Window: time.Hour, Pinned: 6}, clk)
	base := clk.Now()

	// Several deploys in turn, each with a family, cycling the pinned ring
	for d := 0; d < 5; d++ {
		at := base.Add(time.Duration(d) * time.Hour)
		parent := NewRecorder("deploy-"+strconv.Itoa(d), at, http.MethodPost, "/", "localhost", "", http.Header{})
		parent.SetServiceInfo("cloudformation", "CreateStack", "us-east-1")
		buf.Add(parent)
		for i := 0; i < 10; i++ {
			id := "d" + strconv.Itoa(d) + "-call-" + strconv.Itoa(i)
			child := NewRecorder(id, at.Add(time.Duration(i+1)*time.Millisecond),
				http.MethodPost, "/", "localhost", "", http.Header{})
			child.SetResponse(http.Header{}, []byte(`{}`), http.StatusOK, 1<<20, false)
			buf.Add(child)
			parent.AddHop(Hop{Service: "ssm", Operation: "PutParameter", RequestID: id, ResponseStatus: 200})
		}
		parent.SetResponse(http.Header{}, []byte(`{}`), http.StatusBadRequest, 1<<20, false)
		for i := 0; i < 60; i++ {
			addAt(buf, "flush-"+strconv.Itoa(d)+"-"+strconv.Itoa(i), at.Add(time.Duration(i+1)*time.Second), http.StatusOK)
		}
	}

	// The last deploy's calls must still be pinnable: if the counter only ever
	// went up, families would have stopped being pinned after the first deploy.
	family, _ := countPinnedFamily(buf)
	if family == 0 {
		t.Error("no family members pinned after several deploys; the share accounting ratcheted shut")
	}
}
