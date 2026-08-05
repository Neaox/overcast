package ecs

import (
	"testing"
	"time"
)

func TestAddServiceEvent_preservesMilliseconds(t *testing.T) {
	// Given: an event occurs between whole seconds.
	h, clk := newECSRegionTestHandler(t)
	now := time.Date(2026, time.August, 5, 7, 6, 20, int(347*time.Millisecond), time.UTC)
	clk.Set(now)
	svc := &ecsService{}

	// When: ECS records the service event.
	h.addServiceEvent(svc, "task stopped")

	// Then: the AWS timestamp retains enough precision to order same-second events.
	if got, want := svc.Events[0].CreatedAt, float64(now.UnixMilli())/1000; got != want {
		t.Fatalf("createdAt = %v, want %v", got, want)
	}
}
