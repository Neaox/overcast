package scheduler

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

func TestTypedOps_matchDispatchSurface(t *testing.T) {
	s := &Service{}
	ops := s.typedOps()
	expected := []string{
		"CreateScheduleGroup",
		"GetScheduleGroup",
		"DeleteScheduleGroup",
		"ListScheduleGroups",
		"TagResource",
		"UntagResource",
		"ListTagsForResource",
		"CreateSchedule",
		"GetSchedule",
		"UpdateSchedule",
		"DeleteSchedule",
		"ListSchedules",
	}

	if len(ops) != len(expected) {
		t.Fatalf("typed ops len = %d, expected %d", len(ops), len(expected))
	}
	for _, name := range expected {
		operation, ok := ops[name]
		if !ok {
			t.Fatalf("missing typed op %q", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed op %q has Name() %q", name, operation.Name())
		}
	}
	for name, operation := range ops {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}
