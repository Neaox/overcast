// Package harness provides the core test framework for the Overcast compat Go SDK suite.
//
// Tests emit NDJSON events to stdout. Debug output goes to stderr via ctx.Log().
// Tests return an error to signal failure; nil means pass.
package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TestFn is the signature for a single test.
type TestFn func(ctx context.Context, t *TestContext) error

// TestCase describes one test within a group.
type TestCase struct {
	Name string
	Fn   TestFn
	// Op is the AWS API operation name for doc links.
	// Empty string = use Name. "false" = suppress doc link.
	Op string
	// Skip, if non-empty, causes the test to be emitted as skipped.
	Skip string
	// NA, if non-empty, causes the test to be emitted with status "na".
	// Use this when the AWS SDK client does not yet expose this operation.
	// NA results are excluded from pass-rate calculations.
	NA string
}

// TestGroup is a collection of related tests with optional setup/teardown.
type TestGroup struct {
	Suite    string
	Service  string
	Name     string
	Tests    []TestCase
	Setup    func(ctx context.Context, t *TestContext) error
	Teardown func(ctx context.Context, t *TestContext) error
}

// TestContext carries per-run state for tests.
type TestContext struct {
	Endpoint string
	Region   string
	RunID    string

	mu    sync.Mutex
	state map[string]any
}

// NewTestContext creates a fresh TestContext.
func NewTestContext(endpoint, region, runID string) *TestContext {
	return &TestContext{
		Endpoint: endpoint,
		Region:   region,
		RunID:    runID,
		state:    make(map[string]any),
	}
}

// Set stores a value in the context state bag.
func (t *TestContext) Set(key string, val any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state[key] = val
}

// Get retrieves a value from the context state bag.
func (t *TestContext) Get(key string) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.state[key]
	return v, ok
}

// GetString retrieves a string value from the state bag.
func (t *TestContext) GetString(key string) string {
	v, ok := t.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Log writes a debug message to stderr.
func (t *TestContext) Log(msg string) {
	fmt.Fprintln(os.Stderr, "[go-sdk]", msg)
}

// Reset clears the state bag (called between groups).
func (t *TestContext) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = make(map[string]any)
}

// ─── NDJSON events ────────────────────────────────────────────────────────────

type runStartEvent struct {
	Event      string `json:"event"`
	Suite      string `json:"suite"`
	StartedAt  string `json:"started_at"`
	Endpoint   string `json:"endpoint"`
	Version    string `json:"version"`
	TotalTests int    `json:"total_tests,omitempty"`
}

type testResultEvent struct {
	Event      string `json:"event"`
	Suite      string `json:"suite"`
	Service    string `json:"service"`
	Group      string `json:"group"`
	Test       string `json:"test"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type runEndEvent struct {
	Event         string `json:"event"`
	Suite         string `json:"suite"`
	Passed        int    `json:"passed"`
	Failed        int    `json:"failed"`
	Skipped       int    `json:"skipped"`
	Unimplemented int    `json:"unimplemented"`
	DurationMs    int64  `json:"duration_ms"`
}

// emitMu serialises writes to stdout so concurrent goroutines (parallel
// group execution) never interleave partial NDJSON lines.
var emitMu sync.Mutex

func emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: marshal error:", err)
		return
	}
	emitMu.Lock()
	fmt.Fprintln(os.Stdout, string(b))
	emitMu.Unlock()
}

// ─── Unimplemented detection ──────────────────────────────────────────────────

// IsUnimplemented reports whether err signals a 501 / not-implemented response
// from the Overcast emulator.
func IsUnimplemented(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "501") ||
		strings.Contains(s, "NotImplemented") ||
		strings.Contains(s, "UnknownOperationException")
}

// ─── Group runner ─────────────────────────────────────────────────────────────

// GroupResult holds per-group counts.
type GroupResult struct {
	Passed        int
	Failed        int
	Skipped       int
	Unimplemented int
	Cancelled     int
}

// RunGroup executes one TestGroup and emits test_result events.
func RunGroup(ctx context.Context, group TestGroup, t *TestContext) GroupResult {
	var res GroupResult

	// Setup phase
	if group.Setup != nil {
		if err := group.Setup(ctx, t); err != nil {
			reason := fmt.Sprintf("setup failed: %v", err)
			for _, tc := range group.Tests {
				emit(testResultEvent{
					Event:      "test_result",
					Suite:      group.Suite,
					Service:    group.Service,
					Group:      group.Name,
					Test:       tc.Name,
					Status:     "skip",
					DurationMs: 0,
					Error:      reason,
				})
				res.Skipped++
			}
			return res
		}
	}

	// Run tests
	for _, tc := range group.Tests {
		if ctx.Err() != nil {
			emit(cancelledEvent{Event: "cancelled", Suite: group.Suite, Group: group.Name, Test: tc.Name})
			res.Cancelled++
			continue
		}
		if tc.NA != "" {
			emit(testResultEvent{
				Event:      "test_result",
				Suite:      group.Suite,
				Service:    group.Service,
				Group:      group.Name,
				Test:       tc.Name,
				Status:     "na",
				DurationMs: 0,
				Error:      tc.NA,
			})
			continue
		}
		if tc.Skip != "" {
			emit(testResultEvent{
				Event:      "test_result",
				Suite:      group.Suite,
				Service:    group.Service,
				Group:      group.Name,
				Test:       tc.Name,
				Status:     "skip",
				DurationMs: 0,
				Error:      tc.Skip,
			})
			res.Skipped++
			continue
		}

		start := time.Now()
		err := tc.Fn(ctx, t)
		elapsed := time.Since(start).Milliseconds()

		ev := testResultEvent{
			Event:      "test_result",
			Suite:      group.Suite,
			Service:    group.Service,
			Group:      group.Name,
			Test:       tc.Name,
			DurationMs: elapsed,
		}

		switch {
		case err == nil:
			ev.Status = "pass"
			res.Passed++
		case IsUnimplemented(err):
			ev.Status = "unimplemented"
			ev.Error = err.Error()
			res.Unimplemented++
		default:
			ev.Status = "fail"
			ev.Error = err.Error()
			res.Failed++
		}

		emit(ev)
	}

	// Teardown phase (always runs)
	if group.Teardown != nil {
		if err := group.Teardown(ctx, t); err != nil {
			fmt.Fprintf(os.Stderr, "harness: teardown %q: %v\n", group.Name, err)
		}
	}

	return res
}

// RunSuite executes all groups in parallel and emits run_start / run_end events.
// Each group receives its own independent TestContext so groups do not share
// state and can run concurrently without races.
func RunSuite(ctx context.Context, suite string, groups []TestGroup, endpoint, region, runID string) {
	start := time.Now()

	totalTests := 0
	for _, g := range groups {
		totalTests += len(g.Tests)
	}

	emit(runStartEvent{
		Event:      "run_start",
		Suite:      suite,
		StartedAt:  start.UTC().Format(time.RFC3339),
		Endpoint:   endpoint,
		Version:    "1",
		TotalTests: totalTests,
	})

	// Limit concurrent group execution. OVERCAST_COMPAT_PARALLEL_SLOTS is
	// injected by the Go runner; default 8.
	slots := 8
	if v := os.Getenv("OVERCAST_COMPAT_PARALLEL_SLOTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			slots = n
		}
	}
	sem := make(chan struct{}, slots)

	results := make([]GroupResult, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go func(i int, g TestGroup) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Per-group timeout: prevents a single hung HTTP call from
			// blocking this semaphore slot (and thus the whole suite) forever.
			groupCtx, groupCancel := context.WithTimeout(ctx, 5*time.Minute)
			defer groupCancel()
			t := NewTestContext(endpoint, region, runID)
			results[i] = RunGroup(groupCtx, g, t)
		}(i, g)
	}
	wg.Wait()

	var total GroupResult
	for _, r := range results {
		total.Passed += r.Passed
		total.Failed += r.Failed
		total.Skipped += r.Skipped
		total.Unimplemented += r.Unimplemented
	}

	elapsed := time.Since(start).Milliseconds()
	emit(runEndEvent{
		Event:         "run_end",
		Suite:         suite,
		Passed:        total.Passed,
		Failed:        total.Failed,
		Skipped:       total.Skipped,
		Unimplemented: total.Unimplemented,
		DurationMs:    elapsed,
	})
}

// ── Interactive mode types ────────────────────────────────────────────────

// StdinCommand represents a command received on stdin.
type StdinCommand struct {
	Command string    `json:"command"`
	BatchID string    `json:"batch_id,omitempty"`
	Tests   []TestRef `json:"tests,omitempty"`
	Group   string    `json:"group,omitempty"`
	Test    string    `json:"test,omitempty"`
}

// TestRef identifies a group and optional subset of tests.
type TestRef struct {
	Group string   `json:"group"`
	Tests []string `json:"tests,omitempty"`
}

type buildingEvent struct {
	Event   string `json:"event"`
	Suite   string `json:"suite"`
	Message string `json:"message"`
}

type readyEvent struct {
	Event      string `json:"event"`
	Suite      string `json:"suite"`
	TotalTests int    `json:"total_tests"`
}

type batchCompleteEvent struct {
	Event         string `json:"event"`
	Suite         string `json:"suite"`
	BatchID       string `json:"batch_id"`
	Passed        int    `json:"passed"`
	Failed        int    `json:"failed"`
	Skipped       int    `json:"skipped"`
	Unimplemented int    `json:"unimplemented"`
	Cancelled     int    `json:"cancelled"`
	DurationMs    int64  `json:"duration_ms"`
}

type cancelledEvent struct {
	Event   string `json:"event"`
	Suite   string `json:"suite"`
	BatchID string `json:"batch_id,omitempty"`
	Group   string `json:"group,omitempty"`
	Test    string `json:"test,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// EmitBuilding emits a building event during interactive setup.
func EmitBuilding(suite, message string) {
	emit(buildingEvent{Event: "building", Suite: suite, Message: message})
}

// EmitReady emits a ready event when the suite is prepared for commands.
func EmitReady(suite string, totalTests int) {
	emit(readyEvent{Event: "ready", Suite: suite, TotalTests: totalTests})
}

// EmitBatchComplete emits a batch_complete event after a batch finishes.
func EmitBatchComplete(suite, batchID string, totals GroupResult, durationMs int64) {
	emit(batchCompleteEvent{
		Event: "batch_complete", Suite: suite, BatchID: batchID,
		Passed: totals.Passed, Failed: totals.Failed,
		Skipped: totals.Skipped, Unimplemented: totals.Unimplemented,
		Cancelled: totals.Cancelled, DurationMs: durationMs,
	})
}

// EmitPong responds to an orchestrator ping with the currently executing test.
func EmitPong(suite, runningTest string) {
	emit(struct {
		Event       string `json:"event"`
		Suite       string `json:"suite"`
		RunningTest string `json:"running_test"`
	}{Event: "pong", Suite: suite, RunningTest: runningTest})
}

// ReadCommands reads NDJSON commands from stdin and sends them to the returned channel.
// It closes the channel when stdin is closed.
func ReadCommands() <-chan StdinCommand {
	ch := make(chan StdinCommand, 16)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			var cmd StdinCommand
			if err := json.Unmarshal([]byte(line), &cmd); err != nil {
				fmt.Fprintf(os.Stderr, "[harness] invalid JSON on stdin: %s\n", line)
				continue
			}
			ch <- cmd
		}
	}()
	return ch
}
