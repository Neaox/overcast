package lambda

import (
	"context"
	"net"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

// shutdown_idempotency_test.go covers the shutdown methods Service.Stop drives.
//
// Service.Stop is reachable more than once — process shutdown racing a
// signal handler, a test that stops the service and also registers Stop as
// t.Cleanup, a service stopped and restarted in-process — so every Stop it
// calls has to survive a second call. Each of these used to close its stop
// channel unconditionally and panic with "close of closed channel".

func TestInstanceTracker_stopIsIdempotent(t *testing.T) {
	// Given: a running tracker.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())

	// When: shutdown runs twice, as a doubled Service.Stop would drive it.
	tracker.Stop()

	// Then: the second call is a no-op rather than a panic.
	tracker.Stop()
}

func TestInstancePool_stopIsIdempotent(t *testing.T) {
	// Given: a running pool.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})

	// When: shutdown runs twice.
	pool.Stop()

	// Then: the second call is a no-op rather than a panic.
	pool.Stop()
}

func TestRuntimeAPIServer_stopIsIdempotent(t *testing.T) {
	// Given: a Runtime API server on an OS-assigned loopback port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := NewRuntimeAPIServerFromListener(ln, ln.Addr().String(), zap.NewNop(), clock.NewMock())
	if err != nil {
		t.Fatalf("NewRuntimeAPIServerFromListener() error = %v", err)
	}

	// When: shutdown runs twice.
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}

	// Then: the second call is a no-op rather than a panic, and still reports
	// the shutdown as clean.
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}
