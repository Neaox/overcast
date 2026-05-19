// Package harness provides the core test framework for the Overcast compat CLI suite.
//
// Tests emit NDJSON events to stdout. Each test calls the AWS CLI via exec.Command
// and fails if the command exits non-zero or produces unexpected output.
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
	Op   string
	Skip string
	// NA, if non-empty, causes the test to be emitted with status "na".
	// Use this when the AWS CLI does not yet expose this operation.
	// NA results are excluded from pass-rate calculations.
	NA string
	// Depends lists test names in the same group that must pass before this
	// test runs.  If any dependency failed or was skipped, this test is
	// automatically skipped.
	Depends []string
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
	fmt.Fprintln(os.Stderr, "[cli]", msg)
}

// Reset clears the state bag (called between groups).
func (t *TestContext) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = make(map[string]any)
}

// IsUnimplemented reports whether an error represents a 501 / not-implemented response.
func IsUnimplemented(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "501") ||
		strings.Contains(s, "NotImplemented") ||
		strings.Contains(s, "UnknownOperationException") ||
		strings.Contains(s, "UnsupportedOperation") ||
		strings.Contains(s, "not implemented")
}

// ─── NDJSON events ────────────────────────────────────────────────────────────

// emitMu serialises writes to stdout so concurrent goroutines (parallel
// group execution) never interleave partial NDJSON lines.
var emitMu sync.Mutex

func emit(obj any) {
	b, _ := json.Marshal(obj)
	emitMu.Lock()
	os.Stdout.Write(b)
	os.Stdout.Write([]byte("\n"))
	emitMu.Unlock()
}

// GroupCounts holds the result totals from a single RunGroup call.
type GroupCounts struct {
	Passed, Failed, Skipped, Unimplemented, Cancelled int
}

// RunGroup executes a single TestGroup, emitting one test_result per test.
// It returns the aggregate counts for the caller to roll up into run_end.
func RunGroup(ctx context.Context, g TestGroup) GroupCounts {
	t := NewTestContext("", "", "")

	// Extract endpoint/region/runID from context values if present.
	if v, ok := ctx.Value(ctxEndpoint{}).(string); ok {
		t.Endpoint = v
	}
	if v, ok := ctx.Value(ctxRegion{}).(string); ok {
		t.Region = v
	}
	if v, ok := ctx.Value(ctxRunID{}).(string); ok {
		t.RunID = v
	}

	var counts GroupCounts
	failedOrSkipped := map[string]bool{}

	if g.Setup != nil {
		if err := g.Setup(ctx, t); err != nil {
			emit(map[string]any{"event": "group_setup_error", "suite": g.Suite, "group": g.Name, "error": err.Error()})
			for _, tc := range g.Tests {
				emit(map[string]any{
					"event": "test_result", "suite": g.Suite, "service": g.Service,
					"group": g.Name, "test": tc.Name, "status": "skip",
					"error": fmt.Sprintf("setup failed: %v", err), "duration_ms": 0,
				})
				counts.Skipped++
			}
			return counts
		}
	}

	for _, tc := range g.Tests {
		if ctx.Err() != nil {
			emit(cancelledEvent{Event: "cancelled", Suite: g.Suite, Group: g.Name, Test: tc.Name})
			counts.Cancelled++
			continue
		}
		if tc.NA != "" {
			emit(map[string]any{
				"event": "test_result", "suite": g.Suite, "service": g.Service,
				"group": g.Name, "test": tc.Name, "status": "na",
				"error": tc.NA, "duration_ms": 0,
			})
			// na is excluded from pass-rate counters
			continue
		}
		if tc.Skip != "" {
			emit(map[string]any{
				"event": "test_result", "suite": g.Suite, "service": g.Service,
				"group": g.Name, "test": tc.Name, "status": "skip",
				"error": tc.Skip, "duration_ms": 0,
			})
			counts.Skipped++
			failedOrSkipped[tc.Name] = true
			continue
		}

		// Dependency gate — skip if any declared dependency failed or was skipped.
		if len(tc.Depends) > 0 {
			var failedDeps []string
			for _, dep := range tc.Depends {
				if failedOrSkipped[dep] {
					failedDeps = append(failedDeps, dep)
				}
			}
			if len(failedDeps) > 0 {
				emit(map[string]any{
					"event": "test_result", "suite": g.Suite, "service": g.Service,
					"group": g.Name, "test": tc.Name, "status": "skip",
					"error":       fmt.Sprintf("dependency failed: %s", strings.Join(failedDeps, ", ")),
					"duration_ms": 0,
				})
				counts.Skipped++
				failedOrSkipped[tc.Name] = true
				continue
			}
		}

		start := time.Now()
		err := tc.Fn(ctx, t)
		durMs := time.Since(start).Milliseconds()

		if err == nil {
			emit(map[string]any{
				"event": "test_result", "suite": g.Suite, "service": g.Service,
				"group": g.Name, "test": tc.Name, "status": "pass", "duration_ms": durMs,
			})
			counts.Passed++
		} else if IsUnimplemented(err) {
			emit(map[string]any{
				"event": "test_result", "suite": g.Suite, "service": g.Service,
				"group": g.Name, "test": tc.Name, "status": "unimplemented", "duration_ms": durMs,
			})
			counts.Unimplemented++
			failedOrSkipped[tc.Name] = true
		} else {
			emit(map[string]any{
				"event": "test_result", "suite": g.Suite, "service": g.Service,
				"group": g.Name, "test": tc.Name, "status": "fail",
				"error": err.Error(), "duration_ms": durMs,
			})
			counts.Failed++
			failedOrSkipped[tc.Name] = true
		}
	}

	if g.Teardown != nil {
		if err := g.Teardown(ctx, t); err != nil {
			emit(map[string]any{"event": "group_teardown_error", "suite": g.Suite, "group": g.Name, "error": err.Error()})
		}
	}
	return counts
}

// RunSuite executes all groups in parallel.
// RunGroup already creates its own TestContext per group, so no shared state
// is at risk from concurrent execution.
func RunSuite(suite string, groups []TestGroup, endpoint, region, runID string) {
	ctx := context.WithValue(context.Background(), ctxEndpoint{}, endpoint)
	ctx = context.WithValue(ctx, ctxRegion{}, region)
	ctx = context.WithValue(ctx, ctxRunID{}, runID)

	total := 0
	for _, g := range groups {
		total += len(g.Tests)
	}
	startedAt := time.Now()
	emit(map[string]any{
		"event": "run_start", "suite": suite, "run_id": runID,
		"total_tests": total, "endpoint": endpoint,
		"started_at": startedAt.UTC().Format(time.RFC3339),
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

	// Pre-allocate one slot per group so goroutines can write without locking.
	groupResults := make([]GroupCounts, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go func(i int, g TestGroup) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Per-group timeout: prevents a single hung CLI call from
			// blocking this semaphore slot (and thus the whole suite) forever.
			groupCtx, groupCancel := context.WithTimeout(ctx, 5*time.Minute)
			defer groupCancel()
			groupResults[i] = RunGroup(groupCtx, g)
		}(i, g)
	}
	wg.Wait()

	var passed, failed, skipped, unimplemented int
	for _, c := range groupResults {
		passed += c.Passed
		failed += c.Failed
		skipped += c.Skipped
		unimplemented += c.Unimplemented
	}
	emit(map[string]any{
		"event": "run_end", "suite": suite, "run_id": runID,
		"passed": passed, "failed": failed, "skipped": skipped,
		"unimplemented": unimplemented,
		"duration_ms":   time.Since(startedAt).Milliseconds(),
	})
}

// context key types.
type ctxEndpoint struct{}
type ctxRegion struct{}
type ctxRunID struct{}

// NewRunContext returns a context with endpoint, region, and runID values
// that RunGroup will extract.
func NewRunContext(ctx context.Context, endpoint, region, runID string) context.Context {
	ctx = context.WithValue(ctx, ctxEndpoint{}, endpoint)
	ctx = context.WithValue(ctx, ctxRegion{}, region)
	ctx = context.WithValue(ctx, ctxRunID{}, runID)
	return ctx
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
func EmitBatchComplete(suite, batchID string, totals GroupCounts, durationMs int64) {
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
