// Package readiness owns the policy half of "wait until a container-backed
// resource actually answers, and say so when it never does".
//
// # Why this exists
//
// The same poll was hand-written six times — ElastiCache three times, MSK, RDS
// and EFS once each — with three mutually inconsistent answers to the only
// question that matters, which is what to do when the resource never answers:
//
//   - stay in "creating" forever, so `aws elasticache wait` and `aws kafka wait`
//     spin until they give up with nothing to say;
//   - transition to "available" anyway, so the API reports a working cache with
//     nothing listening behind it (#881);
//   - settle in a terminal failed state carrying the reason, which is what RDS,
//     EFS and EKS — the three with the most careful probes — already did.
//
// The third is the decision. A resource whose readiness probe is exhausted goes
// to its service's real AWS terminal-failure status, carrying why. That is what
// eks/live_runtime.go argued for when it fixed the EKS instance of this shape
// (#892) and asked for it to be fixed once everywhere.
//
// # The boundary: this package owns policy, the service owns evidence
//
// A Kafka broker, a Redis instance and an NFS export are ready at different
// moments and prove it in different ways, so the probe is not shared and must
// not become shared: a generic TCP dial is precisely the bug this package
// exists to stop being repeated. Docker's published port accepts connections as
// soon as the proxy is up, well before the process behind it is serving, so a
// dial that succeeds is evidence of a port forward and nothing else.
//
// What a Watch owns is everything that is not evidence: when the first probe
// runs, how often it runs after that, how long the whole thing gets, and what
// happens to the record at each of the four outcomes a probe can report. What
// the caller supplies is a Probe returning one of those four outcomes, and the
// two transitions — ready and failed — in its own service's vocabulary.
//
// # Timing
//
// Every delay goes through the caller's lifecycle.Scheduler and clock.Clock, so
// a test drives an entire budget's worth of attempts by advancing a mock clock
// rather than by sleeping. No time.Now or time.Sleep appears here.
package readiness

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/lifecycle"
)

// State is what one probe attempt concluded.
type State uint8

const (
	// Waiting means the probe found no answer yet and no reason to give up.
	// The Watch tries again until its budget runs out.
	Waiting State = iota
	// Ready means the process behind the resource answered on its own protocol.
	Ready
	// Failed means the resource will never answer — its container exited, its
	// image is wrong, its endpoint does not exist. There is nothing to wait for,
	// so the Watch settles immediately rather than burning the rest of the
	// budget before saying the same thing.
	Failed
	// Gone means the record is no longer this Watch's to settle: something
	// deleted, stopped or modified it while the probe was in flight. The Watch
	// stops silently — writing either outcome would clobber that decision.
	Gone
)

// Result is one probe attempt's conclusion, and for Failed and Waiting the
// evidence behind it.
type Result struct {
	State State
	// Reason is required for Failed — it becomes the resource's status reason —
	// and optional for Waiting, where it is carried forward so an exhausted
	// budget can report what the last attempt actually saw rather than only
	// that time ran out.
	Reason string
}

// Answered reports that the resource is serving.
func Answered() Result { return Result{State: Ready} }

// Retry reports no answer yet, carrying the attempt's error so an exhausted
// budget can name it. A nil err is fine — some probes have nothing to add.
func Retry(err error) Result {
	if err == nil {
		return Result{State: Waiting}
	}
	return Result{State: Waiting, Reason: err.Error()}
}

// Doomed reports that the resource will never answer, and why. The reason is
// written onto the record, so it is addressed to whoever reads the API — not to
// a log reader.
func Doomed(reason string) Result { return Result{State: Failed, Reason: reason} }

// Abandoned reports that the record has moved on and is not this Watch's to
// settle.
func Abandoned() Result { return Result{State: Gone} }

// Policy is the timing a service calibrates for its own engine. It is
// deliberately per-service: a Redis container answers in under a second and a
// first-boot Postgres initialises its data directory for minutes, so a single
// shared budget would be either uselessly long for one or wrong for the other.
type Policy struct {
	// FirstDelay is how long to wait before the first probe — time for the
	// process to get as far as binding its port, so the common case does not
	// spend an attempt on a certain failure.
	FirstDelay time.Duration
	// Interval is the delay between attempts.
	Interval time.Duration
	// Budget is the total wall-clock time the resource gets.
	//
	// A deadline rather than an attempt count, following the argument
	// rds/health.go made when it switched: a slow or contended poll silently
	// shortens an attempt count, because the attempts are what is counted and
	// not the time they took. A machine under load is exactly when a database
	// needs its full budget and exactly when a count gives it least.
	Budget time.Duration
}

// Watch polls one resource until its probe answers, its probe gives up, or the
// budget runs out — and settles the record either way. It never leaves a
// resource claiming progress that nothing is coming to finish.
type Watch struct {
	// Scheduler and Clock come from the owning service, so the whole thing is
	// drivable from a test with a mock clock.
	Scheduler *lifecycle.Scheduler
	Clock     clock.Clock

	// Region, ResourceID and Transition are the scheduler's scoping key. Region
	// is the region the record is stored under: the callbacks run outside any
	// request context, so without it a lookup lands on the default region and
	// silently reads nothing.
	Region     string
	ResourceID string
	Transition string

	// Subject names what is being waited on, in the words the failure reason
	// will be read in: "the Redis engine at 127.0.0.1:63790", "the Kafka broker
	// at b-1.demo...:9092". It is the service's evidence, so it belongs to the
	// service, but the sentence around it is this package's.
	Subject string

	Policy Policy

	// Probe is one readiness attempt. It must prove the process is serving its
	// own protocol; see the package comment for why a successful dial does not.
	Probe func(ctx context.Context) Result

	// OnReady and OnFailed move the record to the service's own ready and
	// terminal-failure statuses. OnFailed is given the reason to record.
	OnReady  func(ctx context.Context)
	OnFailed func(ctx context.Context, reason string)
}

// Start schedules the first probe and returns. Everything after that happens on
// the scheduler.
func (w Watch) Start() {
	deadline := w.Clock.Now().Add(w.Policy.Budget)

	// lastReason carries the most recent Waiting result's evidence into the
	// exhaustion message. Written and read only from the scheduler callback,
	// and the next callback is scheduled from inside the previous one, so the
	// scheduler's own lock orders every access.
	var lastReason string

	var attempt func(ctx context.Context)
	attempt = func(ctx context.Context) {
		res := w.Probe(ctx)
		switch res.State {
		case Ready:
			w.OnReady(ctx)
			return
		case Failed:
			w.OnFailed(ctx, res.Reason)
			return
		case Gone:
			return
		case Waiting:
		}

		if res.Reason != "" {
			lastReason = res.Reason
		}
		if w.Clock.Now().Before(deadline) {
			w.Scheduler.AfterScoped(w.Region, w.ResourceID, w.Transition, w.Policy.Interval, attempt)
			return
		}
		w.OnFailed(ctx, w.exhausted(lastReason))
	}

	w.Scheduler.AfterScoped(w.Region, w.ResourceID, w.Transition, w.Policy.FirstDelay, attempt)
}

// exhausted is the reason a resource that ran out of budget carries. It names
// the subject, the budget it was given, and what the last attempt saw — the
// third being the part that usually identifies the problem, and the part a
// caller reading only "timed out" never gets.
func (w Watch) exhausted(last string) string {
	msg := fmt.Sprintf("%s did not become ready within %s", w.Subject, w.Policy.Budget)
	if last != "" {
		msg += ": " + last
	}
	return msg
}
