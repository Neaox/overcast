package compat

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// Suite process state machine
// ---------------------------------------------------------------------------

// SuiteState represents the current lifecycle state of a suite process.
type SuiteState string

const (
	SuiteBuilding SuiteState = "building"
	SuiteReady    SuiteState = "ready"
	SuiteBusy     SuiteState = "busy"
	SuiteError    SuiteState = "error"
	SuiteStopped  SuiteState = "stopped"
)

// ---------------------------------------------------------------------------
// Command and test reference types
// ---------------------------------------------------------------------------

// TestRef identifies a specific test or group of tests.
type TestRef struct {
	Group string   `json:"group"`
	Tests []string `json:"tests,omitempty"` // if nil, all tests in group
}

// QueuedBatch is a batch of tests waiting to be sent to a suite.
type QueuedBatch struct {
	ID        string    `json:"batch_id"`
	Tests     []TestRef `json:"tests"`
	CreatedAt time.Time `json:"created_at"`
}

// StdinCommand is a JSON command sent to a suite process via stdin.
type StdinCommand struct {
	Command string    `json:"command"`
	BatchID string    `json:"batch_id,omitempty"`
	Tests   []TestRef `json:"tests,omitempty"`
	Group   string    `json:"group,omitempty"`
	Test    string    `json:"test,omitempty"`
}

// ---------------------------------------------------------------------------
// API response types
// ---------------------------------------------------------------------------

// QueueEntry represents a single item in the queue (for API responses).
type QueueEntry struct {
	BatchID string `json:"batch_id"`
	Suite   string `json:"suite"`
	Group   string `json:"group"`
	Test    string `json:"test,omitempty"`
	State   string `json:"state"` // "queued" or "running"
}

// SuiteStatus is the API response for suite state.
type SuiteStatus struct {
	Name        string     `json:"name"`
	State       SuiteState `json:"state"`
	QueuedCount int        `json:"queued_count"`
	RunningTest string     `json:"running_test,omitempty"`
}

// ---------------------------------------------------------------------------
// SuiteProcess — manages a single long-lived suite runner process
// ---------------------------------------------------------------------------

// SuiteProcess manages a single long-lived suite runner process.
type SuiteProcess struct {
	Name          string
	Config        SuiteConfig
	State         SuiteState
	Cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	cancel        context.CancelFunc
	done          chan struct{} // closed when readStdout exits
	Queue         []QueuedBatch
	ActiveBatch   *QueuedBatch
	RunningTest   string // "group:test" currently executing
	PendingBuffer []StdinCommand
	LastEventAt   time.Time // last time any stdout event was received
	PingSentAt    time.Time // last time a ping command was sent
	CancelSentAt  time.Time // last time a cancel command was sent for a stuck test
	Interactive   bool      // true if the suite emitted a 'ready' event
	mu            sync.Mutex
}

// ---------------------------------------------------------------------------
// Orchestrator — central coordinator for all suite processes
// ---------------------------------------------------------------------------

// Orchestrator manages all suite processes for interactive compat testing.
type Orchestrator struct {
	processes map[string]*SuiteProcess
	results   map[string]TestResultEvent // keyed by "suite:group:test"
	mu        sync.RWMutex
	resultsMu sync.RWMutex
	onEvent   func([]byte) // callback to broadcast events (SSE)
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	logger    *slog.Logger

	// sseClients are additional channels that receive raw NDJSON event copies
	// (used by the MCP SSE endpoint).
	sseMu      sync.Mutex
	sseClients map[chan []byte]bool

	// Endpoint and Region are injected into suite subprocess environments.
	Endpoint string
	Region   string
}

// NewOrchestrator creates a new orchestrator for the given suite configs.
// onEvent is called with each raw NDJSON event line for SSE broadcast.
func NewOrchestrator(ctx context.Context, configs []SuiteConfig, onEvent func([]byte), logger *slog.Logger) *Orchestrator {
	ctx, cancel := context.WithCancel(ctx)
	procs := make(map[string]*SuiteProcess, len(configs))
	for _, cfg := range configs {
		procs[cfg.Name] = &SuiteProcess{
			Name:   cfg.Name,
			Config: cfg,
			State:  SuiteStopped,
		}
	}
	return &Orchestrator{
		processes:  procs,
		results:    make(map[string]TestResultEvent),
		sseClients: make(map[chan []byte]bool),
		onEvent:    onEvent,
		ctx:        ctx,
		cancel:     cancel,
		logger:     logger,
	}
}

// ---------------------------------------------------------------------------
// Public methods
// ---------------------------------------------------------------------------

// Start spawns all suite processes, begins reading their stdout, and launches
// a watchdog goroutine that detects stalled suites.
func (o *Orchestrator) Start() error {
	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, sp := range o.processes {
		sp.LastEventAt = time.Now()
		if err := o.startSuite(sp); err != nil {
			return fmt.Errorf("start suite %q: %w", sp.Name, err)
		}
	}

	o.wg.Add(1)
	go o.watchdog()

	return nil
}

// ---------------------------------------------------------------------------
// Watchdog — detects stalled suites
// ---------------------------------------------------------------------------

const (
	// silenceLimit is how long a suite may go without emitting any stdout
	// event before the orchestrator sends a ping to ask if it is still alive.
	silenceLimit = 10 * time.Second

	// pingTimeout is how long the orchestrator waits for a pong response
	// after sending a ping before cancelling the stuck test.
	pingTimeout = 5 * time.Second

	// killGrace is how long the orchestrator waits after cancelling a stuck
	// test before forcibly killing the suite process. If the suite cannot
	// even acknowledge a cancel command it is considered broken and must be
	// terminated to free resources.
	killGrace = 10 * time.Second

	// buildTimeout is the maximum time a suite may spend in the building
	// state before it is considered broken and killed.  Must be generous
	// enough for go run / cargo build / npm install on cold caches.
	buildTimeout = 5 * time.Minute

	// watchdogInterval is how often the watchdog checks all suites.
	watchdogInterval = 2 * time.Second
)

// watchdog periodically checks all suite processes. If an active suite has
// not emitted an event within silenceLimit, the orchestrator sends a ping.
// If no pong arrives within pingTimeout, the stuck test is cancelled.
// If the suite still does not respond after killGrace, the process is killed.
// Suites that stay in the building state beyond buildTimeout are also killed.
func (o *Orchestrator) watchdog() {
	defer o.wg.Done()

	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			return
		case <-ticker.C:
		}

		now := time.Now()
		o.mu.RLock()
		for _, sp := range o.processes {
			sp.mu.Lock()

			switch sp.State {
			case SuiteReady, SuiteError, SuiteStopped:
				// Nothing to monitor for inactive suites.
			case SuiteBuilding:
				// Suite hasn't emitted 'ready' within buildTimeout — kill it.
				if now.Sub(sp.LastEventAt) > buildTimeout {
					sp.mu.Unlock()
					o.killSuite(sp, "building timeout — never reached ready state")
					continue
				}

			case SuiteBusy:
				// Only monitor interactive suites — batch-mode suites cannot
				// respond to ping/cancel and must be left to complete on
				// their own (they exit when done).
				if !sp.Interactive || sp.ActiveBatch == nil {
					sp.mu.Unlock()
					continue
				}

				// Suite was cancelled for a stuck test but still hasn't
				// responded — kill the process.
				if !sp.CancelSentAt.IsZero() && now.Sub(sp.CancelSentAt) > killGrace {
					sp.mu.Unlock()
					o.killSuite(sp, fmt.Sprintf("unresponsive after cancel — stuck on %s", sp.RunningTest))
					continue
				}

				// Suite is silent — hasn't emitted any event recently.
				if now.Sub(sp.LastEventAt) > silenceLimit {
					if sp.PingSentAt.IsZero() {
						// First silence detection — send ping.
						sp.PingSentAt = now
						sp.mu.Unlock()
						o.logger.Debug("suite silent — sending ping", "suite", sp.Name, "test", sp.RunningTest)
						_ = o.sendCommand(sp, StdinCommand{Command: "ping"})
						continue
					}

					if now.Sub(sp.PingSentAt) > pingTimeout {
						// Ping was not answered — cancel the stuck test.
						cancelCmd := StdinCommand{
							Command: "cancel",
							BatchID: sp.ActiveBatch.ID,
						}
						sp.PingSentAt = time.Time{}
						sp.CancelSentAt = now
						sp.mu.Unlock()
						o.logger.Warn("suite stalled — cancelling stuck test",
							"suite", sp.Name,
							"test", sp.RunningTest,
						)
						if err := o.sendCommand(sp, cancelCmd); err != nil {
							o.logger.Error("cancel stalled test failed", "suite", sp.Name, "err", err)
						}
						continue
					}
				} else {
					// Activity detected — reset tracking.
					sp.PingSentAt = time.Time{}
					sp.CancelSentAt = time.Time{}
				}
			}

			sp.mu.Unlock()
		}
		o.mu.RUnlock()
	}
}

// killSuite forcibly terminates a suite process that has broken the protocol
// contract (unresponsive to ping, stuck in building, crashed without EOF).
func (o *Orchestrator) killSuite(sp *SuiteProcess, reason string) {
	o.logger.Error("killing unresponsive suite", "suite", sp.Name, "reason", reason)

	sp.mu.Lock()
	sp.State = SuiteError
	if sp.cancel != nil {
		sp.cancel()
	}
	if sp.Cmd != nil && sp.Cmd.Process != nil {
		_ = sp.Cmd.Process.Kill()
	}
	sp.mu.Unlock()

	// Emit a suite_error event so the UI can show it.
	if o.onEvent != nil {
		data, _ := json.Marshal(struct {
			Event string `json:"event"`
			Suite string `json:"suite"`
			Error string `json:"error"`
		}{Event: string(EventSuiteError), Suite: sp.Name, Error: reason})
		o.onEvent(data)
	}
}

// SubmitTests queues tests for execution across specified suites.
// If suites is nil/empty, submits to all suites.
// Returns batch ID, list of queued items, and count of skipped duplicates.
func (o *Orchestrator) SubmitTests(suites []string, tests []TestRef) (batchID string, queued []QueueEntry, skippedDups int) {
	batchID = generateBatchID()
	now := time.Now()

	o.mu.RLock()
	defer o.mu.RUnlock()

	targets := o.targetSuites(suites)
	for _, sp := range targets {
		sp.mu.Lock()

		// nil/empty tests means "run all tests in this suite". Send the run
		// command with no tests field so the runner executes all its groups.
		if len(tests) == 0 {
			// Simple dedup: skip if there's already an active or queued batch.
			if sp.ActiveBatch != nil || len(sp.Queue) > 0 {
				skippedDups++
				sp.mu.Unlock()
				continue
			}
			suiteBatch := QueuedBatch{ID: batchID, Tests: nil, CreatedAt: now}
			queued = append(queued, QueueEntry{
				BatchID: batchID,
				Suite:   sp.Name,
				Group:   "",
				State:   "queued",
			})
			runAllCmd := StdinCommand{Command: "run", BatchID: batchID}
			switch sp.State {
			case SuiteReady:
				sp.ActiveBatch = &suiteBatch
				sp.mu.Unlock()
				if err := o.sendCommand(sp, runAllCmd); err != nil {
					o.logger.Error("send command failed", "suite", sp.Name, "err", err)
				}
				continue
			case SuiteBuilding:
				sp.PendingBuffer = append(sp.PendingBuffer, runAllCmd)
			case SuiteBusy, SuiteError, SuiteStopped:
				sp.Queue = append(sp.Queue, suiteBatch)
			}
			sp.mu.Unlock()
			continue
		}

		// Deduplicate: skip tests already queued or running.
		var dedupedTests []TestRef
		for _, ref := range tests {
			if len(ref.Tests) == 0 {
				if o.isDuplicate(sp, ref.Group, "") {
					skippedDups++
				} else {
					dedupedTests = append(dedupedTests, ref)
				}
			} else {
				var kept []string
				for _, t := range ref.Tests {
					if o.isDuplicate(sp, ref.Group, t) {
						skippedDups++
					} else {
						kept = append(kept, t)
					}
				}
				if len(kept) > 0 {
					dedupedTests = append(dedupedTests, TestRef{Group: ref.Group, Tests: kept})
				}
			}
		}

		if len(dedupedTests) == 0 {
			sp.mu.Unlock()
			continue
		}

		suiteBatch := QueuedBatch{
			ID:        batchID,
			Tests:     dedupedTests,
			CreatedAt: now,
		}

		// Build queue entries for the response.
		for _, ref := range dedupedTests {
			if len(ref.Tests) == 0 {
				queued = append(queued, QueueEntry{
					BatchID: batchID,
					Suite:   sp.Name,
					Group:   ref.Group,
					State:   "queued",
				})
			} else {
				for _, t := range ref.Tests {
					queued = append(queued, QueueEntry{
						BatchID: batchID,
						Suite:   sp.Name,
						Group:   ref.Group,
						Test:    t,
						State:   "queued",
					})
				}
			}
		}

		switch sp.State {
		case SuiteReady:
			if sp.ActiveBatch == nil {
				// Send immediately.
				sp.ActiveBatch = &suiteBatch
				cmd := StdinCommand{
					Command: "run",
					BatchID: batchID,
					Tests:   dedupedTests,
				}
				sp.mu.Unlock()
				if err := o.sendCommand(sp, cmd); err != nil {
					o.logger.Error("send command failed", "suite", sp.Name, "err", err)
				}
				continue
			}
			// Suite is ready but already has an active batch; queue it.
			sp.Queue = append(sp.Queue, suiteBatch)
		case SuiteBuilding:
			// Buffer for when the suite becomes ready.
			sp.PendingBuffer = append(sp.PendingBuffer, StdinCommand{
				Command: "run",
				BatchID: batchID,
				Tests:   dedupedTests,
			})
		case SuiteBusy, SuiteError, SuiteStopped:
			// Queue even if in error/stopped — will be sent on reload.
			sp.Queue = append(sp.Queue, suiteBatch)
		}
		sp.mu.Unlock()
	}

	return batchID, queued, skippedDups
}

// SubmitFailingTests re-queues all tests whose last result matched one of the
// given statuses (e.g. "fail", "skip", "unimplemented"). If statuses is empty
// it defaults to [StatusFail]. Returns the queued entries so callers can relay
// them to clients.
func (o *Orchestrator) SubmitFailingTests(suiteFilter, serviceFilter string, statuses ...Status) (batchID string, queued []QueueEntry) {
	want := make(map[Status]bool, len(statuses))
	for _, s := range statuses {
		want[s] = true
	}
	if len(want) == 0 {
		want[StatusFail] = true
	}

	o.resultsMu.RLock()

	// Group matching tests by group name, tracking which suites are involved.
	byGroup := make(map[string][]string)
	suiteSet := make(map[string]bool)
	for _, r := range o.results {
		if !want[r.Status] {
			continue
		}
		if suiteFilter != "" && r.Suite != suiteFilter {
			continue
		}
		if serviceFilter != "" && r.Service != serviceFilter {
			continue
		}
		byGroup[r.Group] = append(byGroup[r.Group], r.Test)
		suiteSet[r.Suite] = true
	}
	o.resultsMu.RUnlock()

	if len(byGroup) == 0 {
		return "", nil
	}

	var tests []TestRef
	for group, testNames := range byGroup {
		tests = append(tests, TestRef{Group: group, Tests: testNames})
	}

	var suiteNames []string
	for name := range suiteSet {
		suiteNames = append(suiteNames, name)
	}

	batchID, queued, _ = o.SubmitTests(suiteNames, tests)
	return batchID, queued
}

// CancelTests cancels matching queued/running tests.
// Supports cancellation by batchID, suite+group+test, or all.
func (o *Orchestrator) CancelTests(batchID, suite, group, test string, all bool) []QueueEntry {
	var cancelled []QueueEntry

	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, sp := range o.processes {
		sp.mu.Lock()

		// Remove matching entries from the queue.
		var kept []QueuedBatch
		for _, qb := range sp.Queue {
			matches := all ||
				(batchID != "" && qb.ID == batchID) ||
				(suite != "" && sp.Name == suite)
			if !matches {
				kept = append(kept, qb)
				continue
			}
			for _, ref := range qb.Tests {
				if group != "" && ref.Group != group {
					continue
				}
				if len(ref.Tests) == 0 {
					cancelled = append(cancelled, QueueEntry{
						BatchID: qb.ID,
						Suite:   sp.Name,
						Group:   ref.Group,
						State:   "queued",
					})
				} else {
					for _, t := range ref.Tests {
						if test != "" && t != test {
							continue
						}
						cancelled = append(cancelled, QueueEntry{
							BatchID: qb.ID,
							Suite:   sp.Name,
							Group:   ref.Group,
							Test:    t,
							State:   "queued",
						})
					}
				}
			}
			// Drop this batch from the queue (don't add to kept).
		}
		sp.Queue = kept

		// Cancel running batch if it matches.
		if sp.ActiveBatch != nil {
			shouldCancel := all ||
				(batchID != "" && sp.ActiveBatch.ID == batchID) ||
				(suite != "" && sp.Name == suite)
			if shouldCancel {
				for _, ref := range sp.ActiveBatch.Tests {
					if group != "" && ref.Group != group {
						continue
					}
					if len(ref.Tests) == 0 {
						cancelled = append(cancelled, QueueEntry{
							BatchID: sp.ActiveBatch.ID,
							Suite:   sp.Name,
							Group:   ref.Group,
							State:   "running",
						})
					} else {
						for _, t := range ref.Tests {
							if test != "" && t != test {
								continue
							}
							cancelled = append(cancelled, QueueEntry{
								BatchID: sp.ActiveBatch.ID,
								Suite:   sp.Name,
								Group:   ref.Group,
								Test:    t,
								State:   "running",
							})
						}
					}
				}
				cancelCmd := StdinCommand{Command: "cancel", BatchID: sp.ActiveBatch.ID}
				// Clear ActiveBatch immediately so subsequent SubmitTests
				// calls don't skip this suite as a duplicate. The suite
				// process will still emit batch_complete eventually; the
				// readStdout handler is idempotent (nil ActiveBatch is fine).
				sp.ActiveBatch = nil
				sp.RunningTest = ""
				sp.State = SuiteReady
				sp.mu.Unlock()
				if err := o.sendCommand(sp, cancelCmd); err != nil {
					o.logger.Error("cancel command failed", "suite", sp.Name, "err", err)
				}
				continue
			}
		}

		sp.mu.Unlock()
	}

	// Broadcast cancelled events for entries that were queued (not running).
	// Running entries will emit their own cancelled events via stdout once the
	// suite process acknowledges the cancel command.
	for _, e := range cancelled {
		if e.State == "queued" {
			data, _ := json.Marshal(struct {
				Event   string `json:"event"`
				Suite   string `json:"suite"`
				BatchID string `json:"batch_id"`
				Group   string `json:"group"`
				Test    string `json:"test,omitempty"`
				Reason  string `json:"reason"`
			}{
				Event:   "cancelled",
				Suite:   e.Suite,
				BatchID: e.BatchID,
				Group:   e.Group,
				Test:    e.Test,
				Reason:  "user",
			})
			o.broadcastSSE(data)
		}
	}

	return cancelled
}

// SuiteStates returns the current state of all suites.
func (o *Orchestrator) SuiteStates() []SuiteStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	states := make([]SuiteStatus, 0, len(o.processes))
	for _, sp := range o.processes {
		sp.mu.Lock()
		states = append(states, SuiteStatus{
			Name:        sp.Name,
			State:       sp.State,
			QueuedCount: len(sp.Queue),
			RunningTest: sp.RunningTest,
		})
		sp.mu.Unlock()
	}
	return states
}

// QueueState returns all queued/running items across all suites.
func (o *Orchestrator) QueueState() []QueueEntry {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var entries []QueueEntry
	for _, sp := range o.processes {
		sp.mu.Lock()
		entries = append(entries, suiteQueueEntries(sp)...)
		sp.mu.Unlock()
	}
	return entries
}

// RegisterSSEClient adds a channel that will receive copies of raw NDJSON
// event lines. Used by the MCP SSE endpoint.
func (o *Orchestrator) RegisterSSEClient(ch chan []byte) {
	o.sseMu.Lock()
	o.sseClients[ch] = true
	o.sseMu.Unlock()
}

// UnregisterSSEClient removes a previously registered SSE channel.
func (o *Orchestrator) UnregisterSSEClient(ch chan []byte) {
	o.sseMu.Lock()
	delete(o.sseClients, ch)
	o.sseMu.Unlock()
}

// broadcastSSE sends a copy of the event to all registered SSE clients.
func (o *Orchestrator) broadcastSSE(data []byte) {
	o.sseMu.Lock()
	defer o.sseMu.Unlock()
	for ch := range o.sseClients {
		select {
		case ch <- data:
		default:
			// Slow client — drop rather than block.
		}
	}
}

// Results returns the latest test results, optionally filtered.
// Pass empty strings to skip a filter dimension.
func (o *Orchestrator) Results(suite, service, group, test, status string) []TestResultEvent {
	o.resultsMu.RLock()
	defer o.resultsMu.RUnlock()

	var out []TestResultEvent
	for _, r := range o.results {
		if suite != "" && r.Suite != suite {
			continue
		}
		if service != "" && r.Service != service {
			continue
		}
		if group != "" && r.Group != group {
			continue
		}
		if test != "" && r.Test != test {
			continue
		}
		if status != "" && string(r.Status) != status {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ReloadSuite restarts a specific suite process (hot-swap).
func (o *Orchestrator) ReloadSuite(name string) error {
	o.mu.RLock()
	sp, ok := o.processes[name]
	o.mu.RUnlock()
	if !ok {
		return fmt.Errorf("suite %q not found", name)
	}

	sp.mu.Lock()
	savedQueue := sp.Queue
	sp.Queue = nil
	sp.mu.Unlock()

	o.stopSuiteProcess(sp)

	if err := o.startSuite(sp); err != nil {
		return fmt.Errorf("reload suite %q: %w", name, err)
	}

	// Restore saved queue as pending commands (flushed when suite becomes ready).
	sp.mu.Lock()
	for _, qb := range savedQueue {
		sp.PendingBuffer = append(sp.PendingBuffer, StdinCommand{
			Command: "run",
			BatchID: qb.ID,
			Tests:   qb.Tests,
		})
	}
	sp.mu.Unlock()

	return nil
}

// Shutdown gracefully stops all suite processes.
func (o *Orchestrator) Shutdown() {
	// Stop each suite gracefully before cancelling the parent context.
	o.mu.RLock()
	for _, sp := range o.processes {
		o.stopSuiteProcess(sp)
	}
	o.mu.RUnlock()

	o.cancel()
	o.wg.Wait()
}

// ---------------------------------------------------------------------------
// Internal methods
// ---------------------------------------------------------------------------

// startSuite starts a single suite process.
func (o *Orchestrator) startSuite(sp *SuiteProcess) error {
	if len(sp.Config.Argv) == 0 {
		return fmt.Errorf("suite %q: empty argv", sp.Name)
	}

	ctx, cancel := context.WithCancel(o.ctx)

	//nolint:gosec // argv is from internal config, not user input
	cmd := exec.CommandContext(ctx, sp.Config.Argv[0], sp.Config.Argv[1:]...)
	if sp.Config.Dir != "" {
		cmd.Dir = sp.Config.Dir
	}

	// Run suite subprocesses in their own process group so they do not
	// receive SIGINT when the user presses Ctrl+C on the parent.  This
	// lets the orchestrator gracefully send shutdown commands before
	// the OS kills the child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Env = append(os.Environ(), sp.Config.Env...)
	cmd.Env = append(cmd.Env, "OVERCAST_COMPAT_INTERACTIVE=1")
	if o.Endpoint != "" {
		cmd.Env = append(cmd.Env, "OVERCAST_ENDPOINT="+o.Endpoint)
	}
	if o.Region != "" {
		cmd.Env = append(cmd.Env, "OVERCAST_DEFAULT_REGION="+o.Region)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("suite %q: stdin pipe: %w", sp.Name, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("suite %q: stdout pipe: %w", sp.Name, err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("suite %q: start: %w", sp.Name, err)
	}

	sp.mu.Lock()
	sp.Cmd = cmd
	sp.stdin = stdinPipe
	sp.stdout = stdoutPipe
	sp.cancel = cancel
	sp.State = SuiteBuilding
	sp.ActiveBatch = nil
	sp.RunningTest = ""
	sp.done = make(chan struct{})
	sp.mu.Unlock()

	o.logger.Info("suite process started", "suite", sp.Name, "pid", cmd.Process.Pid)

	o.wg.Add(1)
	go o.readStdout(sp)

	return nil
}

// readStdout reads NDJSON from a suite's stdout, updates state, and broadcasts events.
func (o *Orchestrator) readStdout(sp *SuiteProcess) {
	defer o.wg.Done()

	sp.mu.Lock()
	done := sp.done
	sp.mu.Unlock()
	defer close(done)

	scanner := bufio.NewScanner(sp.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Stable copy for the broadcast callback.
		cp := make([]byte, len(line))
		copy(cp, line)

		if o.onEvent != nil {
			o.onEvent(cp)
		}
		o.broadcastSSE(cp)

		// Peek at the event type and key fields.
		var peek struct {
			Event   string `json:"event"`
			Suite   string `json:"suite"`
			Group   string `json:"group"`
			Test    string `json:"test"`
			Service string `json:"service"`
			Status  Status `json:"status"`
			BatchID string `json:"batch_id"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			o.logger.Warn("malformed NDJSON from suite", "suite", sp.Name, "err", err)
			continue
		}

		switch peek.Event {
		case "building":
			sp.mu.Lock()
			sp.State = SuiteBuilding
			sp.LastEventAt = time.Now()
			sp.mu.Unlock()

		case "ready":
			sp.mu.Lock()
			sp.State = SuiteReady
			sp.Interactive = true
			sp.LastEventAt = time.Now()
			pending := sp.PendingBuffer
			sp.PendingBuffer = nil
			sp.mu.Unlock()

			// Flush commands buffered while the suite was building.
			for _, cmd := range pending {
				if err := o.sendCommand(sp, cmd); err != nil {
					o.logger.Error("flush pending command failed", "suite", sp.Name, "err", err)
				}
			}

			o.processQueue(sp)

		case string(EventTestStart):
			sp.mu.Lock()
			sp.State = SuiteBusy
			sp.RunningTest = peek.Group + ":" + peek.Test
			sp.LastEventAt = time.Now()
			sp.mu.Unlock()

		case string(EventTestResult):
			sp.mu.Lock()
			sp.LastEventAt = time.Now()
			sp.mu.Unlock()
			var ev TestResultEvent
			if err := json.Unmarshal(line, &ev); err == nil {
				key := ev.Suite + ":" + ev.Group + ":" + ev.Test
				o.resultsMu.Lock()
				o.results[key] = ev
				o.resultsMu.Unlock()
			}

		case "batch_complete":
			sp.mu.Lock()
			sp.LastEventAt = time.Now()
			// ActiveBatch may already be nil if CancelTests cleared it.
			if sp.ActiveBatch != nil {
				sp.ActiveBatch = nil
				sp.RunningTest = ""
				sp.State = SuiteReady
			}
			sp.mu.Unlock()
			o.processQueue(sp)

		case "error", string(EventSuiteError):
			sp.mu.Lock()
			sp.State = SuiteError
			sp.LastEventAt = time.Now()
			sp.mu.Unlock()
			o.logger.Error("suite reported error", "suite", sp.Name, "error", peek.Error)

		case "cancelled":
			sp.mu.Lock()
			sp.LastEventAt = time.Now()
			sp.mu.Unlock()
			o.logger.Info("cancellation acknowledged", "suite", sp.Name, "batch", peek.BatchID)

		case "pong":
			sp.mu.Lock()
			sp.LastEventAt = time.Now()
			sp.PingSentAt = time.Time{} // reset — suite acknowledged
			sp.mu.Unlock()
			o.logger.Debug("pong received", "suite", sp.Name)

		default:
			sp.mu.Lock()
			sp.LastEventAt = time.Now()
			sp.mu.Unlock()
		}
	}

	// EOF — suite process exited or crashed.
	sp.mu.Lock()
	if sp.State != SuiteStopped {
		sp.State = SuiteError
		o.logger.Error("suite stdout closed unexpectedly", "suite", sp.Name)
	}
	sp.mu.Unlock()

	if sp.Cmd != nil {
		_ = sp.Cmd.Wait()
	}
}

// sendCommand writes a JSON command to a suite's stdin.
func (o *Orchestrator) sendCommand(sp *SuiteProcess, cmd StdinCommand) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')

	sp.mu.Lock()
	w := sp.stdin
	sp.mu.Unlock()

	if w == nil {
		return fmt.Errorf("suite %q: stdin closed", sp.Name)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("suite %q: stdin write: %w", sp.Name, err)
	}
	o.logger.Debug("sent command to suite", "suite", sp.Name, "command", cmd.Command, "batch", cmd.BatchID)
	return nil
}

// processQueue checks if a suite is ready and sends the next queued batch.
func (o *Orchestrator) processQueue(sp *SuiteProcess) {
	sp.mu.Lock()
	if sp.State != SuiteReady || sp.ActiveBatch != nil || len(sp.Queue) == 0 {
		sp.mu.Unlock()
		return
	}

	batch := sp.Queue[0]
	sp.Queue = sp.Queue[1:]
	sp.ActiveBatch = &batch
	sp.mu.Unlock()

	cmd := StdinCommand{
		Command: "run",
		BatchID: batch.ID,
		Tests:   batch.Tests,
	}
	if err := o.sendCommand(sp, cmd); err != nil {
		o.logger.Error("send queued batch failed", "suite", sp.Name, "batch", batch.ID, "err", err)
		sp.mu.Lock()
		sp.ActiveBatch = nil
		sp.mu.Unlock()
	}
}

// isDuplicate checks if a test is already queued or running for a suite.
// Must be called with sp.mu held.
func (o *Orchestrator) isDuplicate(sp *SuiteProcess, group, test string) bool {
	check := func(refs []TestRef) bool {
		for _, ref := range refs {
			if ref.Group != group {
				continue
			}
			// If either side is "whole group", it's a match.
			if test == "" || len(ref.Tests) == 0 {
				return true
			}
			for _, t := range ref.Tests {
				if t == test {
					return true
				}
			}
		}
		return false
	}

	if sp.ActiveBatch != nil && check(sp.ActiveBatch.Tests) {
		return true
	}
	for _, qb := range sp.Queue {
		if check(qb.Tests) {
			return true
		}
	}
	return false
}

// stopSuiteProcess gracefully shuts down a suite process.
// It sends a shutdown command, waits up to 10 seconds, then kills.
func (o *Orchestrator) stopSuiteProcess(sp *SuiteProcess) {
	sp.mu.Lock()
	if sp.State == SuiteStopped {
		sp.mu.Unlock()
		return
	}
	sp.State = SuiteStopped
	stdinPipe := sp.stdin
	cmd := sp.Cmd
	done := sp.done
	sp.mu.Unlock()

	// Send shutdown command via stdin.
	if stdinPipe != nil {
		shutdownData, _ := json.Marshal(StdinCommand{Command: "shutdown"})
		shutdownData = append(shutdownData, '\n')
		_, _ = stdinPipe.Write(shutdownData)
		_ = stdinPipe.Close()
	}

	// Wait for the reader goroutine to finish (which calls cmd.Wait).
	if done != nil {
		select {
		case <-done:
			// Process exited cleanly.
		case <-time.After(5 * time.Second):
			o.logger.Warn("suite did not exit gracefully, sending SIGTERM", "suite", sp.Name)
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				o.logger.Warn("suite still alive after SIGTERM, killing", "suite", sp.Name)
				if cmd != nil && cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-done
			}
		}
	}
}

// targetSuites returns suite processes matching the given names.
// If names is empty, all processes are returned.
// Must be called with o.mu held (read or write).
func (o *Orchestrator) targetSuites(names []string) []*SuiteProcess {
	if len(names) == 0 {
		out := make([]*SuiteProcess, 0, len(o.processes))
		for _, sp := range o.processes {
			out = append(out, sp)
		}
		return out
	}
	out := make([]*SuiteProcess, 0, len(names))
	for _, name := range names {
		if sp, ok := o.processes[name]; ok {
			out = append(out, sp)
		}
	}
	return out
}

// suiteQueueEntries returns all queued/running items for a single suite.
// Must be called with sp.mu held.
func suiteQueueEntries(sp *SuiteProcess) []QueueEntry {
	var entries []QueueEntry

	// Active batch entries.
	if sp.ActiveBatch != nil {
		for _, ref := range sp.ActiveBatch.Tests {
			if len(ref.Tests) == 0 {
				entries = append(entries, QueueEntry{
					BatchID: sp.ActiveBatch.ID,
					Suite:   sp.Name,
					Group:   ref.Group,
					State:   "running",
				})
			} else {
				for _, t := range ref.Tests {
					entries = append(entries, QueueEntry{
						BatchID: sp.ActiveBatch.ID,
						Suite:   sp.Name,
						Group:   ref.Group,
						Test:    t,
						State:   "running",
					})
				}
			}
		}
	}

	// Queued batch entries.
	for _, qb := range sp.Queue {
		for _, ref := range qb.Tests {
			if len(ref.Tests) == 0 {
				entries = append(entries, QueueEntry{
					BatchID: qb.ID,
					Suite:   sp.Name,
					Group:   ref.Group,
					State:   "queued",
				})
			} else {
				for _, t := range ref.Tests {
					entries = append(entries, QueueEntry{
						BatchID: qb.ID,
						Suite:   sp.Name,
						Group:   ref.Group,
						Test:    t,
						State:   "queued",
					})
				}
			}
		}
	}

	return entries
}

// generateBatchID creates a unique batch identifier like "b-a1b2c3d4".
func generateBatchID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("b-%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return "b-" + hex.EncodeToString(b)
}
