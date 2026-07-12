// Package compat provides the runner that orchestrates per-language test suite
// subprocesses and aggregates their NDJSON output into a RunReport.
//
// Each suite is an executable (or docker image) that writes NDJSON events to
// stdout. The runner starts each suite subprocess, reads its stdout line by
// line, and builds a live RunReport. Suite stderr is forwarded to the runner's
// own stderr as log lines.
//
// Usage:
//
//	r := compat.NewRunner(cfg)
//	report, err := r.Run(ctx)
package compat

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// SuiteConfig describes a single test suite subprocess.
type SuiteConfig struct {
	// Name is the suite identifier, e.g. "node-js-sdk".
	Name string
	// Argv is the command + arguments to run.
	// The first element is the executable; the rest are arguments.
	// The executable is looked up on PATH.
	Argv []string
	// Env is additional environment variables (KEY=VALUE).
	// OVERCAST_ENDPOINT and OVERCAST_DEFAULT_REGION are always injected by the runner.
	Env []string
	// Dir is the working directory for the subprocess.
	// If empty, the runner's working directory is used.
	Dir string
	// Interactive indicates this suite supports the interactive NDJSON
	// stdin/stdout protocol (building → ready → run commands).
	// Suites without this flag are skipped by the orchestrator.
	Interactive bool
}

// RunConfig controls how the runner executes suites.
type RunConfig struct {
	// Endpoint is the Overcast base URL, e.g. "http://localhost:4566".
	Endpoint string
	// Region is the AWS region to advertise to suite clients.
	Region string
	// Suites lists which suites to run. An empty slice runs all registered suites.
	Suites []string
	// Service filters runs to a single AWS service (e.g. "s3"). Empty = all.
	Service string
	// Group filters runs to a single test group (e.g. "s3-crud"). Empty = all.
	Group string
	// Test filters runs to a single test within a group. Empty = all.
	// Only meaningful when Group is also set.
	Test string
	// TestPairs restricts the run to specific (group, test) pairs.
	// Format: ["groupName:testName", ...]. When set, Service/Group/Test filters
	// are ignored — the pairs are the authoritative list.
	TestPairs []string
	// RunID is the unique identifier for this run, injected into all suite
	// subprocesses as OVERCAST_COMPAT_RUN_ID. All test resources must be
	// prefixed with this ID so the post-run orphan sweep can detect leaks.
	// If empty, a random ID is generated in Run().
	RunID string
	// OnEvent is an optional callback invoked with each raw NDJSON event line
	// as it is received from a suite subprocess. The byte slice is a stable
	// copy and may be retained by the caller. Invoked from a single goroutine.
	OnEvent func(raw []byte)
}

// Runner orchestrates suite subprocesses.
type Runner struct {
	cfg       RunConfig
	suites    []SuiteConfig
	logWriter io.Writer // defaults to os.Stderr
}

// NewRunner creates a Runner pre-loaded with the default suite set.
func NewRunner(cfg RunConfig) *Runner {
	r := &Runner{
		cfg:       cfg,
		logWriter: os.Stderr,
	}
	r.suites = r.defaultSuites()
	return r
}

// WithLogWriter redirects runner log output (default: os.Stderr).
func (r *Runner) WithLogWriter(w io.Writer) *Runner {
	r.logWriter = w
	return r
}

// Suites returns the names of the suites that will be executed by this runner,
// applying any name filter from RunConfig.Suites. Useful for callers that need
// to know the resolved suite list before calling Run (e.g. to call ResetRun).
func (r *Runner) Suites() []string {
	src := r.suites
	if len(r.cfg.Suites) > 0 {
		src = FilterSuiteConfigs(src, r.cfg.Suites)
	}
	names := make([]string, len(src))
	for i, s := range src {
		names[i] = s.Name
	}
	return names
}

// DefaultSuiteConfigs returns the built-in suite configuration list with
// endpoint and region injected into each suite's environment. This is the
// public entry point for callers (e.g. cmd/compat interactive mode) that
// need to construct suite configs without creating a full Runner.
func DefaultSuiteConfigs(endpoint, region string) []SuiteConfig {
	r := &Runner{}
	configs := r.defaultSuites()
	for i := range configs {
		configs[i].Env = append(configs[i].Env,
			"OVERCAST_ENDPOINT="+endpoint,
			"OVERCAST_DEFAULT_REGION="+region,
		)
	}
	return configs
}

// defaultSuites returns the built-in suite list.
// Suites that are filtered out by RunConfig.Suites are skipped in Run().
func (r *Runner) defaultSuites() []SuiteConfig {
	return []SuiteConfig{
		{
			Name:        "node-js-sdk",
			Argv:        []string{"node", "--import", "tsx/esm", "src/runner.ts"},
			Dir:         "compat/suites/node-js-sdk",
			Interactive: true,
		},
		{
			Name:        "python-sdk",
			Argv:        []string{"python3", "runner.py"},
			Dir:         "compat/suites/python-sdk",
			Interactive: true,
		},
		{
			Name:        "go-sdk",
			Argv:        []string{"go", "run", "./cmd/runner"},
			Dir:         "compat/suites/go-sdk",
			Interactive: true,
		},
		{
			Name:        "cli",
			Argv:        []string{"go", "run", "./cmd/runner"},
			Dir:         "compat/suites/cli",
			Interactive: true,
		},
		{
			Name:        "cdk",
			Argv:        []string{"node", "--import", "tsx/esm", "src/runner.ts"},
			Dir:         "compat/suites/cdk",
			Interactive: true,
		},
		{
			Name:        "java-sdk",
			Argv:        []string{"sh", "run.sh"},
			Dir:         "compat/suites/java-sdk",
			Interactive: true,
		},
		{
			Name:        "dotnet-sdk",
			Argv:        []string{"sh", "run.sh"},
			Dir:         "compat/suites/dotnet-sdk",
			Interactive: true,
		},
		{
			Name:        "rust-sdk",
			Argv:        []string{"sh", "run.sh"},
			Dir:         "compat/suites/rust-sdk",
			Interactive: true,
		},
	}
}

// Run starts each configured suite subprocess in parallel, reads their NDJSON
// output, and returns an aggregated RunReport. Suites are independent OS
// processes with no shared state so concurrent execution is safe.
// OnEvent is called under a mutex so callers receive events from all suites
// on a single goroutine (same contract as the previous sequential Run).
func (r *Runner) Run(ctx context.Context) (*RunReport, error) {
	if r.cfg.RunID == "" {
		r.cfg.RunID = makeCompatRunID()
	}

	report := &RunReport{
		Endpoint:  r.cfg.Endpoint,
		StartedAt: time.Now(),
	}

	suites := r.suites
	if len(r.cfg.Suites) > 0 {
		suites = FilterSuiteConfigs(suites, r.cfg.Suites)
	}

	// Compute a per-suite parallelism budget so that running all suites
	// concurrently doesn't overwhelm the emulator.  Target ≤ max(8, 2×CPU)
	// total concurrent group executions, divided evenly across suites.
	totalSlots := min(max(8, runtime.NumCPU()*2), 40)
	slotsPerSuite := max(1, totalSlots/len(suites))

	// Pre-allocate slice so results land at stable indices regardless of
	// completion order, preserving deterministic suite ordering in the report.
	results := make([]*SuiteReport, len(suites))

	// Serialise OnEvent calls across goroutines — the public contract says
	// OnEvent is invoked from a single goroutine; the mutex upholds that.
	var eventMu sync.Mutex
	safeOnEvent := r.cfg.OnEvent
	if safeOnEvent != nil {
		r.cfg.OnEvent = func(raw []byte) {
			eventMu.Lock()
			safeOnEvent(raw)
			eventMu.Unlock()
		}
	}

	// Pre-run cleanup: delete orphaned resources from prior runs so suites
	// start with a clean slate and avoid "already exists" errors.
	fmt.Fprintf(r.logWriter, "compat: sweeping orphaned resources before run…\n")
	sweepOrphans(ctx, r.cfg.Endpoint, r.logWriter)

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var suiteErrs []string
	for i, s := range suites {
		wg.Add(1)
		go func(i int, s SuiteConfig) {
			defer wg.Done()
			sr, err := r.runSuite(ctx, s, slotsPerSuite)
			if err != nil {
				// Log to stderr and emit a suite_error event so the UI can show it.
				fmt.Fprintf(r.logWriter, "compat: suite %q failed to run: %v\n", s.Name, err)
				errMu.Lock()
				suiteErrs = append(suiteErrs, err.Error())
				errMu.Unlock()
				if r.cfg.OnEvent != nil {
					data, _ := json.Marshal(struct {
						Event string `json:"event"`
						Suite string `json:"suite"`
						Error string `json:"error"`
					}{Event: string(EventSuiteError), Suite: s.Name, Error: err.Error()})
					r.cfg.OnEvent(data)
				}
			}
			results[i] = sr
		}(i, s)
	}
	wg.Wait()

	// Post-run cleanup: delete any resources left behind by suite teardowns.
	sweepOrphans(ctx, r.cfg.Endpoint, r.logWriter)

	for _, sr := range results {
		if sr != nil {
			report.Suites = append(report.Suites, sr)
		}
	}

	report.FinishedAt = time.Now()
	if len(suiteErrs) > 0 {
		return report, fmt.Errorf("compat: suite infrastructure failure(s): %s", strings.Join(suiteErrs, "; "))
	}
	return report, nil
}

// runSuite starts a single suite subprocess and parses its NDJSON output.
// parallelSlots controls how many test groups the suite may run concurrently
// (injected as OVERCAST_COMPAT_PARALLEL_SLOTS into the subprocess environment).
func (r *Runner) runSuite(ctx context.Context, s SuiteConfig, parallelSlots int) (*SuiteReport, error) {
	if len(s.Argv) == 0 {
		return nil, fmt.Errorf("suite %q: empty argv", s.Name)
	}

	// Hard timeout per suite: if the subprocess doesn't finish within this
	// limit, kill it.  This is the last-resort safety net — individual group
	// timeouts inside each harness fire first and are more graceful.
	const suiteHardTimeout = 25 * time.Minute
	suiteCtx, suiteCancel := context.WithTimeout(ctx, suiteHardTimeout)
	defer suiteCancel()

	// Each suite gets its own isolated runId so that resources created by
	// parallel suites never conflict. The suite ID is derived from the base
	// runId by appending a short suite abbreviation.
	suiteRunID := r.cfg.RunID
	if s.Name != "" {
		// Map well-known suite names to short stable suffixes.
		abbrevs := map[string]string{
			"node-js-sdk": "nj",
			"python-sdk":  "py",
			"go-sdk":      "go",
			"cli":         "cl",
			"dotnet-sdk":  "dn",
			"java-sdk":    "jv",
			"rust-sdk":    "rs",
		}
		if abbrev, ok := abbrevs[s.Name]; ok {
			suiteRunID = r.cfg.RunID + "-" + abbrev
		}
	}

	//nolint:gosec // argv is from internal config, not user input
	cmd := exec.CommandContext(suiteCtx, s.Argv[0], s.Argv[1:]...)
	if s.Dir != "" {
		cmd.Dir = s.Dir
	}
	cmd.Env = append(
		os.Environ(),
		"OVERCAST_ENDPOINT="+r.cfg.Endpoint,
		"OVERCAST_DEFAULT_REGION="+r.cfg.Region,
		"OVERCAST_COMPAT_RUN_ID="+suiteRunID,
	)
	if r.cfg.Service != "" {
		cmd.Env = append(cmd.Env, "OVERCAST_COMPAT_SERVICE="+r.cfg.Service)
	}
	if r.cfg.Group != "" {
		cmd.Env = append(cmd.Env, "OVERCAST_COMPAT_GROUPS="+r.cfg.Group)
	}
	if r.cfg.Test != "" {
		cmd.Env = append(cmd.Env, "OVERCAST_COMPAT_TESTS="+r.cfg.Test)
	}
	if len(r.cfg.TestPairs) > 0 {
		cmd.Env = append(cmd.Env, "OVERCAST_COMPAT_TEST_PAIRS="+strings.Join(r.cfg.TestPairs, ","))
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("OVERCAST_COMPAT_PARALLEL_SLOTS=%d", parallelSlots))
	cmd.Env = append(cmd.Env, s.Env...)

	// Notify SSE clients that this suite subprocess is about to start.
	if r.cfg.OnEvent != nil {
		data, _ := json.Marshal(struct {
			Event string `json:"event"`
			Suite string `json:"suite"`
		}{Event: string(EventSuiteStarting), Suite: s.Name})
		r.cfg.OnEvent(data)
	}

	// Pipe stdout for NDJSON parsing; capture stderr for error reporting
	// while still forwarding it to the log writer.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("suite %q: stdout pipe: %w", s.Name, err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(r.logWriter, &stderrBuf)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("suite %q: start: %w", s.Name, err)
	}

	sr := parseNDJSON(stdout, s.Name, r.cfg.OnEvent)

	// Wait for process exit.
	waitErr := cmd.Wait()

	// Detect a suite that crashed without producing any test results.
	// A non-zero exit with zero tests means the suite itself failed (e.g.
	// Docker build error, missing dependency, compilation failure).
	if waitErr != nil && sr.Total() == 0 {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		// Cap error message length so the SSE event stays reasonable.
		if len(errMsg) > 2000 {
			errMsg = errMsg[len(errMsg)-2000:]
		}
		return sr, fmt.Errorf("suite %q: %s", s.Name, errMsg)
	}
	return sr, nil
}

// parseNDJSON reads NDJSON lines from r and builds a SuiteReport.
// It is tolerant: malformed lines are skipped.
// onEvent is called with each raw line (a stable copy) before parsing;
// it may be nil if the caller does not need live event delivery.
//
// Groups are tracked by name in a map so that interleaved output from
// parallel group execution is handled correctly — a group's results are
// appended to the same GroupReport regardless of the order lines arrive.
func parseNDJSON(r io.Reader, suiteName string, onEvent func([]byte)) *SuiteReport {
	sr := &SuiteReport{Suite: suiteName}
	// groups tracks seen groups by name; sr.Groups preserves insertion order.
	groups := make(map[string]*GroupReport)

	scanner := bufio.NewScanner(r)
	// Allow lines up to 1 MiB (large error messages from SDK).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		// Deliver a stable copy to the callback before we parse.
		if onEvent != nil {
			cp := make([]byte, len(line))
			copy(cp, line)
			onEvent(cp)
		}

		var raw RawEvent
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		switch raw.Event {
		case EventTestResult:
			var ev TestResultEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				continue
			}
			gr, ok := groups[ev.Group]
			if !ok {
				gr = &GroupReport{Name: ev.Group, Service: ev.Service}
				sr.Groups = append(sr.Groups, gr)
				groups[ev.Group] = gr
			}
			tr := TestResultEvent{
				Event:      EventTestResult,
				Suite:      ev.Suite,
				Service:    ev.Service,
				Group:      ev.Group,
				Test:       ev.Test,
				Op:         ev.Op,
				Status:     ev.Status,
				DurationMS: ev.DurationMS,
				Error:      ev.Error,
			}
			if gr != nil {
				gr.Tests = append(gr.Tests, tr)
			}
			switch ev.Status {
			case StatusPass:
				sr.Passed++
				if gr != nil {
					gr.Passed++
				}
			case StatusFail:
				sr.Failed++
				if gr != nil {
					gr.Failed++
				}
			case StatusSkip:
				sr.Skipped++
				if gr != nil {
					gr.Skipped++
				}
			case StatusUnimplemented:
				sr.Unimplemented++
				if gr != nil {
					gr.Unimplemented++
				}
			case StatusNA:
				// no-op: N/A results are forwarded to SSE clients but excluded from counts.
			}
		// run_start, test_start, and run_end are informational only — counts are
		// derived from individual test_result events to keep the runner resilient
		// to crashed suite processes that never emit run_end.
		case EventRunStart, EventSuiteStarting, EventSuiteError, EventTestStart, EventRunEnd:
			// no-op
		// StatusNA events are intentionally not counted in any suite totals.
		case "na":
			// no-op: N/A results are forwarded to SSE clients (via onEvent above)
			// but excluded from SuiteReport counts.
		}
	}

	return sr
}

// FilterSuiteConfigs filters a list of suite configs to only those whose Name
// appears in names. Used by cmd/compat to narrow the default configs by --suite.
func FilterSuiteConfigs(all []SuiteConfig, names []string) []SuiteConfig {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	var out []SuiteConfig
	for _, s := range all {
		if set[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// makeCompatRunID returns a unique run identifier in the form "oc-{8 hex chars}".
func makeCompatRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based suffix.
		return fmt.Sprintf("oc-%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return "oc-" + hex.EncodeToString(b)
}
