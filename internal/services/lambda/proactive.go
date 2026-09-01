package lambda

// proactive.go — proactive initialization (Phase 3 of
// docs/plans/lambda-cold-start.md).
//
// AWS documents that Lambda "may proactively initialize execution
// environments" ahead of traffic; measurements attribute the majority of
// inits for API-Gateway-attached functions to it. Overcast mirrors the
// behavior: once a function's configuration has settled for settleDelay
// (CDK deploys issue Create plus several updates back to back — the debounce
// is what keeps that churn from building and destroying containers per call),
// one execution environment is created in the background so the next request
// lands warm.
//
// Fidelity: the environment reports AWS_LAMBDA_INITIALIZATION_TYPE=on-demand
// and its first REPORT line carries no Init Duration — both exactly what AWS
// proactive initialization looks like from inside and outside the function.
//
// Candidacy (all required):
//   - LAMBDA_PROACTIVE_INIT is enabled (default true since issue #1099's
//     flip; LAMBDA_PROACTIVE_INIT=false opts back out);
//   - the function is Active;
//   - there is evidence it will be invoked: it was invoked at least once this
//     process, or it has a function URL or event source mapping. (Detecting
//     API Gateway / AppSync wiring lives in other services' state and is a
//     planned follow-up before the default flips on.)
//
// Resource discipline: creation try-acquires the same admission budgets as a
// real invocation and simply reschedules when anything is contended — it can
// never queue ahead of real work (see InstancePool.ProactiveInit).

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/middleware"
)

const (
	// proactiveSettleDelay is how long a function must go without an
	// identity-changing write before an environment is created for it.
	proactiveSettleDelay = 10 * time.Second
	// proactiveRetryDelay is how long to wait before retrying when capacity
	// was contended.
	proactiveRetryDelay = 15 * time.Second
)

// pendingInit is one function's scheduled proactive-init attempt.
type pendingInit struct {
	timer *clock.Timer
	// region is the function's home partition (from its ARN), carried so the
	// timer-fired attempt — which runs on a bare background context — reads
	// the same store partition the function was written to.
	region string
}

// proactiveIniter debounces function-change signals and asks the pool for one
// proactive environment per settled candidate function. All methods are
// nil-receiver-safe so callers need no wiring guards.
type proactiveIniter struct {
	mu      sync.Mutex
	timers  map[string]*pendingInit
	invoked map[string]bool // functions invoked at least once this process
	stopped bool

	pool func() *InstancePool
	ls   *lambdaStore
	// wired reports trigger evidence beyond in-process invocations.
	wired func(ctx context.Context, name string) bool
	// prefill, when set, pre-builds cold-start artifacts for a settled
	// function. Runs regardless of createEnvs — a warmed tar cache costs a
	// bounded slice of memory and needs no trigger evidence.
	prefill func(ctx context.Context, fn *Function)
	// createEnvs gates actual environment creation (LAMBDA_PROACTIVE_INIT).
	// The initer itself always runs, driving the artifact pre-fill.
	createEnvs bool
	log        *zap.Logger
	clk        clock.Clock
}

func newProactiveIniter(pool func() *InstancePool, ls *lambdaStore, wired func(ctx context.Context, name string) bool, log *zap.Logger, clk clock.Clock) *proactiveIniter {
	return &proactiveIniter{
		timers:  make(map[string]*pendingInit),
		invoked: make(map[string]bool),
		pool:    pool,
		ls:      ls,
		wired:   wired,
		log:     log,
		clk:     clk,
	}
}

// NoteInvoked records that name was invoked this process, making it a
// proactive-init candidate after future configuration changes.
func (pi *proactiveIniter) NoteInvoked(name string) {
	if pi == nil {
		return
	}
	pi.mu.Lock()
	if !pi.stopped {
		pi.invoked[name] = true
	}
	pi.mu.Unlock()
}

// NoteFunctionChanged (re)starts fn's settle timer. Call on every
// identity-changing write; each call pushes the attempt out by settleDelay,
// so deploy churn collapses into a single attempt after the last write.
func (pi *proactiveIniter) NoteFunctionChanged(fn *Function) {
	if fn == nil {
		return
	}
	pi.schedule(fn.Name, regionFromFunctionARN(fn.ARN), proactiveSettleDelay)
}

// NoteFunctionDeleted cancels any pending attempt for name.
func (pi *proactiveIniter) NoteFunctionDeleted(name string) {
	if pi == nil {
		return
	}
	pi.mu.Lock()
	if p, ok := pi.timers[name]; ok {
		p.timer.Stop()
		delete(pi.timers, name)
	}
	delete(pi.invoked, name)
	pi.mu.Unlock()
}

// Stop cancels all pending attempts. In-flight environment creation is owned
// by the pool (warmWG) and shuts down with it.
func (pi *proactiveIniter) Stop() {
	if pi == nil {
		return
	}
	pi.mu.Lock()
	pi.stopped = true
	for name, p := range pi.timers {
		p.timer.Stop()
		delete(pi.timers, name)
	}
	pi.mu.Unlock()
}

func (pi *proactiveIniter) schedule(name, region string, delay time.Duration) {
	if pi == nil {
		return
	}
	pi.mu.Lock()
	defer pi.mu.Unlock()
	if pi.stopped {
		return
	}
	if p, ok := pi.timers[name]; ok {
		p.timer.Stop()
		if region == "" {
			region = p.region
		}
	}
	// The closure carries its own pendingInit so a stale firing — a timer that
	// went off just as a newer schedule replaced it — identifies itself in
	// attempt and stands down instead of cancelling the newer attempt.
	p := &pendingInit{region: region}
	p.timer = pi.clk.AfterFunc(delay, func() { pi.attempt(name, p) })
	pi.timers[name] = p
}

// attempt runs when a function's settle timer fires: re-check everything
// against live state (the function may have changed, been deleted, or lost
// candidacy since scheduling) and ask the pool for one environment. self is
// the pendingInit this firing belongs to — a firing superseded by a newer
// schedule stands down.
func (pi *proactiveIniter) attempt(name string, self *pendingInit) {
	pi.mu.Lock()
	if pi.stopped || pi.timers[name] != self {
		pi.mu.Unlock()
		return
	}
	region := self.region
	delete(pi.timers, name)
	invoked := pi.invoked[name]
	pi.mu.Unlock()

	pool := pi.pool()
	if pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if region != "" {
		ctx = middleware.ContextWithRegion(ctx, region)
	}
	fn, aerr := pi.ls.getFunction(ctx, name)
	if aerr != nil || fn == nil || fn.State != "Active" {
		return
	}
	// Pre-build the cold-start artifacts either way: the tar cache is
	// byte-bounded and needs no trigger evidence, and this is exactly the
	// settled-deploy moment the cache wants to be filled at.
	if pi.prefill != nil {
		pi.prefill(ctx, fn)
	}
	if !pi.createEnvs {
		return
	}
	if !invoked && (pi.wired == nil || !pi.wired(ctx, name)) {
		pi.log.Debug("lambda proactive init: no trigger evidence — skipping",
			zap.String("function", name))
		return
	}

	switch pool.ProactiveInit(fn) {
	case proactiveStarted:
		pi.log.Info("lambda proactive init: environment creation started",
			zap.String("function", name))
	case proactiveBusy:
		// Capacity was contended; try again later rather than ever queueing
		// ahead of real work.
		pi.schedule(name, region, proactiveRetryDelay)
	case proactiveAlreadyWarm, proactiveUnavailable:
	}
}
