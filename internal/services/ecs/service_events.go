package ecs

// service_events.go — the wording of every message an ECS service event can
// carry, declared once so that the code outside this package which reads that
// wording has something to hold on to.
//
// A service whose tasks will not start explains itself through its event list
// and almost nowhere else: rolloutStateReason stays empty unless a deployment
// circuit breaker tripped, so the event text is usually the only account of the
// failure a caller ever gets. CloudFormation's ECS provisioner therefore picks
// the failure out of the list by matching this wording — see
// ecsServiceFailureEvent in internal/services/cloudformation/provisioner_ecs.go
// — and reports it as the stack's status reason, in place of the "Resource
// creation cancelled" that a rollback would otherwise leave behind.
//
// Nothing in the compiler joins those two halves. Reword an event in place and
// the match quietly stops firing: the stack still fails, but it fails with
// whatever event happens to be newest, which after a replacement attempt is
// usually a cheerful "has started 1 tasks." — the exact diagnostic loss that
// motivated the constants and the contract test over them
// (provisioner_ecs_contract_test.go, in package cloudformation).
//
// So every message a service event carries is declared here, listed in exactly
// one of the two groups below, and a call site that formats a literal of its
// own fails TestServiceEvents_everyCallSiteUsesADeclaredFormat. Adding an event
// is then a decision about which group it belongs to, made once and visible to
// the reader, rather than an omission nobody notices until a stack rolls back
// with nothing useful to say.

// Failure-shaped service events. CloudFormation treats a service reporting any
// of these as a failed resource and quotes the message back to the caller, so
// their wording is load-bearing beyond this package.
const (
	// ServiceEventUnableToPlaceTaskFormat reports a task the scheduler could
	// not place. It is the most useful of the three, because the cause follows
	// "Reason:" verbatim — insufficient capacity, an unsatisfiable placement
	// constraint, a missing subnet — and that suffix is what CloudFormation
	// prefers over any other failure event.
	ServiceEventUnableToPlaceTaskFormat = "(service %s) was unable to place a task. Reason: %s"
	// ServiceEventUnableToStartConsistentlyFormat reports that a deployment has
	// failed tasks often enough to look like a crash loop rather than a blip.
	// AWS emits it once as the count crosses the threshold, not once per task.
	ServiceEventUnableToStartConsistentlyFormat = "(service %s) is unable to consistently start tasks successfully. For more information, see the Troubleshooting section."
	// ServiceEventDeploymentFailedFormat is what a tripped deployment circuit
	// breaker announces. It says the deployment is over but not why, which is
	// why CloudFormation reaches past it for a reason-bearing event first.
	ServiceEventDeploymentFailedFormat = "(service %s) (deployment %s) deployment failed: tasks failed to start."
)

// Routine service events. These report ordinary lifecycle progress, and
// CloudFormation must go on treating them as progress: several of them are
// emitted while a deployment is failing — a service that cannot keep its tasks
// alive starts and stops them repeatedly — so misreading one as a failure, or
// simply reporting the newest event without classifying it, is how a real
// diagnostic gets replaced by "has started 1 tasks.".
const (
	// ServiceEventDrainingConnectionsFormat reports a scale-down beginning.
	ServiceEventDrainingConnectionsFormat = "(service %s) has begun draining connections on %d tasks."
	// ServiceEventForcedDeploymentFormat reports a deployment started without a
	// task-definition change, as `aws ecs update-service --force-new-deployment`
	// asks for.
	ServiceEventForcedDeploymentFormat = "(service %s) has begun a forced deployment."
	// ServiceEventUpdatedTaskDefinitionFormat reports a new task definition
	// taking over. Note that it opens with "was updated", one word away from
	// the "was unable to" the failure matcher looks for.
	ServiceEventUpdatedTaskDefinitionFormat = "(service %s) was updated to use task definition %s."
	// ServiceEventDrainingFormat reports a service being deleted.
	ServiceEventDrainingFormat = "(service %s) is draining."
	// ServiceEventStartedTasksFormat reports tasks reaching RUNNING. A
	// crash-looping service emits it on every replacement attempt, which is
	// precisely why it keeps landing at the head of the event list.
	ServiceEventStartedTasksFormat = "(service %s) has started %d tasks."
	// ServiceEventStoppedTasksFormat reports tasks the scheduler stopped,
	// whether they were superseded by a newer deployment or scaled away.
	ServiceEventStoppedTasksFormat = "(service %s) has stopped %d tasks."
	// ServiceEventSteadyStateFormat reports a deployment that has finished:
	// desired count running, nothing left to replace.
	ServiceEventSteadyStateFormat = "(service %s) has reached a steady state."
	// ServiceEventRolloutHeldAtCeilingFormat reports a rollout that cannot
	// place its next task without breaching its own maximumPercent, while
	// minimumHealthyPercent forbids retiring anything to make room. AWS has no
	// event for this — the rollout simply stops moving, which is the least
	// diagnosable failure a service has, so Overcast says so instead. It is
	// routine rather than failure-shaped on purpose: the configuration is the
	// caller's own, nothing has failed, and CloudFormation must not report a
	// stack failure over a deployment that is doing exactly as it was told.
	ServiceEventRolloutHeldAtCeilingFormat = "(service %s) is holding at %d task(s): its deployment configuration permits at most %d and requires %d running."
)

// FailureServiceEventFormats returns every service-event message that reports a
// deployment failing. CloudFormation's failure matcher must recognise all of
// them; the contract test in package cloudformation walks this list to check
// that it still does.
func FailureServiceEventFormats() []string {
	return []string{
		ServiceEventUnableToPlaceTaskFormat,
		ServiceEventUnableToStartConsistentlyFormat,
		ServiceEventDeploymentFailedFormat,
	}
}

// RoutineServiceEventFormats returns every service-event message that reports
// ordinary progress. CloudFormation's failure matcher must recognise none of
// them, so that a rollout still in flight — or one already recovering — is not
// reported to the user as the reason a stack failed.
func RoutineServiceEventFormats() []string {
	return []string{
		ServiceEventDrainingConnectionsFormat,
		ServiceEventForcedDeploymentFormat,
		ServiceEventUpdatedTaskDefinitionFormat,
		ServiceEventDrainingFormat,
		ServiceEventStartedTasksFormat,
		ServiceEventStoppedTasksFormat,
		ServiceEventSteadyStateFormat,
		ServiceEventRolloutHeldAtCeilingFormat,
	}
}
