// cmd/compat/main.go — Overcast compatibility test CLI.
//
// Runs one or more test suite subprocesses, collects their NDJSON output, and
// prints a summary report.  When --serve is set a live compatibility dashboard
// is served on --port (default 7777).
//
// Usage:
//
//	go run ./cmd/compat [flags]
//	go build -o bin/compat ./cmd/compat
//
// Flags:
//
//	--endpoint    Overcast base URL (default: http://localhost:4566)
//	--region      AWS region (default: us-east-1)
//	--suite       Comma-separated suite names to run (default: all)
//	--format      Output format: pretty | json (default: pretty)
//	--serve       Start the compatibility dashboard HTTP server
//	--port        Dashboard listen address (default: :7777)
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Neaox/overcast/compat"
)

func main() {
	endpoint := flag.String("endpoint", envOr("OVERCAST_ENDPOINT", "http://localhost:4566"), "Overcast base URL")
	region := flag.String("region", envOr("OVERCAST_DEFAULT_REGION", "us-east-1"), "AWS region")
	suiteFlag := flag.String("suite", "", "Comma-separated suite names to run (empty = all)")
	format := flag.String("format", envOr("OVERCAST_COMPAT_FORMAT", "pretty"), "Output format: pretty|json|agent")
	serve := flag.Bool("serve", false, "Start the compatibility dashboard HTTP server")
	port := flag.String("port", envOr("OVERCAST_COMPAT_PORT", ":7777"), "Dashboard listen address (e.g. :7777)")
	resultsFile := flag.String("results-file", envOr("OVERCAST_COMPAT_RESULTS_FILE", "compat-results.json"), "Path to persist last run results (empty to disable)")
	agentReportFile := flag.String("agent-report-file", envOr("OVERCAST_COMPAT_AGENT_REPORT", "compat-report.txt"), "Path to write the agent-friendly text report after each run (empty to disable)")
	reportMode := flag.Bool("report", false, "Read an existing results file and print an agent-friendly summary (no tests are run)")
	interactive := flag.Bool("interactive", false, "Start in interactive mode (long-lived suite processes)")
	noUI := flag.Bool("no-ui", false, "Don't serve embedded UI (use with external Vite dev server)")
	flag.Parse()

	// --report: parse an existing file and summarise it for agents, then exit.
	if *reportMode {
		path := *resultsFile
		if path == "" {
			path = "compat-results.json"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "compat: cannot read %s: %v\n", path, err)
			os.Exit(2)
		}
		var rep compat.RunReport
		if err := json.Unmarshal(data, &rep); err != nil {
			fmt.Fprintf(os.Stderr, "compat: cannot parse %s: %v\n", path, err)
			os.Exit(2)
		}
		printAgentReport(&rep)
		return
	}

	// Normalise port: accept bare number like "7777" as well as ":7777".
	addr := *port
	if len(addr) > 0 && addr[0] != ':' {
		addr = ":" + addr
	}

	var suites []string
	if *suiteFlag != "" {
		for _, s := range strings.Split(*suiteFlag, ",") {
			if t := strings.TrimSpace(s); t != "" {
				suites = append(suites, t)
			}
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// --interactive: long-lived suite processes with orchestrator control.
	if *serve && *interactive {
		var ui fs.FS
		if !*noUI {
			ui = uiFS
		}
		srv := compat.NewServer(ui)
		if *resultsFile != "" {
			if err := srv.LoadResultsFile(*resultsFile); err != nil {
				fmt.Fprintf(os.Stderr, "compat: warning: %v\n", err)
			}
		}

		configs := compat.DefaultSuiteConfigs(*endpoint, *region)
		if len(suites) > 0 {
			configs = compat.FilterSuiteConfigs(configs, suites)
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		orch := compat.NewOrchestrator(ctx, configs, srv.Broadcast, logger)
		orch.Endpoint = *endpoint
		orch.Region = *region
		srv.SetOrchestrator(orch)

		if err := orch.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "compat: orchestrator start: %v\n", err)
			os.Exit(2)
		}

		httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()} //nolint:gosec
		go func() {
			fmt.Fprintf(os.Stderr, "compat: interactive dashboard listening on http://localhost%s\n", addr)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "compat: server error: %v\n", err)
			}
		}()

		<-ctx.Done()
		orch.Shutdown()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		return
	}

	// --serve: start the live dashboard server before running suites.
	var srv *compat.Server
	var httpSrv *http.Server
	if *serve {
		srv = compat.NewServer(uiFS)
		// Pre-populate from disk so the dashboard shows the last run immediately.
		if *resultsFile != "" {
			if err := srv.LoadResultsFile(*resultsFile); err != nil {
				fmt.Fprintf(os.Stderr, "compat: warning: %v\n", err)
			}
		}

		// Register the re-run function so the dashboard can trigger new runs.
		rf := *resultsFile
		arf := *agentReportFile
		srv.SetRunFunc(func(filter compat.RunFilter) error {
			runCfg := compat.RunConfig{
				Endpoint:  *endpoint,
				Region:    *region,
				Suites:    suites,
				Service:   filter.Service,
				Group:     filter.Group,
				Test:      filter.Test,
				TestPairs: filter.TestPairs,
				OnEvent:   srv.Broadcast,
			}
			// A filter may narrow suites further.
			if filter.Suite != "" {
				runCfg.Suites = []string{filter.Suite}
			}
			srv.ResetRun(runCfg.Suites...)
			r2 := compat.NewRunner(runCfg)
			rep, err := r2.Run(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "compat: re-run error: %v\n", err)
				return err
			}
			srv.FinishRun(rep)
			if rf != "" {
				if err := srv.SaveResultsFile(rf); err != nil {
					fmt.Fprintf(os.Stderr, "compat: warning: %v\n", err)
				}
			}
			if arf != "" {
				if err := writeAgentReportFile(arf, rep); err != nil {
					fmt.Fprintf(os.Stderr, "compat: warning: %v\n", err)
				}
			}
			return nil
		})

		httpSrv = &http.Server{Addr: addr, Handler: srv.Handler()} //nolint:gosec
		go func() {
			fmt.Fprintf(os.Stderr, "compat: dashboard listening on http://localhost%s\n", addr)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "compat: server error: %v\n", err)
			}
		}()
	}

	cfg := compat.RunConfig{
		Endpoint: *endpoint,
		Region:   *region,
		Suites:   suites,
	}
	if srv != nil {
		cfg.OnEvent = srv.Broadcast
	}

	runner := compat.NewRunner(cfg)
	if srv != nil {
		// Broadcast run_reset before the initial run so the UI knows which
		// suites are about to refresh and can keep the others visible.
		srv.ResetRun(runner.Suites()...)
	}
	report, err := runner.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compat: fatal: %v\n", err)
		os.Exit(2)
	}

	if srv != nil {
		srv.FinishRun(report)
		if *resultsFile != "" {
			if err := srv.SaveResultsFile(*resultsFile); err != nil {
				fmt.Fprintf(os.Stderr, "compat: warning: %v\n", err)
			}
		}
		if *agentReportFile != "" {
			if err := writeAgentReportFile(*agentReportFile, report); err != nil {
				fmt.Fprintf(os.Stderr, "compat: warning: %v\n", err)
			}
		}
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "compat: json encode: %v\n", err)
			os.Exit(2)
		}
	case "junit":
		if err := printJUnit(report); err != nil {
			fmt.Fprintf(os.Stderr, "compat: junit: %v\n", err)
			os.Exit(2)
		}
	case "agent":
		printAgentReport(report)
	default:
		printPretty(report)
	}

	// When --serve is active, keep the server alive so users can review results.
	if *serve {
		fmt.Fprintf(os.Stderr, "compat: run complete — dashboard still at http://localhost%s (Ctrl+C to quit)\n", addr)
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}

	// Always exit 0: individual test failures are expected coverage gaps.
	// Only infrastructure errors (handled above) use non-zero exit codes.
}

func printPretty(report *compat.RunReport) {
	fmt.Printf("Overcast Compatibility Report\n")
	fmt.Printf("Endpoint: %s\n", report.Endpoint)
	fmt.Printf("Duration: %s\n\n", report.FinishedAt.Sub(report.StartedAt).Round(1e6))

	var totalPass, totalFail, totalSkip int

	for _, sr := range report.Suites {
		fmt.Printf("Suite: %s\n", sr.Suite)
		fmt.Printf("  %-40s %6s %6s %6s\n", "Group", "Pass", "Fail", "Skip")
		fmt.Printf("  %-40s %6s %6s %6s\n", strings.Repeat("-", 40), "------", "------", "------")
		for _, gr := range sr.Groups {
			prefix := "✓"
			if gr.Failed > 0 {
				prefix = "✗"
			}
			fmt.Printf("  %s %-38s %6d %6d %6d\n", prefix, gr.Name, gr.Passed, gr.Failed, gr.Skipped)
			if gr.Failed > 0 {
				for _, t := range gr.Tests {
					if t.Status == compat.StatusFail {
						msg := t.Error
						if len(msg) > 120 {
							msg = msg[:117] + "..."
						}
						fmt.Printf("      ✗ %s: %s\n", t.Test, msg)
					}
				}
			}
		}
		fmt.Printf("\n  Total: %d passed, %d failed, %d skipped\n\n",
			sr.Passed, sr.Failed, sr.Skipped)
		totalPass += sr.Passed
		totalFail += sr.Failed
		totalSkip += sr.Skipped
	}

	fmt.Printf("Overall: %d passed, %d failed, %d skipped\n",
		totalPass, totalFail, totalSkip)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// writeAgentReportFile writes the agent-friendly text report to path,
// atomically replacing any previous file.
func writeAgentReportFile(path string, report *compat.RunReport) error {
	// Write the temp file in the same directory as path so os.Rename succeeds
	// even when the destination is on a different filesystem from os.TempDir().
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	f, err := os.CreateTemp(dir, ".compat-report-*.txt")
	if err != nil {
		return fmt.Errorf("compat: agent report: %w", err)
	}
	tmp := f.Name()
	old := os.Stdout
	os.Stdout = f
	printAgentReport(report)
	os.Stdout = old
	if err := f.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("compat: agent report: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("compat: agent report: %w", err)
	}
	return nil
}

// printAgentReport prints a terse, structured summary optimised for AI agents.
// It groups results by action category so an agent can quickly decide what to
// implement or fix next.
//
// Categories:
//  1. Suite totals (one line per suite).
//  2. Unimplemented services — emulator returns 501; map to internal/services/<svc>.
//  3. Genuine failures — assertion errors that should not be failing.
//  4. Cascade failures — failed only because an earlier step in the same group failed.
func printAgentReport(report *compat.RunReport) {
	fmt.Printf("=== Compat Results — %s ===\n", report.StartedAt.UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Printf("Endpoint: %s   Duration: %s\n\n",
		report.Endpoint, report.FinishedAt.Sub(report.StartedAt).Round(1e6))

	// --- Suite totals ---
	fmt.Printf("%-16s %5s %5s %7s %5s\n", "SUITE", "Pass", "Fail", "Unimpl", "Skip")
	fmt.Printf("%-16s %5s %5s %7s %5s\n", strings.Repeat("-", 16), "-----", "-----", "-------", "-----")
	for _, sr := range report.Suites {
		fmt.Printf("%-16s %5d %5d %7d %5d\n",
			sr.Suite, sr.Passed, sr.Failed, sr.Unimplemented, sr.Skipped)
	}
	fmt.Println()

	// --- Unimplemented: keyed by service, then op name → suites that saw it ---
	// Key: "service/op"  Value: set of suite names
	type opKey struct{ service, op string }
	unimplOps := make(map[opKey]map[string]struct{})
	for _, sr := range report.Suites {
		for _, gr := range sr.Groups {
			for _, t := range gr.Tests {
				if t.Status != compat.StatusUnimplemented {
					continue
				}
				k := opKey{service: gr.Service, op: t.Test}
				if unimplOps[k] == nil {
					unimplOps[k] = make(map[string]struct{})
				}
				unimplOps[k][sr.Suite] = struct{}{}
			}
		}
	}

	// Group by service.
	type serviceOp struct {
		op     string
		suites []string
	}
	svcOps := make(map[string][]serviceOp)
	for k, suiteSet := range unimplOps {
		var ss []string
		for s := range suiteSet {
			ss = append(ss, s)
		}
		sort.Strings(ss)
		svcOps[k.service] = append(svcOps[k.service], serviceOp{op: k.op, suites: ss})
	}
	svcs := make([]string, 0, len(svcOps))
	for s := range svcOps {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)

	if len(svcs) > 0 {
		fmt.Println("UNIMPLEMENTED SERVICES  (emulator returns 501 — implement in internal/services/<service>/)")
		for _, svc := range svcs {
			ops := svcOps[svc]
			sort.Slice(ops, func(i, j int) bool { return ops[i].op < ops[j].op })
			fmt.Printf("  %-22s → internal/services/%s/\n", svc, svc)
			for _, op := range ops {
				fmt.Printf("    %-28s [%s]\n", op.op, strings.Join(op.suites, ", "))
			}
		}
		fmt.Println()
	}

	// --- Genuine failures vs cascade failures.
	// A test is a cascade failure when its group already had an earlier failure
	// and the error contains phrases like "no <resource> from <PreviousOp>".
	type failEntry struct {
		suite   string
		group   string
		test    string
		err     string
		cascade bool
	}
	var fails []failEntry
	for _, sr := range report.Suites {
		for _, gr := range sr.Groups {
			// Track which tests in this group failed so we can detect cascades.
			groupFailed := false
			for _, t := range gr.Tests {
				if t.Status != compat.StatusFail {
					continue
				}
				cascade := groupFailed && isCascadeError(t.Error)
				fails = append(fails, failEntry{
					suite:   sr.Suite,
					group:   gr.Name,
					test:    t.Test,
					err:     t.Error,
					cascade: cascade,
				})
				groupFailed = true
			}
		}
	}

	var genuine, cascades []failEntry
	for _, f := range fails {
		if f.cascade {
			cascades = append(cascades, f)
		} else {
			genuine = append(genuine, f)
		}
	}

	if len(genuine) > 0 {
		fmt.Println("GENUINE FAILURES  (should work but doesn't — investigate emulator implementation)")
		for _, f := range genuine {
			msg := f.err
			if len(msg) > 160 {
				msg = msg[:157] + "..."
			}
			fmt.Printf("  %s/%s/%s\n    → %s\n", f.suite, f.group, f.test, msg)
		}
		fmt.Println()
	}

	if len(cascades) > 0 {
		fmt.Println("CASCADE FAILURES  (caused by a genuine failure above — fix that first)")
		for _, f := range cascades {
			fmt.Printf("  %s/%s/%s\n", f.suite, f.group, f.test)
		}
		fmt.Println()
	}

	total := len(genuine) + len(cascades)
	if total == 0 && len(svcs) == 0 {
		fmt.Println("All tests passed.")
	}
}

// isCascadeError returns true when the error message is characteristic of a
// test that failed only because a previous step in the same group failed
// (e.g. "no bucket from CreateBucket", "no queue from CreateQueue").
func isCascadeError(msg string) bool {
	lower := strings.ToLower(msg)
	markers := []string{" from create", " from register", " from put", " from start", "no cluster from", "no state machine from", "no function from"}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// JUnit XML output — CI-friendly format (GitLab, Jenkins, etc.)
// ---------------------------------------------------------------------------

type junitXML struct {
	XMLName xml.Name       `xml:"testsuites"`
	Suites  []junitSuite   `xml:"testsuite"`
}

type junitSuite struct {
	Name     string        `xml:"name,attr"`
	Tests    int           `xml:"tests,attr"`
	Failures int           `xml:"failures,attr"`
	Errors   int           `xml:"errors,attr"`
	Skipped  int           `xml:"skipped,attr"`
	Time     float64       `xml:"time,attr"`
	Cases    []junitCase   `xml:"testcase"`
}

type junitCase struct {
	Name      string     `xml:"name,attr"`
	ClassName string     `xml:"classname,attr"`
	Time      float64    `xml:"time,attr"`
	Failure   *junitFail `xml:"failure,omitempty"`
	Skipped   *junitSkip `xml:"skipped,omitempty"`
	Error     *junitFail `xml:"error,omitempty"`
}

type junitFail struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type junitSkip struct {
	Message string `xml:"message,attr"`
}

func printJUnit(report *compat.RunReport) error {
	fmt.Fprint(os.Stdout, xml.Header)

	var j junitXML
	for _, sr := range report.Suites {
		if sr == nil {
			continue
		}
		js := junitSuite{
			Name: sr.Suite,
			Time: report.FinishedAt.Sub(report.StartedAt).Seconds(),
		}
		for _, gr := range sr.Groups {
			for _, tr := range gr.Tests {
				js.Tests++
				tc := junitCase{
					Name:      gr.Name + "." + tr.Test,
					ClassName: sr.Suite + "." + gr.Name,
					Time:      float64(tr.DurationMS) / 1000.0,
				}
				switch tr.Status {
				case "fail":
					js.Failures++
					tc.Failure = &junitFail{
						Message: tr.Test + " failed",
						Body:    tr.Error,
					}
				case "skip", "na":
					js.Skipped++
					msg := tr.Error
					if msg == "" {
						msg = "skipped"
					}
					tc.Skipped = &junitSkip{Message: msg}
				case "unimplemented":
					js.Skipped++
					tc.Skipped = &junitSkip{Message: "unimplemented: " + tr.Error}
				}
				js.Cases = append(js.Cases, tc)
			}
		}
		j.Suites = append(j.Suites, js)
	}

	enc := xml.NewEncoder(os.Stdout)
	enc.Indent("", "  ")
	return enc.Encode(j)
}
