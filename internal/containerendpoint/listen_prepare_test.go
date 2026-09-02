package containerendpoint

// listen_prepare_test.go — the probe image is fetched before the walk's clock
// starts (#1586).
//
// probeImagePullTimeout is 60s and was documented as spent outside the
// per-candidate budget. It was — and inside probeTotalBudget, which is 45s, so
// it could never be reached: a cold pull on a slow link was cut off by the
// walk, every candidate came back "unavailable", and the address was chosen
// with nothing measured. The pull now runs once, against the caller's own
// context, before the budget is derived.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// prepareBudget is what the walk gets. Deliberately far shorter than any real
// pull, so a prepare that ran inside it would be visible as a truncation.
const prepareBudget = 20 * time.Millisecond

func TestResolveListen_preparesTheProbeOutsideTheWalkBudget(t *testing.T) {
	probe := reachableFrom(dockerInternalHost)
	d := deps(func() bool { return false }, "192.168.1.10", func(string) bool { return true }, probe)
	d.budget = prepareBudget

	var prepareDeadline time.Time
	var hadDeadline bool
	d.prepare = func(ctx context.Context) error {
		prepareDeadline, hadDeadline = ctx.Deadline()
		return nil
	}

	// The caller's own context carries no deadline, which is what makes the
	// pull's own 60s reachable.
	got := resolveListen(context.Background(), nil, ListenOptions{Network: "overcast_control"}, d)

	if hadDeadline {
		t.Errorf("prepare ran under a deadline (%s); the walk budget must not bound the image pull",
			time.Until(prepareDeadline))
	}
	if !got.Verified {
		t.Errorf("Listen = %+v, want the probe to have run after a successful prepare", got)
	}
	if len(probe.asked) == 0 {
		t.Error("no candidate was probed")
	}
}

// A pull that cannot finish is not a verdict about any address. It lands where
// a daemon that cannot run the probe lands: the ordering stands on its own,
// nothing claims it was established, and nothing is reported broken.
func TestResolveListen_anUnfetchableImageIsNotAVerdict(t *testing.T) {
	probe := reachableFrom(dockerInternalHost)
	d := deps(func() bool { return false }, "192.168.1.10", func(string) bool { return true }, probe)
	d.prepare = func(context.Context) error { return errors.New("pull busybox: no route to the registry") }

	got := resolveListen(context.Background(), nil, ListenOptions{Network: "overcast_control"}, d)

	if got.ContainerHost == "" {
		t.Fatal("no address at all; a failed pull must not take Lambda from broken to absent")
	}
	if got.Verified {
		t.Error("Verified = true; nothing was measured")
	}
	if got.Unreachable {
		t.Error("Unreachable = true; a missing measurement is not a broken host")
	}
	if len(probe.asked) != 0 {
		t.Errorf("candidates were probed without the image: %v", probe.asked)
	}
}

// No prepare is the ordinary case for a caller with no daemon, and for every
// existing test. It must not change the walk.
func TestResolveListen_worksWithNothingToPrepare(t *testing.T) {
	probe := reachableFrom(dockerInternalHost)
	d := deps(func() bool { return false }, "192.168.1.10", func(string) bool { return true }, probe)

	got := resolveListen(context.Background(), nil, ListenOptions{Network: "overcast_control"}, d)

	if !got.Verified {
		t.Errorf("Listen = %+v, want the probe to have run", got)
	}
}
