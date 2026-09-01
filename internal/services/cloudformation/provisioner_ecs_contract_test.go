package cloudformation

// provisioner_ecs_contract_test.go — the contract between the wording ECS puts
// on a service event and the matcher CloudFormation uses to find the failure
// among them.
//
// ecsServiceFailureEvent classifies service events by matching substrings of
// their text, because the text is all a failing service gives us: a deployment
// that has not tripped a circuit breaker reports an empty rolloutStateReason,
// so without the events a stack that failed because its tasks could not be
// placed has nothing to say beyond "Resource creation cancelled". The producer
// of that text is Overcast's own ECS emulation, one package over, and until now
// nothing connected the two: reword an event there and CloudFormation quietly
// falls back to reporting whichever event is newest — after a replacement
// attempt, usually "has started 1 tasks." — which is precisely the regression
// PR #993 fixed.
//
// Design. The faithful test would drive the real emitter: stand up an ECS
// handler, fail a deployment, read the events back. That is out of reach from
// here. recordPlacementFailure, recordDeploymentFailure and addServiceEvent are
// unexported, and reaching them the way production does — over the internal
// HTTP router, through CreateCluster and CreateService — means provoking a
// genuine placement or container failure, which makes this a container-runtime
// test rather than a wording test, and a slow and flaky one at that.
//
// So the wording is exported from the ecs package instead, and both the emitter
// and this file refer to the same constants. That buys three separate failures
// for three separate mistakes: rename a constant and this file stops compiling;
// reword one and the assertions below fail with the message the matcher no
// longer understands; add a new event and package ecs's own
// TestServiceEvents_everyCallSiteUsesADeclaredFormat makes you declare and
// classify it, which lands it in one of the two lists this file walks. What the
// arrangement deliberately does not do is import ecs from production code —
// CloudFormation talks to ECS over the internal router precisely so the two
// stay decoupled, and a test-only import keeps it that way.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/services/ecs"
)

func TestECSServiceFailureEvent_recognisesEveryFailureWording(t *testing.T) {
	for _, format := range ecs.FailureServiceEventFormats() {
		t.Run(serviceEventCaseName(format), func(t *testing.T) {
			// Given: the event ECS publishes when a deployment fails this way.
			message := fillServiceEventFormat(t, format)

			// When: CloudFormation looks through the service's events for the
			// failure to report.
			got := ecsServiceFailureEvent([]describedECSEvent{{Message: message}})

			// Then: it recognises this one.
			if got != message {
				t.Fatalf("ecsServiceFailureEvent(%q) = %q, want the event itself\n"+
					"ECS still publishes this wording, but provisioner_ecs.go no longer matches it, so a stack "+
					"that fails this way will report whichever event happens to be newest instead of the reason.",
					message, got)
			}
		})
	}
}

func TestECSServiceFailureEvent_leavesRoutineWordingAlone(t *testing.T) {
	for _, format := range ecs.RoutineServiceEventFormats() {
		t.Run(serviceEventCaseName(format), func(t *testing.T) {
			// Given: an event reporting ordinary progress. A service whose
			// tasks keep dying emits several of these while it fails, so they
			// share the event list with the real diagnostic.
			message := fillServiceEventFormat(t, format)

			// When: CloudFormation looks through the service's events for the
			// failure to report.
			got := ecsServiceFailureEvent([]describedECSEvent{{Message: message}})

			// Then: it finds none, and the caller falls through to the rollout
			// state rather than blaming a healthy event for the failure.
			if got != "" {
				t.Fatalf("ecsServiceFailureEvent(%q) = %q, want no match\n"+
					"A progress event is being read as a failure, so the stack will report it as the reason it "+
					"failed in place of the event that actually explains the failure.", message, got)
			}
		})
	}
}

func TestECSServiceFailureEvent_findsTheFailureUnderRoutineWording(t *testing.T) {
	// Given: the event list a crash-looping service really produces — every
	// failure wording buried under every routine wording, because ECS prepends
	// events and a replacement attempt runs after the failure that caused it.
	var events []describedECSEvent
	for _, format := range ecs.RoutineServiceEventFormats() {
		events = append(events, describedECSEvent{Message: fillServiceEventFormat(t, format)})
	}
	for _, format := range ecs.FailureServiceEventFormats() {
		events = append(events, describedECSEvent{Message: fillServiceEventFormat(t, format)})
	}

	// When: CloudFormation looks for the failure to report.
	got := ecsServiceFailureEvent(events)

	// Then: it reaches past the newer routine events to the placement failure,
	// the only one of the three that names a cause.
	want := fillServiceEventFormat(t, ecs.ServiceEventUnableToPlaceTaskFormat)
	if got != want {
		t.Fatalf("ecsServiceFailureEvent() = %q, want %q", got, want)
	}
}

// serviceEventCaseName turns a message format into a readable subtest name by
// dropping the "(service %s)" every event opens with.
func serviceEventCaseName(format string) string {
	return strings.TrimSpace(strings.TrimPrefix(format, "(service %s)"))
}

// fillServiceEventFormat renders a message format the way ECS would, with
// stand-in values. The values are deliberately inert — no word in them is one
// the matcher looks for — so that whether an event is classified as a failure
// is decided by the wording under test and not by what was substituted into it.
func fillServiceEventFormat(t *testing.T, format string) string {
	t.Helper()
	var args []any
	for i := 0; i < len(format)-1; i++ {
		if format[i] != '%' {
			continue
		}
		switch format[i+1] {
		case '%':
		case 's':
			args = append(args, "app-service")
		case 'd':
			args = append(args, 2)
		default:
			t.Fatalf("unhandled verb %%%c in %q; teach fillServiceEventFormat about it", format[i+1], format)
		}
		i++
	}
	return fmt.Sprintf(format, args...)
}
