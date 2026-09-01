package lifecycle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/benbjohnson/clock"

	"github.com/overcast-sh/overcast/internal/middleware"
)

// TestAfterScoped verifies the two guarantees AfterScoped exists for: the
// callback context carries the scheduling region, and keys are region-scoped
// so same-named resources in different regions don't collide.
func TestAfterScoped(t *testing.T) {
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var mu sync.Mutex
	var fired []string
	record := func(ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, middleware.RegionFromContext(ctx, "MISSING"))
	}

	s.AfterScoped("us-east-1", "db1", "available", time.Second, record)
	s.AfterScoped("eu-west-1", "db1", "available", time.Second, record)

	// Same id + transition in two regions must be two distinct pending keys.
	if got := s.PendingCount(); got != 2 {
		t.Fatalf("PendingCount = %d, want 2 (regions collided on the scheduler key)", got)
	}

	mock.Add(2 * time.Second)
	mu.Lock()
	if len(fired) != 2 {
		mu.Unlock()
		t.Fatalf("fired %d callbacks, want 2", len(fired))
	}
	regions := map[string]bool{fired[0]: true, fired[1]: true}
	mu.Unlock()
	if !regions["us-east-1"] || !regions["eu-west-1"] {
		t.Fatalf("callback contexts carried regions %v, want both us-east-1 and eu-west-1", regions)
	}
}

// TestCancelScoped verifies cancellation only affects the named region.
func TestCancelScoped(t *testing.T) {
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var mu sync.Mutex
	var fired []string
	record := func(ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, middleware.RegionFromContext(ctx, "MISSING"))
	}

	s.AfterScoped("us-east-1", "db1", "health", time.Second, record)
	s.AfterScoped("eu-west-1", "db1", "health", time.Second, record)

	if !s.CancelScoped("us-east-1", "db1", "health") {
		t.Fatal("CancelScoped returned false for a pending transition")
	}
	if got := s.PendingCount(); got != 1 {
		t.Fatalf("PendingCount after cancel = %d, want 1", got)
	}

	mock.Add(2 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 || fired[0] != "eu-west-1" {
		t.Fatalf("fired = %v, want exactly [eu-west-1] (cancel leaked across regions)", fired)
	}
}
