//go:build linux

package lambdainit

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// startShipper wires a shipper to the fake host and runs it until the test
// finishes.
func startShipper(t *testing.T, h *fakeHost, frames, bytes int) *shipper {
	t.Helper()
	var diag lockedBuffer
	s := newShipper(h.addr(), frames, bytes, time.Now, &diagLog{w: &diag})
	ctx, cancel := context.WithCancel(context.Background())
	go s.run(ctx)
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.close(closeCtx)
		closeCancel()
		cancel()
		if t.Failed() {
			t.Logf("shipper diagnostics:\n%s", diag.String())
		}
	})
	return s
}

func TestShipperDeliversFramesInOrder(t *testing.T) {
	h := newFakeHost(t)
	s := startShipper(t, h, 0, 0)

	for i := 1; i <= 20; i++ {
		if got := s.publish("req-1", initproto.SrcStdout, "line-"+strconv.Itoa(i)); got != uint64(i) {
			t.Fatalf("publish %d returned seq %d", i, got)
		}
	}

	h.mustAwaitFrameCount(20)
	frames := h.snapshotFrames()
	assertContiguous(t, frames)
	for i, f := range frames {
		if f.Msg != "line-"+strconv.Itoa(i+1) || f.Req != "req-1" || f.Src != initproto.SrcStdout {
			t.Fatalf("frame %d = %+v", i, f)
		}
	}
}

func TestShipperReconnectsAndReplaysWithoutLoss(t *testing.T) {
	h := newFakeHost(t)
	// The host hangs up after the third frame, mid-stream, the way a restarted
	// listener or a dropped connection would.
	h.setDropAfter(3)

	s := startShipper(t, h, 0, 0)
	for i := 1; i <= 5; i++ {
		s.publish("", initproto.SrcStdout, "line-"+strconv.Itoa(i))
	}
	h.mustAwaitFrameCount(3)
	h.awaitHangUp()

	// A dead connection announces itself on the next write, not before: an
	// init with nothing to say has nothing to discover. These lines are what
	// discovers it, and what the replay then has to deliver.
	for i := 6; i <= 10; i++ {
		s.publish("", initproto.SrcStdout, "line-"+strconv.Itoa(i))
	}

	h.mustAwaitFrameCount(10)
	frames := h.snapshotFrames()

	// The host de-duplicates by seq, so what it holds after the replay is
	// exactly the stream the init published: nothing lost, nothing doubled.
	assertContiguous(t, frames)
	if len(frames) != 10 {
		t.Fatalf("host holds %d frames, want 10: %v", len(frames), frameMessages(frames))
	}
	for _, f := range frames {
		if f.Gap != 0 {
			t.Fatalf("a reconnect that lost nothing reported a gap: %+v", f)
		}
	}
	if n := h.streamCount(); n < 2 {
		t.Fatalf("the init opened %d log streams, so it never reconnected", n)
	}
}

func TestShipperReportsAGapWhenTheBacklogOverflows(t *testing.T) {
	h := newFakeHost(t)

	// Publish before the shipper is running: 20 frames into a backlog that
	// holds 4, so 16 are lost before anything can be sent.
	var diag lockedBuffer
	s := newShipper(h.addr(), 4, 0, time.Now, &diagLog{w: &diag})
	for i := 1; i <= 20; i++ {
		s.publish("", initproto.SrcStdout, "line-"+strconv.Itoa(i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.close(closeCtx)
		closeCancel()
	}()

	h.mustAwaitFrameCount(5) // the gap frame plus the four survivors
	frames := h.snapshotFrames()

	gap := frames[0]
	if gap.Gap != 16 {
		t.Fatalf("first frame = %+v, want a gap of 16", gap)
	}
	if gap.Seq != 16 {
		t.Fatalf("the gap frame is at seq %d, want 16 — the last seq that was lost", gap.Seq)
	}
	// After the gap the sequence continues unbroken, so the host knows exactly
	// what it has.
	assertContiguous(t, frames)
	for i, f := range frames[1:] {
		if want := "line-" + strconv.Itoa(17+i); f.Msg != want {
			t.Fatalf("frame after the gap = %q, want %q", f.Msg, want)
		}
	}
	if !strings.Contains(diag.String(), "[overcast-init] log channel gap: 16 frames") {
		t.Fatalf("the gap was not reported in the diagnostics:\n%s", diag.String())
	}
}

func TestShipperFlushWaitsForEverythingPublished(t *testing.T) {
	h := newFakeHost(t)
	s := startShipper(t, h, 0, 0)

	for i := 1; i <= 50; i++ {
		s.publish("req-9", initproto.SrcStderr, "line-"+strconv.Itoa(i))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got, want := s.writtenSeq(), s.currentSeq(); got != want {
		t.Fatalf("flush returned with %d of %d frames written", got, want)
	}

	h.mustAwaitFrameCount(50)
	assertContiguous(t, h.snapshotFrames())
}
