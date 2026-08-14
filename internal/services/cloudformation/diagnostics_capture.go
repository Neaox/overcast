package cloudformation

// diagnostics_capture.go — when the journal is written, and what stops it
// getting in the way.
//
// Capture runs at the moment a deploy is decided to have failed, which is the
// last moment the evidence still exists: the ECS service is still there with
// its event history, its stopped task records still carry their exit codes,
// and the container output retained when those containers died is still
// reachable. The rollback that follows deletes all of it, exactly as AWS
// deletes it, and that is not something to work around — see hard rule 2 in
// diagnostics.go. Reading before deleting is the whole trick.
//
// The write is deferred until the operation returns, so the entry records the
// status the stack actually came to rest at rather than the in-progress status
// capture ran under. Gathering happens before teardown; recording happens
// after it. Moving the gathering to where the recording is would produce an
// empty journal on every ECS failure, which is what the P0 test pins.
//
// Everything here is best-effort and bounded. A collector that fails, hangs or
// finds nothing must not fail the rollback, must not change the stack's
// status, and must not hold up teardown for longer than its budget. A missing
// section is a missing section.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// deployDiagnosticsBudget bounds the whole capture — every collector, every
// internal call — because it runs on the failure path while a developer is
// watching a deploy fail. Long enough for a handful of in-process dispatches,
// short enough that a wedged collector costs a pause rather than a hang. The
// budget covers gathering only; the store write that follows is not on it.
const deployDiagnosticsBudget = 5 * time.Second

// maxCapturedLogBytes bounds the container output copied into one entry. The
// tail is kept, not the head — the last thing a process says before it gives
// up is the useful part — at the same 16 KiB the RDS retained-logs endpoint
// settled on, for the same reason: enough for a stack trace and the lines
// around it, small enough that the record stays a record.
const maxCapturedLogBytes = 16 * 1024

// maxCapturedTasks caps how many stopped tasks one resource contributes. A
// service that failed fifty times says the same thing fifty times; a readable
// sample plus a count of what was left out is more useful than all of it, and
// it is what keeps a single entry bounded when a crash-looping container is
// the failure being diagnosed.
const maxCapturedTasks = 3

// diagnosticCollector gathers evidence for one failed resource. Collectors are
// registered by CloudFormation resource type and reach their service over the
// emulator router — never by importing it. That is not incidental tidiness:
// CloudFormation deliberately has no Go import of any service package, and the
// provisioner has stayed decoupled for the whole of its life by dispatching
// through the router instead. A collector that imported `internal/services/ecs`
// would be the first crack in that, for no benefit — the router reaches the
// same code.
//
// A collector returns what it found. It reports no error: there is nothing a
// caller could usefully do with one, and a collector that cannot answer should
// say so in a section note rather than by failing.
type diagnosticCollector func(ctx context.Context, router http.Handler, region string, res StackResource) collectedEvidence

// collectedEvidence is one collector's contribution: the panes it gathered,
// plus the two Overcast-authored sentences the payload carries at top level.
type collectedEvidence struct {
	Sections []DiagnosticSection
	// Headline is Overcast's one-sentence reading of the evidence.
	Headline string
	// Counterfactual names what real AWS would have given instead. Collectors
	// write it because only the collector knows which of its panes AWS would
	// also have produced.
	Counterfactual string
}

// diagnosticCollectors maps a CloudFormation resource type to the collector
// that knows how to diagnose it. ECS is the only entry today; a Lambda
// (init failure: error type, init duration, init output) and an RDS
// (start failure: status, reason, engine output) collector are the obvious
// next two, and both fit the existing section kinds without extending them.
//
// A resource type with no collector contributes no sections, which is a
// perfectly good outcome: the entry still carries the reason CloudFormation
// recorded, and it correctly offers no counterfactual, because nothing was
// preserved that AWS would have discarded.
var diagnosticCollectors = map[string]diagnosticCollector{
	"AWS::ECS::Service": collectECSServiceEvidence,
}

// collectDeployDiagnostics gathers the diagnosis for a failed deploy. Call it
// at the point of failure, before anything is torn down; pair every call with
// a deferred recordDeployDiagnostics so the entry is written once the stack
// has reached its terminal status.
//
// attempted is the resource list the operation built — stack.Resources on a
// create, the update's own in-progress list on an update — because on the
// update path the failed resource is not on the stack record yet.
//
// Returns nil when there is nothing worth journalling, which is the signal for
// recordDeployDiagnostics to do nothing.
func (p *provisioner) collectDeployDiagnostics(
	ctx context.Context,
	stack *Stack,
	operation string,
	attempted []StackResource,
) *DeployDiagnostics {
	failed := failedResources(attempted)
	if len(failed) == 0 {
		return nil
	}

	p.mu.Lock()
	router := p.router
	p.mu.Unlock()

	out := &DeployDiagnostics{
		StackName:  stack.StackName,
		StackID:    stack.StackID,
		Operation:  operation,
		CapturedAt: p.clk.Now().UTC().Format(time.RFC3339),
		AWSReason:  failed[0].StatusReason,
		Resources:  make([]DiagnosticResource, 0, len(failed)),
	}

	// The budget covers gathering and only gathering. It derives from the
	// operation's context so that a shutdown cancels capture rather than
	// outliving it — a diagnosis nobody will read is not worth delaying an
	// exit for.
	gatherCtx, cancel := context.WithTimeout(ctx, deployDiagnosticsBudget)
	defer cancel()

	for _, r := range failed {
		entry := DiagnosticResource{
			LogicalID:    r.LogicalID,
			PhysicalID:   r.PhysicalID,
			Type:         r.Type,
			StatusReason: r.StatusReason,
		}
		if collect, ok := diagnosticCollectors[r.Type]; ok && router != nil {
			evidence := collect(gatherCtx, router, stack.Region, r)
			entry.Sections = stampCaptureTime(evidence.Sections, out.CapturedAt)
			if out.Headline == "" {
				out.Headline = evidence.Headline
			}
			if out.Counterfactual == "" {
				out.Counterfactual = evidence.Counterfactual
			}
		}
		out.Resources = append(out.Resources, entry)
	}

	// The counterfactual is a claim that Overcast kept something AWS would
	// have discarded. With nothing preserved there is no such claim to make,
	// so the field is dropped rather than filled with a weaker sentence.
	// Enforced here rather than trusted to each collector, so a collector
	// added later cannot make the claim without the evidence.
	if !out.preservedSomething() {
		out.Counterfactual = ""
	}
	return out
}

// recordDeployDiagnostics writes the gathered entry, stamping the status the
// stack finished at. Deferred by every caller, so it runs after the rollback
// has completed.
//
// A capture that found nothing never displaces one that found something. That
// matters for the one path that can capture the same failure twice: a deploy
// that failed with DisableRollback journals what it saw, and a later
// RollbackStack journals again — by which time ECS may have reaped the stopped
// task records the first capture read. Overwriting a real diagnosis with an
// empty one there would lose the answer to the very question being asked.
func (p *provisioner) recordDeployDiagnostics(ctx context.Context, stack *Stack, d *DeployDiagnostics) {
	if d == nil {
		return
	}
	log := p.log.WithRecorder(ctx)
	d.StackStatus = stack.Status

	if !d.hasEvidence() {
		if existing, err := p.store.getDeployDiagnostics(ctx, stack.StackName); err == nil && existing.hasEvidence() {
			log.Debug("cfn: keeping the earlier deploy diagnosis; this capture found nothing",
				zap.String("stack", stack.StackName))
			return
		}
	}

	if err := p.store.putDeployDiagnostics(ctx, d); err != nil {
		// A journal that could not be stored is a lost convenience, not a
		// failed deploy. It must never reach the stack's status.
		log.Warn("cfn: could not record deploy diagnostics",
			zap.String("stack", stack.StackName), zap.Error(err))
		return
	}
	// This log line is Overcast talking in its own voice, in the terminal the
	// developer is already watching, so it may name the console tab — the
	// pointer that hard rule 1 forbids in any AWS-shaped field.
	log.Info("cfn: captured deploy diagnostics before rollback; see the stack's Diagnostics tab",
		zap.String("stack", stack.StackName),
		zap.String("status", stack.Status))
}

// stampCaptureTime fills in the capture time on any log pane that did not set
// one. Collectors gather everything in a single instant and have no clock of
// their own, so the time belongs here rather than in each of them — and a
// collector that reuses an older retained copy keeps the time it set.
func stampCaptureTime(sections []DiagnosticSection, capturedAt string) []DiagnosticSection {
	for i := range sections {
		if sections[i].Log != nil && sections[i].Log.CapturedAt == "" {
			sections[i].Log.CapturedAt = capturedAt
		}
	}
	return sections
}

// clearDeployDiagnostics removes a stack's journal entry after a deploy
// succeeds, which is what makes "a journal exists" mean "the most recent
// deploy of this stack failed".
//
// Without it a stack that failed, was fixed and now reads CREATE_COMPLETE
// would keep answering with an explanation of a deploy that no longer
// describes anything — a failure story attached to a green stack, which is
// worse than no story at all. The console cannot filter that out for itself:
// whether a journal exists is the answer to its request, not a precondition
// of making it.
//
// Only a *successful deploy* clears it. DeleteStack deliberately does not,
// which matters more than it sounds: a stack left ROLLBACK_COMPLETE is
// delete-only, so the CDK CLI's response to a failed deploy is to delete the
// stack and create it again. Clearing on delete would take the diagnosis away
// at precisely the moment the developer went looking for it. The tombstone
// record DeleteStack leaves behind keeps the stack's final state and its
// events readable for the same reason, and the diagnosis belongs with them.
//
// Best-effort, like every other part of this. A stack that provisioned
// correctly must not be failed over a stale diagnostic record.
func (p *provisioner) clearDeployDiagnostics(ctx context.Context, stack *Stack) {
	if err := p.store.deleteDeployDiagnostics(ctx, stack.StackName); err != nil {
		p.log.WithRecorder(ctx).Warn("cfn: could not clear the previous deploy diagnostics",
			zap.String("stack", stack.StackName), zap.Error(err))
	}
}

// failedResources picks the resources whose failure this deploy stopped for.
// Provisioning halts at the first failure, so in practice there is one — but
// the list is the honest shape, and the payload is built for more than one.
func failedResources(attempted []StackResource) []StackResource {
	var failed []StackResource
	for _, r := range attempted {
		if r.Status == ResourceCreateFailed || r.Status == ResourceUpdateFailed {
			failed = append(failed, r)
		}
	}
	return failed
}

// quoteReason wraps a CloudFormation reason for inclusion in a sentence,
// trimming the trailing full stop so the sentence does not end in "..".
func quoteReason(reason string) string {
	return `"` + strings.TrimRight(strings.TrimSpace(reason), ".") + `."`
}
