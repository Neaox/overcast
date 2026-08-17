package scheduler

// Tags are keyed by resource ARN in a namespace of their own, so nothing ties
// them to the lifetime of the schedule or group they describe. Deleting either
// left them behind, so ListTagsForResource kept answering for a resource that
// was gone and the store grew for the life of the session.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func newTagLifetimeService(t *testing.T) *Service {
	t.Helper()
	return New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		zap.NewNop(),
		clock.NewMock(),
	)
}

// storedTagCount reports how many tag blobs the whole namespace holds, which is
// what says whether a delete cleaned up or merely stopped pointing at it.
func storedTagCount(t *testing.T, s *Service) int {
	t.Helper()
	kvs, err := s.store.Scan(context.Background(), nsTags, "")
	if err != nil {
		t.Fatalf("scan tags: %v", err)
	}
	return len(kvs)
}

func seedTaggedSchedule(t *testing.T, s *Service, group, name string) {
	t.Helper()
	ctx := context.Background()
	if _, aerr := s.createScheduleTyped(ctx, &createScheduleRequest{
		GroupName:          group,
		Name:               name,
		ScheduleExpression: "rate(1 hour)",
		FlexibleTimeWindow: flexibleTimeWindow{Mode: "OFF"},
		Target: scheduleTarget{
			Arn:     "arn:aws:lambda:us-east-1:000000000000:function:noop",
			RoleArn: "arn:aws:iam::000000000000:role/noop",
		},
	}); aerr != nil {
		t.Fatalf("createScheduleTyped: %v", aerr)
	}
	if _, aerr := s.tagResourceTyped(ctx, &tagResourceRequest{
		ResourceArn: s.scheduleARN("us-east-1", group, name),
		Tags:        map[string]string{"env": "prod"},
	}); aerr != nil {
		t.Fatalf("tagResourceTyped: %v", aerr)
	}
}

func TestDeleteSchedule_takesItsTagsWithIt(t *testing.T) {
	// Given: a tagged schedule
	s := newTagLifetimeService(t)
	ctx := context.Background()
	seedTaggedSchedule(t, s, defaultGroup, "nightly")
	if got := storedTagCount(t, s); got != 1 {
		t.Fatalf("stored tag blobs = %d, want 1 before the delete", got)
	}

	// When: the schedule is deleted
	if _, aerr := s.deleteScheduleTyped(ctx, &deleteScheduleRequest{
		GroupName: defaultGroup,
		Name:      "nightly",
	}); aerr != nil {
		t.Fatalf("deleteScheduleTyped: %v", aerr)
	}

	// Then: its tags went with it
	if got := storedTagCount(t, s); got != 0 {
		t.Fatalf("stored tag blobs = %d, want 0 after the delete", got)
	}
}

// A group's delete takes its schedules with it, so it must take their tags too
// — the schedules are removed through the same path, and the group has its own
// tags on top.
func TestDeleteScheduleGroup_takesEveryTagWithIt(t *testing.T) {
	// Given: a tagged group holding a tagged schedule
	s := newTagLifetimeService(t)
	ctx := context.Background()
	if _, aerr := s.createScheduleGroupTyped(ctx, &createScheduleGroupRequest{
		Name: "batch",
		Tags: map[string]string{"env": "prod"},
	}); aerr != nil {
		t.Fatalf("createScheduleGroupTyped: %v", aerr)
	}
	seedTaggedSchedule(t, s, "batch", "nightly")
	if got := storedTagCount(t, s); got != 2 {
		t.Fatalf("stored tag blobs = %d, want 2 before the delete", got)
	}

	// When: the group is deleted
	if _, aerr := s.deleteScheduleGroupTyped(ctx, &deleteScheduleGroupRequest{Name: "batch"}); aerr != nil {
		t.Fatalf("deleteScheduleGroupTyped: %v", aerr)
	}

	// Then: nothing the group owned is left behind
	if got := storedTagCount(t, s); got != 0 {
		t.Fatalf("stored tag blobs = %d, want 0 after the delete", got)
	}
}
