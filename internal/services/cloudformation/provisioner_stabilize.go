package cloudformation

// provisioner_stabilize.go — the wait every asynchronous resource shares.
//
// An ECS service and an RDS instance each grew their own polling loop before
// this existed, and the two drifted: one read its clock off time.Now(), the
// other off the injected clock; one reported the resource's own account of the
// failure, the other a bare timeout. Five more hand-rolled loops would be five
// more chances to get the same details wrong, so what varies between resources
// is all that a resource type writes down here:
//
//   - the describe call that answers with the resource's current status, and
//     with the service's own account of a failure where the service records one
//   - what that service's status values mean — which one is done, which ones
//     are the end of the road
//   - how long the resource is worth waiting for
//
// Everything else — the poll cadence, the deadline, honouring cancellation, and
// the wording of the two failures a wait can end in — is the same for all of
// them and lives here.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// resourceStabilizePollInterval is how often a wait re-reads a status. Every
// dispatch is in-process, so the cost of asking is small; the resources behind
// these waits take seconds at the very best, so the value of asking faster is
// smaller still. The RDS wait settled on the same quarter-second.
const resourceStabilizePollInterval = 250 * time.Millisecond

// readinessOutcome is what a status means for the resource waiting on it.
type readinessOutcome int

const (
	// readinessWaiting — the resource is still working. Keep polling.
	readinessWaiting readinessOutcome = iota
	// readinessReady — the resource is finished and usable.
	readinessReady
	// readinessFailed — the resource will not become ready. Fail now rather
	// than hold the stack open for the rest of the budget.
	readinessFailed
)

// statusVocabulary is one service's status values, split into the two sets that
// end a wait. Everything not named keeps the resource waiting: AWS adds
// statuses, and treating an unrecognised one as done would complete a resource
// in a state nothing here understands — the exact failure these waits exist to
// prevent.
//
// Comparison folds case. The status vocabularies are AWS's own and AWS is not
// consistent about it — an ElastiCache replication group reports "available"
// and an ElastiCache serverless cache reports "AVAILABLE" — so matching
// literally would leave one of the two waiting out its whole budget over a
// cache that was ready on the first poll.
type statusVocabulary struct {
	ready  []string
	failed []string
}

// classify decides what a reported status means.
//
// An empty status is treated as ready. A service that reports no state at all
// cannot be waited on for one, and holding a resource open for the full timeout
// over a describe that never mentions a state would fail deploys that work —
// the status field is the whole basis of the wait, so its absence has to mean
// "this resource is not asynchronous" rather than "this resource is not done".
func (v statusVocabulary) classify(status string) readinessOutcome {
	folded := strings.ToLower(strings.TrimSpace(status))
	if folded == "" {
		return readinessReady
	}
	for _, s := range v.ready {
		if folded == strings.ToLower(s) {
			return readinessReady
		}
	}
	for _, s := range v.failed {
		if folded == strings.ToLower(s) {
			return readinessFailed
		}
	}
	return readinessWaiting
}

// stabilizeWait is one resource type's wait. subject names the resource in
// every message the wait produces ("MSK cluster app-msk") and goal says what it
// was waiting for ("become ACTIVE") — together they are what makes a failure
// actionable, because "timed out" on its own tells an operator nothing about
// which of a stack's resources stalled or how far it got.
//
// describe returns the resource's current status and, where the service records
// one, its own account of why the resource is in that status: an MSK
// stateInfo.message, an EKS health issue, a Lambda StateReason. A resource that
// has ceased to exist is an error from describe, not a status — it is not going
// to become ready, and waiting out the budget for one only delays saying so.
type stabilizeWait struct {
	subject  string
	goal     string
	timeout  time.Duration
	statuses statusVocabulary
	describe func(ctx context.Context) (status string, reason string, err error)
}

// awaitResourceReady polls until the resource is ready, reaches a status its
// service documents as terminal, or runs out of budget.
//
// The clock is injected rather than read off time.Now(): a timeout measured
// against the wall clock cannot be exercised by a test without spending the
// timeout, and a repo-wide rule exists so that no wait has to.
func awaitResourceReady(ctx context.Context, clk clock.Clock, w stabilizeWait) error {
	deadline := clk.Now().Add(w.timeout)
	for {
		status, reason, err := w.describe(ctx)
		if err != nil {
			return err
		}
		switch w.statuses.classify(status) {
		case readinessReady:
			return nil
		case readinessFailed:
			return stabilizeFailure(w, status, reason)
		case readinessWaiting:
			// Still working — fall through to the deadline check and poll again.
		}
		if clk.Now().After(deadline) {
			return stabilizeTimeout(w, status, reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-clk.After(resourceStabilizePollInterval):
		}
	}
}

// stabilizeFailure words the end of a wait that the service itself ended. The
// status alone is rarely enough — "FAILED" is the same word for an image that
// would not pull and a broker that would not start — so the service's own
// reason leads when there is one, with the status behind it for the cases where
// there is not.
func stabilizeFailure(w stabilizeWait, status, reason string) error {
	if reason != "" {
		return fmt.Errorf("%s reached %q while waiting for it to %s: %s", w.subject, status, w.goal, reason)
	}
	return fmt.Errorf("%s reached %q while waiting for it to %s", w.subject, status, w.goal)
}

// stabilizeTimeout words the end of a wait that ran out of budget. It says
// where the resource got to and what it was being waited for, because a bare
// "timed out" is what an operator cannot act on: the whole point of the message
// is to distinguish a cluster still pulling an image from one wedged in a
// status that will never change.
func stabilizeTimeout(w stabilizeWait, status, reason string) error {
	if reason != "" {
		return fmt.Errorf("%s did not %s within %s (last status %q: %s)",
			w.subject, w.goal, w.timeout, status, reason)
	}
	return fmt.Errorf("%s did not %s within %s (last status %q)",
		w.subject, w.goal, w.timeout, status)
}
