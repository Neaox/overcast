package smtp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// TestServe_ShutdownDoesNotStealSemaphoreToken pins the ctx.Done() branch of the
// accept loop's slot-acquisition select. Saturating sema makes the
// `s.sema <- struct{}{}` case permanently unready, so the select must take
// ctx.Done() instead of choosing between two ready cases at random.
//
// The regression: a bare `break` inside that select terminates the select, not
// the accept loop. The loop fell through and spawned a handler for the
// connection it had just closed. That handler's deferred `<-s.sema` then
// releases a slot it never acquired, permanently lowering the effective
// concurrency limit.
func TestServe_ShutdownDoesNotStealSemaphoreToken(t *testing.T) {
	srv := NewServer("127.0.0.1:0", NewMailStore(10))

	// Saturate the semaphore so the accept loop cannot acquire a slot.
	for i := 0; i < maxConcurrentConnections; i++ {
		srv.sema <- struct{}{}
	}

	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.Serve(ctx)
	}()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Wait out the accept rather than synchronising on it: the handler that
	// would write the SMTP greeting is precisely the thing that must not run,
	// so there is no in-band signal for "Accept returned".
	time.Sleep(200 * time.Millisecond)

	cancel()

	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return within 10s of context cancellation")
	}

	if got := len(srv.sema); got != maxConcurrentConnections {
		t.Errorf("semaphore holds %d tokens after shutdown, want %d — the accept loop "+
			"spawned a handler that released a slot it never acquired",
			got, maxConcurrentConnections)
	}
}

// TestServe_ShutdownWithPendingAcceptDoesNotHang covers the same defect through
// its worst consequence: with sema empty (the ordinary shutdown case) the
// wrongly-spawned handler's deferred `<-s.sema` blocks forever, so s.wg.Done()
// never runs and Serve never returns from s.wg.Wait().
//
// Reaching the ctx.Done() branch here is probabilistic — with sema empty both
// select cases are ready and Go picks at random — so the test races cancellation
// against a burst of dials over many iterations. On the unfixed code it wedges
// well within the iteration budget.
func TestServe_ShutdownWithPendingAcceptDoesNotHang(t *testing.T) {
	const iterations = 50
	const connsPerIteration = 8

	for i := 0; i < iterations; i++ {
		srv := NewServer("127.0.0.1:0", NewMailStore(10))
		addr, err := srv.Listen()
		if err != nil {
			t.Fatalf("iteration %d: Listen: %v", i, err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		served := make(chan struct{})
		go func() {
			defer close(served)
			srv.Serve(ctx)
		}()

		// Dial concurrently with the cancellation so connections are still
		// arriving as the context goes down — the window the bug lives in.
		var dialers sync.WaitGroup
		for j := 0; j < connsPerIteration; j++ {
			dialers.Add(1)
			go func() {
				defer dialers.Done()
				// Close immediately: a peer-closed connection is still handed
				// to Accept, and it keeps handlers from lingering on reads.
				if c, derr := net.DialTimeout("tcp", addr, 2*time.Second); derr == nil {
					_ = c.Close()
				}
				// A dial error is expected once the listener closes.
			}()
		}
		cancel()
		dialers.Wait()

		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: Serve did not return within 5s of cancellation — a handler "+
				"was spawned without a semaphore token and its deferred release blocked forever", i)
		}
	}
}
