package lambda

// instance_tracker.go — tracks running and idle Lambda execution instances.
//
// Each Lambda function can have one warm instance (the current model). The
// tracker records when each instance was acquired (running) and released
// (idle), enables computing an ExpiresAt time based on the 15-minute idle TTL,
// and publishes SSE events for the topology map.
//
// The tracker is separate from InstancePool (which manages the actual warm
// container processes) so that it works today even while NodeRuntime is a
// stub — it tracks invocations regardless of whether a real container is
// behind them.

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
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

// trackerEntry is the in-memory record for one Lambda instance.
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
}

func (e *trackerEntry) toPayload(clk clock.Clock) events.LambdaInstancePayload {
	_ = clk // reserved for future: last-used relative to injected clock
	expiresAt := e.lastUsed.Add(15 * time.Minute)
	return events.LambdaInstancePayload{
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
		MemoryUsedMB:         0,   // TODO(priority:P3): collect via /proc or container stats
		CPUPercent:           0.0, // TODO(priority:P3): collect via /proc or container stats
	}
}

// instanceTracker tracks running/idle Lambda instances and publishes lifecycle events.
type instanceTracker struct {
	mu      sync.Mutex
	entries map[string]*trackerEntry // keyed by function name

	bus    *events.Bus
	clk    clock.Clock
	log    *zap.Logger
	stopCh chan struct{}
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
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bus = b
}

// Acquire marks the named function as starting (new instance) or running (reused).
// Returns the stable instanceID assigned to this instance.
// payload is the triggering event body — stored for the UI "Trigger Event" tab.
//
// Cold starts (new instance): status = "starting". The caller must call Ready()
// after the container is actually acquired and ready to invoke. Warm starts
// (reused instance): status = "running" immediately.
func (t *instanceTracker) Acquire(functionName string, payload []byte) string {
	t.mu.Lock()

	entry, ok := t.entries[functionName]
	if !ok {
		entry = &trackerEntry{
			instanceID:   uuid.New().String(),
			functionName: functionName,
			startedAt:    t.clk.Now(),
			status:       instanceStatusStarting,
		}
	} else {
		entry.status = instanceStatusRunning
	}
	entry.lastUsed = t.clk.Now()
	entry.lastInvocationStatus = ""
	entry.lastInvocationError = ""
	if len(payload) > 0 {
		entry.triggerEvent = payload
	}
	t.entries[functionName] = entry

	snap := entry.toPayload(t.clk)
	bus := t.bus
	t.mu.Unlock()

	if bus != nil {
		bus.Publish(context.Background(), events.Event{
			Type:    events.LambdaInstanceAcquired,
			Time:    t.clk.Now(),
			Source:  "lambda",
			Payload: snap,
		})
	}
	return entry.instanceID
}

// Ready transitions a starting instance to initializing and publishes an
// LambdaInstanceReady event so the topology map can update the node status
// in real time. Safe to call on instances that are already initializing or
// running (no-op). Called after rt.Acquire succeeds — i.e. the container is
// alive and registered with the Runtime API, but the RIC has not yet polled
// GET /next.
func (t *instanceTracker) Ready(functionName string) {
	t.mu.Lock()
	entry, ok := t.entries[functionName]
	if !ok || entry.status != instanceStatusStarting {
		t.mu.Unlock()
		return
	}
	entry.status = instanceStatusInitializing
	snap := entry.toPayload(t.clk)
	bus := t.bus
	t.mu.Unlock()

	if bus != nil {
		bus.Publish(context.Background(), events.Event{
			Type:    events.LambdaInstanceReady,
			Time:    t.clk.Now(),
			Source:  "lambda",
			Payload: snap,
		})
	}
}

// RuntimeConnected transitions an initializing instance to running and
// publishes a LambdaInstanceInitializing event. Called when the container's
// runtime interface client (RIC) issues its first GET /next — meaning the
// language runtime and handler code have been loaded and the function is
// truly ready to execute.
func (t *instanceTracker) RuntimeConnected(functionName string) {
	t.mu.Lock()
	entry, ok := t.entries[functionName]
	if !ok || (entry.status != instanceStatusInitializing && entry.status != instanceStatusStarting) {
		t.mu.Unlock()
		return
	}
	entry.status = instanceStatusRunning
	snap := entry.toPayload(t.clk)
	bus := t.bus
	t.mu.Unlock()

	if bus != nil {
		bus.Publish(context.Background(), events.Event{
			Type:    events.LambdaInstanceInitializing,
			Time:    t.clk.Now(),
			Source:  "lambda",
			Payload: snap,
		})
	}
}

// Release marks the instance as idle after an invocation completes.
// success indicates whether the invocation completed without runtime/function
// errors. failureReason is captured when success is false.
func (t *instanceTracker) Release(functionName string, success bool, failureReason string) {
	t.mu.Lock()
	entry, ok := t.entries[functionName]
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
	snap := entry.toPayload(t.clk)
	bus := t.bus
	t.mu.Unlock()

	if bus != nil {
		bus.Publish(context.Background(), events.Event{
			Type:    events.LambdaInstanceReleased,
			Time:    t.clk.Now(),
			Source:  "lambda",
			Payload: snap,
		})
	}
}

// SetLogRefs attaches the CloudWatch Logs group and stream to the instance.
// Called after EnsureLogStream creates or confirms the stream.
func (t *instanceTracker) SetLogRefs(functionName, logGroup, logStream string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry, ok := t.entries[functionName]; ok {
		entry.logGroup = logGroup
		entry.logStream = logStream
	}
}

// Evict removes a function's instance record (e.g. after DeleteFunction).
func (t *instanceTracker) Evict(functionName string) {
	t.mu.Lock()
	entry, ok := t.entries[functionName]
	if ok {
		delete(t.entries, functionName)
	}
	bus := t.bus
	t.mu.Unlock()
	if !ok {
		return
	}
	if bus != nil {
		snap := entry.toPayload(t.clk)
		snap.Status = instanceStatusIdle // already removed; mark as idle before eviction
		bus.Publish(context.Background(), events.Event{
			Type:    events.LambdaInstanceEvicted,
			Time:    t.clk.Now(),
			Source:  "lambda",
			Payload: snap,
		})
	}
}

// Instances returns a point-in-time snapshot of all tracked instances.
func (t *instanceTracker) Instances() []events.LambdaInstancePayload {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]events.LambdaInstancePayload, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, e.toPayload(t.clk))
	}
	return out
}

// Stop shuts down the background sweeper.
func (t *instanceTracker) Stop() {
	close(t.stopCh)
}

// sweepLoop evicts instances that have been idle for more than poolIdleTTL.
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
	for name, entry := range t.entries {
		if entry.status == instanceStatusIdle && entry.lastUsed.Before(cutoff) {
			evict = append(evict, *entry)
			delete(t.entries, name)
		}
	}
	bus := t.bus
	t.mu.Unlock()

	for _, e := range evict {
		t.log.Debug("lambda tracker: evicted idle instance",
			zap.String("function", e.functionName),
			zap.String("instance", e.instanceID),
		)
		if bus != nil {
			snap := e.toPayload(t.clk)
			bus.Publish(context.Background(), events.Event{
				Type:    events.LambdaInstanceEvicted,
				Time:    t.clk.Now(),
				Source:  "lambda",
				Payload: snap,
			})
		}
	}
}
