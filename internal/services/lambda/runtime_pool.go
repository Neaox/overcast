package lambda

// runtime_pool.go — InstancePool manages the warm execution environments for
// every function.
//
// AWS Lambda reuses execution environments (containers) for sequential
// invocations and scales out to one environment per concurrent invocation.
// InstancePool models both: each function has a *warm set* of idle instances,
// and concurrent invocations each take their own.
//
// Capacity model:
//   - Concurrent invocations of one function each get their own instance;
//     whatever the warm set cannot supply is cold started.
//   - After a burst, surplus instances stay warm (up to maxWarm) and are
//     reaped by the sweeper once idle for poolIdleTTL, rather than being
//     destroyed the moment their invocation ends.
//   - Provisioned concurrency keeps a target number of instances alive: they
//     are created eagerly, exempt from the idle sweep, and replenished when
//     lost. See SetProvisionedConcurrency.
//   - Instances whose identity no longer matches the function (see
//     functionInstanceIdentity) are retired: immediately by InvalidateFunction
//     when idle, on Release when an invocation is in flight, and as a backstop
//     on the next Acquire.
//
// Retiring an on-demand instance never starts a replacement — the next
// invocation cold starts one. Retiring a provisioned instance does, because
// that is what provisioned concurrency means.
//
// Thread safety: all public methods are safe for concurrent use.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/logging"
)

const (
	// poolSweepInterval is how often the sweeper goroutine wakes up.
	poolSweepInterval = 30 * time.Second
	// poolIdleTTL is how long an instance may sit idle before being evicted.
	poolIdleTTL = 15 * time.Minute
	// defaultMaxWarmInstances bounds the warm set per function when no explicit
	// cap is configured. A burst of concurrent invocations would otherwise leave
	// one container per invocation holding memory on a dev machine until the
	// idle TTL expired.
	defaultMaxWarmInstances = 10
)

// InstanceObserver is told about execution environments the pool creates or
// destroys outside an invocation, so a tracker of them can stay in step.
// Implemented by instanceTracker.
type InstanceObserver interface {
	// InstanceWarmed reports an environment created without an invocation —
	// today, only provisioned concurrency pre-warming.
	InstanceWarmed(functionName, instanceID string, provisioned bool)
	// InstanceLost reports an environment that no longer exists, for any
	// reason: idle sweep, reclaimed for capacity, retired after an update,
	// died in Docker, or its function was deleted.
	InstanceLost(functionName, instanceID string)
}

// poolEntry holds one idle RuntimeInstance.
type poolEntry struct {
	inst     RuntimeInstance
	identity string    // functionInstanceIdentity when the instance was created
	lastUsed time.Time // updated on every Release
}

// provisionedState is the provisioned-concurrency target for one function.
type provisionedState struct {
	target int
	fn     *Function // snapshot used to cold start replacements
}

// InstancePool manages warm RuntimeInstances for every function.
type InstancePool struct {
	mu sync.Mutex
	// entries is the warm (idle) set per function name. Instances handed to a
	// caller are removed and only reappear on Release.
	entries map[string][]*poolEntry
	// checkedOut counts instances currently serving an invocation, per
	// function. entries + checkedOut is how many environments exist, which is
	// what provisioned-concurrency replenishment is measured against.
	checkedOut map[string]int
	// expected records the identity each function is currently expected to run
	// with, so Release can retire an instance that was checked out before a
	// code or configuration update landed.
	expected map[string]string
	// provisioned holds the provisioned-concurrency target per function.
	provisioned map[string]*provisionedState
	// provisionedInstances records which execution environments hold each
	// function's reservation, keyed by function name then instance ID. Tracking
	// the identities — rather than counting environments — is what keeps
	// provisioned concurrency a floor: on-demand instances created by a burst
	// never count towards the target, and the environments that are exempt from
	// the idle sweep are exactly the ones the UI shows as provisioned.
	provisionedInstances map[string]map[string]bool
	// provisionedPending counts environments a replenish goroutine is currently
	// starting, so concurrent calls do not each create the same shortfall.
	provisionedPending map[string]int

	maxWarm        int
	maxInstances   int
	maxPerFunction int
	// slotFreed is closed and replaced whenever capacity frees up, waking every
	// invocation queued behind an instance limit. See signalSlotFreed.
	slotFreed chan struct{}

	rt     Runtime // used for cold starts
	log    *zap.Logger
	clk    clock.Clock
	stopCh chan struct{}
	// observer, when set, is told about environments the pool creates or
	// destroys outside an invocation, so the instance tracker never reports an
	// environment that no longer exists. Set once during wiring, before the
	// pool is used.
	observer InstanceObserver
	// warmWG tracks background provisioning goroutines so Stop can wait.
	warmWG sync.WaitGroup
}

// PoolLimits bounds how many containers the pool will keep and run. Zero
// fields take their defaults.
type PoolLimits struct {
	// MaxWarmPerFunction bounds the idle set for one function.
	MaxWarmPerFunction int
	// MaxInstances bounds containers across all functions.
	MaxInstances int
	// MaxInstancesPerFunction bounds concurrent containers for one function.
	MaxInstancesPerFunction int
}

func (l PoolLimits) withDefaults() PoolLimits {
	if l.MaxWarmPerFunction < 1 {
		l.MaxWarmPerFunction = defaultMaxWarmInstances
	}
	if l.MaxInstances < 1 {
		l.MaxInstances = defaultMaxInstances
	}
	if l.MaxInstancesPerFunction < 1 {
		l.MaxInstancesPerFunction = defaultMaxInstancesPerFunction
	}
	// A per-function limit above the global one can never be reached; clamping
	// keeps the two consistent so error messages name the limit that binds.
	if l.MaxInstancesPerFunction > l.MaxInstances {
		l.MaxInstancesPerFunction = l.MaxInstances
	}
	return l
}

// NewInstancePool creates an InstancePool backed by rt and starts the
// background sweeper. Call Stop() to shut it down.
func NewInstancePool(rt Runtime, log *zap.Logger, clk clock.Clock, limits PoolLimits) *InstancePool {
	limits = limits.withDefaults()
	p := &InstancePool{
		entries:              make(map[string][]*poolEntry),
		checkedOut:           make(map[string]int),
		expected:             make(map[string]string),
		provisioned:          make(map[string]*provisionedState),
		provisionedInstances: make(map[string]map[string]bool),
		provisionedPending:   make(map[string]int),
		maxWarm:              limits.MaxWarmPerFunction,
		maxInstances:         limits.MaxInstances,
		maxPerFunction:       limits.MaxInstancesPerFunction,
		slotFreed:            make(chan struct{}),
		rt:                   rt,
		log:                  log,
		clk:                  clk,
		stopCh:               make(chan struct{}),
	}
	go p.sweepLoop()
	return p
}

// functionInstanceIdentity fingerprints everything that is baked into an
// execution environment when its container is created: the deployment package
// (or image), plus the configuration that becomes container environment
// variables, resource limits, entrypoint, injected layers and attached
// networks.
//
// A container started with the old values can never observe the new ones — the
// process was exec'd with the old environment — so the pool compares this, not
// the code hash alone, when deciding whether a warm instance is still usable.
//
// Fields that never reach the container (Description, Role, RevisionId, tags
// other than the hot-reload path) are deliberately excluded: including them
// would force a pointless cold start on a cosmetic edit.
func functionInstanceIdentity(fn *Function) string {
	if fn == nil {
		return ""
	}
	h := sha256.New()
	// Length-prefix every field so no value can forge the encoding of another
	// (e.g. an env value containing "\nenv:OTHER=").
	add := func(field, value string) {
		_, _ = fmt.Fprintf(h, "%s=%d:%s\n", field, len(value), value)
	}

	add("code", functionCodeIdentity(fn))
	add("runtime", fn.Runtime)
	add("handler", fn.Handler)
	add("timeout", strconv.Itoa(fn.Timeout))
	add("memory", strconv.Itoa(fn.MemorySize))
	add("arch", strings.Join(fn.Architectures, ","))
	add("loggroup", fn.logGroupName())

	keys := make([]string, 0, len(fn.Environment))
	for k := range fn.Environment {
		keys = append(keys, k)
	}
	slices.Sort(keys) // map iteration order must not leak into the fingerprint
	for _, k := range keys {
		add("env:"+k, fn.Environment[k])
	}

	for _, layer := range fn.Layers {
		add("layer", layer.ARN)
	}
	if fn.VpcConfig != nil {
		add("vpc", fn.VpcConfig.VpcId)
		add("subnets", strings.Join(fn.VpcConfig.SubnetIds, ","))
		add("security-groups", strings.Join(fn.VpcConfig.SecurityGroupIds, ","))
	}
	if fn.ImageConfig != nil {
		add("entrypoint", strings.Join(fn.ImageConfig.EntryPoint, "\x00"))
		add("command", strings.Join(fn.ImageConfig.Command, "\x00"))
		add("workdir", fn.ImageConfig.WorkingDirectory)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ─── Acquire / Release ───────────────────────────────────────────────────────

// takeWarm removes and returns an idle instance for fn, or nil when the caller
// must cold start. Pooled instances whose identity no longer matches fn (its
// code or configuration changed) are closed here.
//
// It also records fn's current identity as the one the pool expects, so that
// Release can retire an instance checked out under an older configuration.
// The caller must already hold a concurrency slot from admit.
func (p *InstancePool) takeWarm(fn *Function) RuntimeInstance {
	identity := functionInstanceIdentity(fn)

	p.mu.Lock()
	if p.entries == nil { // pool stopped
		p.mu.Unlock()
		return nil
	}
	p.expected[fn.Name] = identity

	var stale []RuntimeInstance
	var chosen RuntimeInstance
	warm := p.entries[fn.Name]
	for i := 0; i < len(warm); i++ {
		if warm[i].identity != identity {
			// Backstop for updates that did not go through InvalidateFunction.
			stale = append(stale, warm[i].inst)
			continue
		}
		if chosen == nil {
			chosen = warm[i].inst
		}
	}
	if len(stale) > 0 || chosen != nil {
		kept := warm[:0]
		for _, e := range warm {
			if e.identity != identity || e.inst == chosen {
				continue
			}
			kept = append(kept, e)
		}
		p.setWarmLocked(fn.Name, kept)
	}
	p.mu.Unlock()

	for _, inst := range stale {
		p.log.Debug("lambda pool: evicting stale instance", zap.String("function", fn.Name))
		p.closeInstance(fn.Name, inst, "close stale instance failed")
	}
	if chosen != nil {
		p.log.Debug("lambda pool: warm start", zap.String("function", fn.Name))
	}
	return chosen
}

// setWarmLocked stores the warm set for name, deleting the key when empty so
// the map does not grow one entry per function that has ever run.
// Caller must hold p.mu.
func (p *InstancePool) setWarmLocked(name string, warm []*poolEntry) {
	if len(warm) == 0 {
		delete(p.entries, name)
		return
	}
	p.entries[name] = warm
}

// Acquire returns an instance for fn — reused from the warm set when one
// matches, cold started otherwise. Concurrent callers never share an instance.
//
// It may block while the invocation queues behind an instance limit, and
// returns a TooManyRequestsException (see ThrottleError) when a concurrency
// limit refuses the invocation outright or ctx expires while queued.
func (p *InstancePool) Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	return p.acquire(ctx, fn, nil)
}

// AcquireWithProgress is like Acquire but reports lifecycle steps via progress.
func (p *InstancePool) AcquireWithProgress(ctx context.Context, fn *Function, progress ProgressFunc) (RuntimeInstance, error) {
	return p.acquire(ctx, fn, progress)
}

func (p *InstancePool) acquire(ctx context.Context, fn *Function, progress ProgressFunc) (RuntimeInstance, error) {
	// Bound how long an invocation may queue for capacity by the function's own
	// timeout. Without this the async and event-source paths — which hand us a
	// cancellable context with no deadline — would block indefinitely instead
	// of eventually throttling.
	admitCtx, cancelAdmit := context.WithTimeout(ctx, queueBudget(fn))
	defer cancelAdmit()

	if err := p.admit(admitCtx, fn); err != nil {
		return nil, err
	}
	if inst := p.takeWarm(fn); inst != nil {
		return inst, nil
	}
	if err := p.admitContainer(admitCtx, fn); err != nil {
		p.releaseSlot(fn.Name)
		return nil, err
	}

	p.log.Debug("lambda pool: cold start", zap.String("function", fn.Name))
	var inst RuntimeInstance
	var err error
	if cr, ok := p.rt.(*ContainerRuntime); ok && progress != nil {
		inst, err = cr.AcquireWithProgress(ctx, fn, progress)
	} else {
		inst, err = p.rt.Acquire(ctx, fn)
	}
	if err != nil || inst == nil {
		// A runtime that returns no instance and no error would otherwise leak
		// the admitted slot, permanently shrinking the function's concurrency.
		p.releaseSlot(fn.Name)
		if err == nil {
			err = fmt.Errorf("lambda runtime returned no instance for %q", fn.Name)
		}
		return nil, err
	}
	return inst, nil
}

// releaseSlot gives back a concurrency slot taken by admit without an instance
// to show for it (the cold start failed, or capacity ran out).
func (p *InstancePool) releaseSlot(name string) {
	p.mu.Lock()
	p.decCheckedOutLocked(name)
	p.signalSlotFreedLocked()
	p.mu.Unlock()
}

// decCheckedOutLocked drops one in-flight slot for name. Caller must hold p.mu.
func (p *InstancePool) decCheckedOutLocked(name string) {
	if p.checkedOut == nil {
		return
	}
	p.checkedOut[name]--
	if p.checkedOut[name] <= 0 {
		delete(p.checkedOut, name)
	}
}

// releaseDisposition is what Release decided to do with an instance.
type releaseDisposition int

const (
	pooled releaseDisposition = iota
	closeUnhealthy
	closeAfterStop
	closeRetired
	closeSurplus
)

// releaseLog maps a disposition to its log line.
var releaseLog = map[releaseDisposition]string{
	closeUnhealthy: "lambda pool: discarding unhealthy instance",
	closeAfterStop: "lambda pool: closing instance released after stop",
	closeRetired:   "lambda pool: retiring instance after configuration change",
	closeSurplus:   "lambda pool: warm set full — closing surplus instance",
}

// Release returns inst to the warm set after an invocation. The instance is
// destroyed instead when it is unhealthy, when the function was updated while
// the invocation was in flight, or when the warm set is already full.
// Implements the Runtime interface — inst.FunctionName() is the pool key.
func (p *InstancePool) Release(_ context.Context, inst RuntimeInstance, healthy bool) {
	name := inst.FunctionName()
	usable := healthy && inst.Healthy()

	// The decision and the append happen under one lock hold: splitting them
	// would let two concurrent releases both observe room for one more and
	// push the warm set past its cap.
	p.mu.Lock()
	p.decCheckedOutLocked(name)
	// Whatever is decided below, this invocation's slot is now free: either the
	// instance goes back to the warm set for the next caller, or it is closed
	// and the next caller cold starts into the space it leaves.
	p.signalSlotFreedLocked()

	disposition := pooled
	// expected is empty for a function the pool has never admitted, in which
	// case there is nothing to compare against; an identity is always a hash.
	switch want := p.expected[name]; {
	case !usable:
		disposition = closeUnhealthy
	case p.entries == nil:
		disposition = closeAfterStop
	// The function's code or configuration changed while this invocation was in
	// flight. AWS lets the in-flight invocation finish in the old environment
	// and then retires it, so destroy the instance now rather than pooling one
	// that can never see the new configuration.
	case want != "" && want != inst.ConfigIdentity():
		disposition = closeRetired
	case len(p.entries[name]) >= p.warmCapacityLocked(name):
		disposition = closeSurplus
	default:
		p.entries[name] = append(p.entries[name], &poolEntry{
			inst:     inst,
			identity: inst.ConfigIdentity(),
			lastUsed: p.clk.Now(),
		})
	}
	p.mu.Unlock()

	if disposition == pooled {
		p.log.Debug("lambda pool: instance returned to pool", zap.String("function", name))
		return
	}
	p.log.Debug(releaseLog[disposition], zap.String("function", name))
	p.closeInstance(name, inst, "close on release failed")
	// An environment lost while the reservation still stands must be rebuilt;
	// one closed because the pool is stopping or already full must not be.
	if disposition == closeUnhealthy || disposition == closeRetired {
		p.replenishProvisioned(name)
	}
}

// warmCapacityLocked is how many idle instances name may hold. Provisioned
// concurrency raises the cap when its target exceeds maxWarm, so a configured
// allocation is never fought by the warm-set limit.
// Caller must hold p.mu.
func (p *InstancePool) warmCapacityLocked(name string) int {
	capacity := p.maxWarm
	if st, ok := p.provisioned[name]; ok && st.target > capacity {
		capacity = st.target
	}
	return capacity
}

// closeInstance destroys inst and tells the observer the environment is gone.
// Every path that gets rid of a container goes through here so the tracker and
// the pool can never disagree about what exists.
func (p *InstancePool) closeInstance(name string, inst RuntimeInstance, msg string) {
	p.mu.Lock()
	if ids := p.provisionedInstances[name]; ids != nil {
		delete(ids, inst.InstanceID())
		if len(ids) == 0 {
			delete(p.provisionedInstances, name)
		}
	}
	p.mu.Unlock()
	if err := inst.Close(); err != nil {
		p.log.Warn("lambda pool: "+msg,
			zap.String("function", name),
			zap.Error(err),
		)
	}
	if p.observer != nil {
		p.observer.InstanceLost(name, inst.InstanceID())
	}
}

// CanHandle delegates to the underlying runtime.
func (p *InstancePool) CanHandle(runtimeID string) bool {
	return p.rt.CanHandle(runtimeID)
}

// ─── Eviction and retirement ─────────────────────────────────────────────────

// EvictFunction closes and removes every warm instance for the named function
// and forgets its provisioned-concurrency target. Called by DeleteFunction so
// containers do not linger after deletion.
func (p *InstancePool) EvictFunction(name string) {
	p.mu.Lock()
	warm := p.entries[name]
	delete(p.entries, name)
	delete(p.expected, name)
	delete(p.provisioned, name)
	delete(p.provisionedInstances, name)
	p.mu.Unlock()

	for _, entry := range warm {
		p.closeInstance(name, entry.inst, "evict close failed")
	}
}

// InvalidateFunction retires the execution environments for fn after its code
// or configuration changed.
//
//   - Idle instances: closed immediately, so stale containers do not linger
//     until the 15-minute idle sweep.
//   - Instances serving an invocation: left running. AWS never interrupts an
//     in-flight invocation to apply an update, so each finishes in the old
//     environment and Release then destroys it instead of pooling it.
//   - Identity unchanged (e.g. only the description was edited): the warm set
//     is kept.
//
// No on-demand replacement is started — the next invocation cold starts one.
// A function with provisioned concurrency is re-provisioned in the background
// against the new configuration, which is the point of having reserved it.
//
// Returns the number of idle instances that were closed.
func (p *InstancePool) InvalidateFunction(fn *Function) int {
	if fn == nil {
		return 0
	}
	name := fn.Name
	identity := functionInstanceIdentity(fn)

	p.mu.Lock()
	if p.entries == nil { // pool stopped
		p.mu.Unlock()
		return 0
	}
	p.expected[name] = identity
	warm := p.entries[name]
	var retire []RuntimeInstance
	kept := warm[:0]
	for _, entry := range warm {
		if entry.identity == identity {
			kept = append(kept, entry)
			continue
		}
		retire = append(retire, entry.inst)
	}
	p.setWarmLocked(name, kept)
	// Rebuild the reservation from the function as it is now, not the snapshot
	// taken when provisioned concurrency was first configured.
	reprovision := false
	if st, ok := p.provisioned[name]; ok {
		snapshot := *fn
		st.fn = &snapshot
		reprovision = true
	}
	p.mu.Unlock()

	for _, inst := range retire {
		p.log.Debug("lambda pool: retiring idle instance after configuration change",
			zap.String("function", name))
		p.closeInstance(name, inst, "close retired instance failed")
	}
	if reprovision {
		p.replenishProvisioned(name)
	}
	return len(retire)
}

// handleContainerDied drops warm instances whose Docker container has died —
// removed by hand (`docker rm -f`), OOM-killed, or lost to a Docker restart.
//
// Without this, the pool keeps treating the dead container as warm: an
// invocation is handed the corpse, submits the event to a Runtime API that no
// container is polling, and blocks until the function's configured timeout
// expires before failing. Dropping the entry here makes that a cold start.
//
// The exitNotifier only routes die events to invocations that are in flight, so
// this handler is what covers a container that dies while idle in the pool.
// Subscribe it to events.DockerContainerDied.
func (p *InstancePool) handleContainerDied(_ context.Context, e events.Event) {
	payload, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || payload.Service != "lambda" || payload.ContainerID == "" {
		return
	}

	p.mu.Lock()
	if p.entries == nil { // pool stopped
		p.mu.Unlock()
		return
	}
	var name string
	var dead RuntimeInstance
	for fnName, warm := range p.entries {
		for i, entry := range warm {
			if entry.inst.ContainerID() != payload.ContainerID {
				continue
			}
			name, dead = fnName, entry.inst
			p.setWarmLocked(fnName, append(warm[:i:i], warm[i+1:]...))
			break
		}
		if dead != nil {
			break
		}
	}
	p.mu.Unlock()

	if dead == nil {
		return // not warm: in flight (the exitNotifier has it) or already gone
	}
	p.log.Info("lambda pool: warm instance container died — next invoke will cold start",
		zap.String("function", name),
		zap.String("container", shortContainerID(payload.ContainerID)),
	)
	p.closeInstance(name, dead, "close dead instance failed")
	p.replenishProvisioned(name)
}

// ─── Provisioned concurrency ─────────────────────────────────────────────────

// SetProvisionedConcurrency reserves target execution environments for fn.
// They are created in the background, exempt from the idle sweep, and
// replenished whenever one is lost. target <= 0 clears the reservation,
// after which the environments become ordinary warm instances subject to the
// idle TTL — matching AWS, which does not tear them down instantly.
func (p *InstancePool) SetProvisionedConcurrency(fn *Function, target int) {
	if fn == nil {
		return
	}
	if target <= 0 {
		p.ClearProvisionedConcurrency(fn.Name)
		return
	}
	snapshot := *fn
	p.mu.Lock()
	if p.entries == nil {
		p.mu.Unlock()
		return
	}
	p.provisioned[fn.Name] = &provisionedState{target: target, fn: &snapshot}
	p.mu.Unlock()

	p.replenishProvisioned(fn.Name)
}

// ClearProvisionedConcurrency removes the reservation for name. The
// environments it was holding open are not torn down — they lose their
// exemption from the idle sweep and age out like any other warm instance,
// matching AWS, which does not kill them the moment the config is deleted.
func (p *InstancePool) ClearProvisionedConcurrency(name string) {
	p.mu.Lock()
	delete(p.provisioned, name)
	delete(p.provisionedInstances, name)
	p.mu.Unlock()
}

// ProvisionedStatus reports the reservation for name: how many environments are
// requested, how many exist (warm or serving an invocation), and how many are
// idle and immediately available. ok is false when name has no reservation.
func (p *InstancePool) ProvisionedStatus(name string) (requested, allocated, available int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.provisioned[name]
	if !ok {
		return 0, 0, 0, false
	}
	reserved := p.provisionedInstances[name]
	allocated = len(reserved)
	for _, entry := range p.entries[name] {
		if reserved[entry.inst.InstanceID()] {
			available++
		}
	}
	return st.target, allocated, available, true
}

// markProvisioned records that instanceID holds part of name's reservation.
func (p *InstancePool) markProvisioned(name, instanceID string) {
	if instanceID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provisionedInstances == nil {
		return
	}
	if p.provisionedInstances[name] == nil {
		p.provisionedInstances[name] = make(map[string]bool)
	}
	p.provisionedInstances[name][instanceID] = true
}

// isProvisionedLocked reports whether inst holds part of name's reservation.
// Caller must hold p.mu.
func (p *InstancePool) isProvisionedLocked(name string, inst RuntimeInstance) bool {
	return p.provisionedInstances[name][inst.InstanceID()]
}

// clearProvisionedPending releases the in-flight reservation starts a
// replenish goroutine claimed.
func (p *InstancePool) clearProvisionedPending(name string, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provisionedPending == nil {
		return
	}
	p.provisionedPending[name] -= n
	if p.provisionedPending[name] <= 0 {
		delete(p.provisionedPending, name)
	}
}

// provisionedRuntime is implemented by runtimes that can mark an environment
// as pre-warmed for provisioned concurrency, so the container reports the AWS
// initialization type applications actually see.
type provisionedRuntime interface {
	AcquireProvisioned(ctx context.Context, fn *Function) (RuntimeInstance, error)
}

func (p *InstancePool) acquireProvisioned(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	if pr, ok := p.rt.(provisionedRuntime); ok {
		return pr.AcquireProvisioned(ctx, fn)
	}
	return p.rt.Acquire(ctx, fn)
}

// replenishProvisioned tops the function back up to its provisioned target,
// cold starting the shortfall on a background goroutine so callers (Release, a
// died-container event, a configuration update) are never blocked on Docker.
func (p *InstancePool) replenishProvisioned(name string) {
	p.mu.Lock()
	st, ok := p.provisioned[name]
	if !ok || p.entries == nil || st.fn == nil {
		p.mu.Unlock()
		return
	}
	// Only environments that hold the reservation count. Provisioned
	// concurrency is a floor, not a ceiling: a burst of on-demand instances
	// must not make the pool think the reservation is already satisfied.
	shortfall := st.target - len(p.provisionedInstances[name]) - p.provisionedPending[name]
	if shortfall <= 0 {
		p.mu.Unlock()
		return
	}
	// Record the pending starts so concurrent replenish calls do not each
	// decide to create the same shortfall.
	p.provisionedPending[name] += shortfall
	fn := *st.fn
	p.mu.Unlock()

	p.warmWG.Add(1)
	go func() {
		defer p.warmWG.Done()
		defer p.clearProvisionedPending(fn.Name, shortfall)
		for range shortfall {
			select {
			case <-p.stopCh:
				continue
			default:
			}
			// Provisioned capacity is reserved, so it does not queue behind
			// the emulator's instance limits the way an on-demand invocation
			// does — the reservation was accepted up front.
			inst, err := p.acquireProvisioned(context.Background(), &fn)
			if err != nil {
				p.log.Warn("lambda pool: provisioned concurrency cold start failed",
					zap.String("function", fn.Name),
					zap.Error(err),
				)
				continue
			}
			// Mark it as holding the reservation before pooling it, so the
			// sweeper and the capacity reclaimer see it as protected the
			// instant it becomes visible.
			p.markProvisioned(fn.Name, inst.InstanceID())
			if p.observer != nil {
				// Announce before pooling: if Release decides to close this
				// instance instead, closeInstance's InstanceLost retracts it.
				p.observer.InstanceWarmed(fn.Name, inst.InstanceID(), true)
			}
			// Release decrements the in-flight count, so borrow a slot first to
			// keep the books level — this environment was never checked out.
			p.mu.Lock()
			if p.checkedOut != nil {
				p.checkedOut[fn.Name]++
			}
			p.mu.Unlock()
			p.Release(context.Background(), inst, true)
			p.log.Debug("lambda pool: provisioned environment ready",
				zap.String("function", fn.Name))
		}
	}()
}

// ─── Lifecycle ───────────────────────────────────────────────────────────────

// Stop shuts down the background sweeper and closes every warm instance.
func (p *InstancePool) Stop() {
	close(p.stopCh)
	p.warmWG.Wait()

	// Cancel log contexts on all remaining instances so log streaming
	// goroutines exit cleanly and don't fight with GC during shutdown.
	p.mu.Lock()
	entries := p.entries
	p.entries = nil
	p.expected = nil
	p.provisioned = nil
	p.provisionedInstances = nil
	p.provisionedPending = nil
	p.checkedOut = nil
	// Wake anything queued behind an instance limit; it will see the stopped
	// pool and give up rather than wait out its whole timeout.
	p.signalSlotFreedLocked()
	p.mu.Unlock()
	for name, warm := range entries {
		for _, entry := range warm {
			p.closeInstance(name, entry.inst, "stop close failed")
		}
	}
}

// sweepLoop runs until Stop() is called, evicting instances idle longer than
// poolIdleTTL.
func (p *InstancePool) sweepLoop() {
	ticker := p.clk.Ticker(poolSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.sweep()
		case <-p.stopCh:
			return
		}
	}
}

func (p *InstancePool) sweep() {
	cutoff := p.clk.Now().Add(-poolIdleTTL)

	type staleInstance struct {
		name string
		inst RuntimeInstance
	}
	var stale []staleInstance

	p.mu.Lock()
	for name, warm := range p.entries {
		kept := warm[:0]
		for _, entry := range warm {
			// Environments holding a provisioned reservation are exempt from
			// the idle sweep regardless of how long they have been idle — that
			// is what the reservation buys. Spillover instances created by a
			// burst age out normally.
			if p.isProvisionedLocked(name, entry.inst) || !entry.lastUsed.Before(cutoff) {
				kept = append(kept, entry)
				continue
			}
			stale = append(stale, staleInstance{name, entry.inst})
		}
		p.setWarmLocked(name, kept)
	}
	p.mu.Unlock()

	for _, s := range stale {
		// A per-tick sweep-cycle outcome (fires on the sweeper's own
		// schedule, not because of a specific invoke) — TRACE per the
		// trace-vs-debug policy (CONTRIBUTING.md § Log levels).
		p.log.Log(logging.TraceLevel, "lambda pool: evicting idle instance", zap.String("function", s.name))
		p.closeInstance(s.name, s.inst, "sweep close failed")
	}
}
