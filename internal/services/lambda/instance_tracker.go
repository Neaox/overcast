package lambda

// instance_tracker.go — tracks running and idle Lambda execution instances.
//
// One record per execution environment, keyed by the instance's stable ID, so
// a function running five concurrent invocations reports five instances. The
// records back GET /_overcast/lambda/instances and the system map, and drive the
// LambdaInstance* SSE events.
//
// Records are created two ways:
//
//   - By an invocation. Begin opens a record before the runtime is acquired
//     (so a cold start is visible while it happens); Bind then attaches it to
//     the instance that actually served it, merging into the existing record
//     when a warm instance was reused.
//   - By provisioned concurrency, via InstanceWarmed — no invocation involved.
//
// The pool reports destroyed environments through InstanceLost, so a record
// never outlives its container.
//
// The tracker is separate from InstancePool (which owns the actual container
// processes) so that it works even when NodeRuntime is standing in for Docker.

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/logging"
)

// InstanceStatus values for LambdaInstancePayload.Status.
const (
	instanceStatusStarting     = "starting"
	instanceStatusInitializing = "initializing"
	instanceStatusRunning      = "running"
	instanceStatusIdle         = "idle"
	invocationSucceeded        = "succeeded"
	invocationFailed           = "failed"
)

// trackerEntry is the in-memory record for one Lambda execution environment.
type trackerEntry struct {
	instanceID           string
	functionName         string
	status               string
	startedAt            time.Time
	lastUsed             time.Time
	logGroup             string
	logStream            string
	triggerEvent         []byte
	lastInvocationStatus string
	lastInvocationError  string
	// containerID is the Docker container backing this environment; empty for
	// runtimes that are not container-backed, in which case no resource stats
	// are sampled.
	containerID string
	// memUsedMB / cpuPercent are the most recent resource sample (refreshStats).
	// A CPU rate needs two samples, so cpuPercent stays 0 until the second one;
	// the cumulative counters from the previous sample are kept for the delta.
	memUsedMB     int
	cpuPercent    float64
	lastCPUTotal  uint64
	lastSystemCPU uint64
	// provisioned marks an environment held open by a provisioned concurrency
	// reservation. Exempt from the idle sweep.
	provisioned bool
	// retire is set by Invalidate when the function's code or configuration
	// changed while this instance was mid-invocation. Finish evicts the record
	// instead of marking it idle.
	retire bool
}

func (e *trackerEntry) toPayload() events.LambdaInstancePayload {
	expiresAt := e.lastUsed.Add(trackerIdleTTL)
	p := events.LambdaInstancePayload{
		InstanceID:           e.instanceID,
		FunctionName:         e.functionName,
		Status:               e.status,
		StartedAt:            e.startedAt.UnixMilli(),
		LastUsed:             e.lastUsed.UnixMilli(),
		ExpiresAt:            expiresAt.UnixMilli(),
		LogGroup:             e.logGroup,
		LogStream:            e.logStream,
		LastInvocationStatus: e.lastInvocationStatus,
		LastInvocationError:  e.lastInvocationError,
		TriggerEvent:         e.triggerEvent,
		Provisioned:          e.provisioned,
		MemoryUsedMB:         e.memUsedMB,
		CPUPercent:           e.cpuPercent,
	}
	if e.provisioned {
		// A provisioned environment never expires on idleness.
		p.ExpiresAt = 0
	}
	return p
}

// instanceTracker tracks running/idle Lambda instances and publishes lifecycle events.
type instanceTracker struct {
	mu sync.Mutex
	// entries is keyed by instance ID. Invocations whose runtime has not been
	// acquired yet are keyed by a provisional ID until Bind rekeys them.
	entries map[string]*trackerEntry

	// statsFn samples resource usage for a container, wired by SetStatsFunc
	// when the Docker runtime comes up; nil means no stats (NodeRuntime, tests).
	statsFn func(ctx context.Context, containerID string) (docker.ContainerStats, error)
	// lastStatsRefresh throttles refreshStats so several UI clients polling the
	// snapshot endpoint do not multiply Docker stats calls.
	lastStatsRefresh time.Time

	bus      *events.Bus
	clk      clock.Clock
	log      *zap.Logger
	stopOnce sync.Once
	stopCh   chan struct{}
}

// SetStatsFunc wires container resource sampling. Called once during Docker
// runtime wiring, before any UI traffic depends on it; nil-safe receiver.
func (t *instanceTracker) SetStatsFunc(fn func(ctx context.Context, containerID string) (docker.ContainerStats, error)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statsFn = fn
}

func newInstanceTracker(clk clock.Clock, log *zap.Logger) *instanceTracker {
	t := &instanceTracker{
		entries: make(map[string]*trackerEntry),
		clk:     clk,
		log:     log,
		stopCh:  make(chan struct{}),
	}
	go t.sweepLoop()
	return t
}

// SetBus wires the event bus. Called from Service.InitBus after construction.
func (t *instanceTracker) SetBus(b *events.Bus) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bus = b
}

func (t *instanceTracker) publish(typ events.Type, snap events.LambdaInstancePayload) {
	t.mu.Lock()
	bus := t.bus
	t.mu.Unlock()
	if bus == nil {
		return
	}
	bus.Publish(context.Background(), events.Event{
		Type:    typ,
		Time:    t.clk.Now(),
		Source:  "lambda",
		Payload: snap,
	})
}

// ─── Invocation lifecycle ────────────────────────────────────────────────────

// trackedInvocation is the handle for one invocation's tracker record. Every
// method is nil-safe, so callers with no tracker wired do not need to guard
// each call.
type trackedInvocation struct {
	t   *instanceTracker
	key string
	fn  string
	// bound is set by Bind once the record represents a real execution
	// environment. Until then the record is provisional and Abandon discards
	// it rather than leaving a phantom instance behind.
	bound bool
}

// Begin opens a record for a new invocation of functionName and reports it as
// starting. payload is the triggering event body, stored for the UI's
// "Trigger Event" tab. Call Bind once the runtime instance is known.
func (t *instanceTracker) Begin(functionName string, payload []byte) *trackedInvocation {
	if t == nil {
		return nil
	}
	key := uuid.NewString() // provisional until Bind rekeys to the instance
	now := t.clk.Now()
	entry := &trackerEntry{
		instanceID:   uuid.NewString(),
		functionName: functionName,
		startedAt:    now,
		lastUsed:     now,
		status:       instanceStatusStarting,
		triggerEvent: payload,
	}

	t.mu.Lock()
	if t.entries == nil {
		t.mu.Unlock()
		return nil
	}
	t.entries[key] = entry
	snap := entry.toPayload()
	t.mu.Unlock()

	t.publish(events.LambdaInstanceAcquired, snap)
	return &trackedInvocation{t: t, key: key, fn: functionName}
}

// Bind attaches the invocation to the execution environment that will serve it.
// When inst is a warm instance that already has a record, the provisional
// record opened by Begin is dropped and the existing one is reused, so warm
// reuse keeps a stable instance ID rather than inventing a new one each time.
func (i *trackedInvocation) Bind(inst RuntimeInstance) {
	if i == nil || inst == nil {
		return
	}
	id := inst.InstanceID()
	if id == "" || id == i.key {
		return
	}
	t := i.t

	t.mu.Lock()
	if t.entries == nil {
		t.mu.Unlock()
		return
	}
	pending := t.entries[i.key]
	existing, warm := t.entries[id]
	switch {
	case pending == nil:
		t.mu.Unlock()
		return
	case warm:
		// Warm reuse: carry this invocation's trigger event onto the record
		// that already represents the container, and discard the provisional.
		existing.status = instanceStatusRunning
		existing.lastUsed = t.clk.Now()
		existing.containerID = inst.ContainerID()
		existing.lastInvocationStatus = ""
		existing.lastInvocationError = ""
		if len(pending.triggerEvent) > 0 {
			existing.triggerEvent = pending.triggerEvent
		}
		existing.retire = false
		delete(t.entries, i.key)
		i.key, i.bound = id, true
		snap := existing.toPayload()
		t.mu.Unlock()
		t.publish(events.LambdaInstanceAcquired, snap)
		return
	default:
		// Cold start: this container is new, so adopt the provisional record
		// under the instance's own ID.
		delete(t.entries, i.key)
		pending.instanceID = id
		pending.containerID = inst.ContainerID()
		t.entries[id] = pending
		i.key, i.bound = id, true
		t.mu.Unlock()
	}
}

// Ready reports that the container is alive and registered with the Runtime API
// but has not yet run the handler.
func (i *trackedInvocation) Ready() {
	i.transition(instanceStatusInitializing, events.LambdaInstanceReady,
		instanceStatusStarting)
}

// Running reports that the language runtime has finished INIT and the
// invocation is executing.
func (i *trackedInvocation) Running() {
	i.transition(instanceStatusRunning, events.LambdaInstanceInitializing,
		instanceStatusStarting, instanceStatusInitializing)
}

func (i *trackedInvocation) transition(to string, evt events.Type, from ...string) {
	if i == nil {
		return
	}
	t := i.t
	t.mu.Lock()
	entry, ok := t.entries[i.key]
	if !ok {
		t.mu.Unlock()
		return
	}
	allowed := false
	for _, s := range from {
		if entry.status == s {
			allowed = true
			break
		}
	}
	if !allowed {
		t.mu.Unlock()
		return
	}
	entry.status = to
	snap := entry.toPayload()
	t.mu.Unlock()

	t.publish(evt, snap)
}

// SetLogRefs attaches the CloudWatch Logs group and stream to the instance.
func (i *trackedInvocation) SetLogRefs(logGroup, logStream string) {
	if i == nil {
		return
	}
	i.t.mu.Lock()
	defer i.t.mu.Unlock()
	if entry, ok := i.t.entries[i.key]; ok {
		entry.logGroup = logGroup
		entry.logStream = logStream
	}
}

// Finish marks the invocation complete. The instance goes idle, unless the
// function was updated mid-invocation (Invalidate), in which case the record is
// evicted — the container is retired on release and will not be reused.
func (i *trackedInvocation) Finish(success bool, failureReason string) {
	if i == nil {
		return
	}
	t := i.t
	t.mu.Lock()
	entry, ok := t.entries[i.key]
	if !ok {
		t.mu.Unlock()
		return
	}
	entry.status = instanceStatusIdle
	entry.lastUsed = t.clk.Now()
	if success {
		entry.lastInvocationStatus = invocationSucceeded
		entry.lastInvocationError = ""
	} else {
		entry.lastInvocationStatus = invocationFailed
		entry.lastInvocationError = failureReason
	}
	retire := entry.retire
	if retire {
		delete(t.entries, i.key)
	}
	snap := entry.toPayload()
	t.mu.Unlock()

	t.publish(events.LambdaInstanceReleased, snap)
	if retire {
		t.publish(events.LambdaInstanceEvicted, snap)
	}
}

// Abandon drops the record for an invocation that never got an environment —
// the cold start failed, or the invocation was throttled before one was
// assigned. Only removes the provisional record; a bound instance survives so
// the container it represents stays visible.
func (i *trackedInvocation) Abandon(reason string) {
	if i == nil {
		return
	}
	t := i.t
	t.mu.Lock()
	entry, ok := t.entries[i.key]
	if !ok || entry.instanceID == "" {
		t.mu.Unlock()
		return
	}
	if i.bound {
		// The environment exists; report the failure and leave it idle.
		entry.status = instanceStatusIdle
		entry.lastUsed = t.clk.Now()
		entry.lastInvocationStatus = invocationFailed
		entry.lastInvocationError = reason
		snap := entry.toPayload()
		t.mu.Unlock()
		t.publish(events.LambdaInstanceReleased, snap)
		return
	}
	delete(t.entries, i.key)
	entry.lastInvocationStatus = invocationFailed
	entry.lastInvocationError = reason
	snap := entry.toPayload()
	t.mu.Unlock()
	t.publish(events.LambdaInstanceEvicted, snap)
}

// invocationOutcome reduces an invocation's error and result to the
// (success, reason) pair Finish takes. A function that returned a handled
// error counts as a failure, matching what the UI shows for the instance.
func invocationOutcome(err error, result *InvokeResult) (bool, string) {
	switch {
	case err != nil:
		return false, err.Error()
	case result != nil && result.FunctionError != "":
		return false, result.FunctionError
	default:
		return true, ""
	}
}

// ─── Pool-driven lifecycle ───────────────────────────────────────────────────

// InstanceWarmed records an execution environment created outside an
// invocation — today, only provisioned concurrency pre-warming. Idempotent.
// containerID may be empty for runtimes that are not container-backed.
func (t *instanceTracker) InstanceWarmed(functionName, instanceID, containerID string, provisioned bool) {
	if t == nil || instanceID == "" {
		return
	}
	now := t.clk.Now()
	t.mu.Lock()
	if t.entries == nil {
		t.mu.Unlock()
		return
	}
	entry, ok := t.entries[instanceID]
	if !ok {
		entry = &trackerEntry{
			instanceID:   instanceID,
			functionName: functionName,
			startedAt:    now,
			status:       instanceStatusIdle,
		}
		t.entries[instanceID] = entry
	}
	if containerID != "" {
		entry.containerID = containerID
	}
	entry.provisioned = provisioned
	entry.lastUsed = now
	snap := entry.toPayload()
	t.mu.Unlock()

	if !ok {
		t.publish(events.LambdaInstanceReady, snap)
	}
}

// InstanceLost records that an execution environment no longer exists. Called
// by the pool for every container it destroys, whatever the reason: idle sweep,
// reclaimed for capacity, retired after an update, died in Docker, or the
// function was deleted.
func (t *instanceTracker) InstanceLost(functionName, instanceID string) {
	if t == nil || instanceID == "" {
		return
	}
	t.mu.Lock()
	entry, ok := t.entries[instanceID]
	if ok {
		delete(t.entries, instanceID)
	}
	t.mu.Unlock()
	if !ok {
		return
	}
	snap := entry.toPayload()
	snap.Status = instanceStatusIdle // already removed
	t.publish(events.LambdaInstanceEvicted, snap)
}

// Invalidate retires every instance of functionName after a code or
// configuration update. Idle instances are evicted immediately; instances
// mid-invocation are marked so Finish evicts them when the invocation
// completes. No replacement is tracked until the next invocation arrives.
func (t *instanceTracker) Invalidate(functionName string) {
	if t == nil {
		return
	}
	var evicted []events.LambdaInstancePayload
	t.mu.Lock()
	for key, entry := range t.entries {
		if entry.functionName != functionName {
			continue
		}
		if entry.status != instanceStatusIdle {
			entry.retire = true
			continue
		}
		delete(t.entries, key)
		evicted = append(evicted, entry.toPayload())
	}
	t.mu.Unlock()

	for _, snap := range evicted {
		t.publish(events.LambdaInstanceEvicted, snap)
	}
}

// Evict removes every record for a function (e.g. after DeleteFunction).
func (t *instanceTracker) Evict(functionName string) {
	if t == nil {
		return
	}
	var evicted []events.LambdaInstancePayload
	t.mu.Lock()
	for key, entry := range t.entries {
		if entry.functionName != functionName {
			continue
		}
		delete(t.entries, key)
		snap := entry.toPayload()
		snap.Status = instanceStatusIdle // already removed
		evicted = append(evicted, snap)
	}
	t.mu.Unlock()

	for _, snap := range evicted {
		t.publish(events.LambdaInstanceEvicted, snap)
	}
}

// Instances returns a point-in-time snapshot of all tracked instances,
// refreshing per-container resource stats first (best-effort).
func (t *instanceTracker) Instances() []events.LambdaInstancePayload {
	if t == nil {
		return nil
	}
	t.refreshStats()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]events.LambdaInstancePayload, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, e.toPayload())
	}
	return out
}

// statsRefreshMinInterval throttles refreshStats: snapshot requests arriving
// closer together than this reuse the previous sample instead of hitting the
// Docker daemon again.
const statsRefreshMinInterval = time.Second

// refreshStats samples memory/CPU for every container-backed entry and stores
// the results on the entries. Runs only on the snapshot (UI) path — never on
// the invoke path — and is best-effort: a failed sample keeps the previous
// values rather than zeroing them.
//
// CPU% is derived docker-CLI-style from the delta between this sample's and
// the previous sample's cumulative counters, so it reads as "since the last
// snapshot request" and is 0 on the first sample of an instance.
func (t *instanceTracker) refreshStats() {
	type target struct {
		key         string
		containerID string
	}
	t.mu.Lock()
	statsFn := t.statsFn
	if statsFn == nil || t.entries == nil {
		t.mu.Unlock()
		return
	}
	now := t.clk.Now()
	if now.Sub(t.lastStatsRefresh) < statsRefreshMinInterval {
		t.mu.Unlock()
		return
	}
	t.lastStatsRefresh = now
	targets := make([]target, 0, len(t.entries))
	for key, e := range t.entries {
		if e.containerID != "" {
			targets = append(targets, target{key: key, containerID: e.containerID})
		}
	}
	t.mu.Unlock()
	if len(targets) == 0 {
		return
	}

	// One bounded window for the whole batch; the Docker client caps its own
	// request concurrency, so per-target goroutines are safe.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	samples := make([]*docker.ContainerStats, len(targets))
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats, err := statsFn(ctx, tgt.containerID)
			if err != nil {
				return // keep the entry's previous values
			}
			samples[i] = &stats
		}()
	}
	wg.Wait()

	t.mu.Lock()
	defer t.mu.Unlock()
	for i, tgt := range targets {
		stats := samples[i]
		if stats == nil {
			continue
		}
		entry, ok := t.entries[tgt.key]
		if !ok || entry.containerID != tgt.containerID {
			continue // evicted or rebound while sampling
		}
		entry.memUsedMB = int(stats.MemoryUsageBytes / (1024 * 1024))
		if entry.lastCPUTotal > 0 && stats.CPUTotalUsage >= entry.lastCPUTotal &&
			stats.SystemCPUUsage > entry.lastSystemCPU {
			cpuDelta := float64(stats.CPUTotalUsage - entry.lastCPUTotal)
			sysDelta := float64(stats.SystemCPUUsage - entry.lastSystemCPU)
			cpus := stats.OnlineCPUs
			if cpus < 1 {
				cpus = 1
			}
			// Round to 0.1 pp so the UI doesn't jitter through noise digits.
			entry.cpuPercent = float64(int(cpuDelta/sysDelta*float64(cpus)*1000+0.5)) / 10
		}
		entry.lastCPUTotal = stats.CPUTotalUsage
		entry.lastSystemCPU = stats.SystemCPUUsage
	}
}

// Stop shuts down the background sweeper. Idempotent: Service.Stop is
// reachable more than once (process shutdown, a test's t.Cleanup, a service
// stopped and restarted in-process), and a second close would panic.
func (t *instanceTracker) Stop() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() { close(t.stopCh) })
}

// sweepLoop evicts instances that have been idle for more than trackerIdleTTL.
const trackerIdleTTL = 15 * time.Minute
const trackerSweepInterval = 30 * time.Second

func (t *instanceTracker) sweepLoop() {
	ticker := t.clk.Ticker(trackerSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.sweep()
		case <-t.stopCh:
			return
		}
	}
}

func (t *instanceTracker) sweep() {
	cutoff := t.clk.Now().Add(-trackerIdleTTL)

	t.mu.Lock()
	var evict []trackerEntry
	for key, entry := range t.entries {
		if entry.provisioned || entry.status != instanceStatusIdle {
			continue
		}
		if entry.lastUsed.Before(cutoff) {
			evict = append(evict, *entry)
			delete(t.entries, key)
		}
	}
	t.mu.Unlock()

	for _, e := range evict {
		// A per-tick sweep-cycle outcome — TRACE per the trace-vs-debug
		// policy (CONTRIBUTING.md § Log levels), matching InstancePool's
		// analogous sweep loop.
		t.log.Log(logging.TraceLevel, "lambda tracker: evicted idle instance",
			zap.String("function", e.functionName),
			zap.String("instance", e.instanceID),
		)
		t.publish(events.LambdaInstanceEvicted, e.toPayload())
	}
}
