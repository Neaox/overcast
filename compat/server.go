// Package compat — HTTP server for the compatibility test dashboard.
//
// The server exposes three endpoints:
//
//	GET /events   — Server-Sent Events stream of NDJSON test events.
//	               New clients receive a full replay of the current run so
//	               far, then live events as they arrive.
//	GET /results  — The last completed RunReport as indented JSON.
//	GET /         — Embedded static UI files.
//
// Calls Broadcast(raw) for each NDJSON line from the test runner.
// Call FinishRun(report) once the whole run is done.
// Call ResetRun() to clear the replay buffer at the start of a new run.
package compat

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxReplayBuf is the maximum number of events kept in the SSE replay buffer.
// Events beyond this cap are dropped oldest-first so memory stays bounded.
const maxReplayBuf = 10_000

// RunFilter scopes a re-run to a subset of tests.
// All fields are optional; zero value means "run everything".
type RunFilter struct {
	Suite   string `json:"suite,omitempty"`
	Service string `json:"service,omitempty"`
	Group   string `json:"group,omitempty"`
	Test    string `json:"test,omitempty"`
	// Statuses, when non-empty, restricts the run to tests whose result in the
	// last run matched one of the given statuses (e.g. "fail", "skip",
	// "unimplemented"). The server expands this to TestPairs before calling
	// the run function.
	Statuses []string `json:"statuses,omitempty"`
	// TestPairs is set internally (not decoded from JSON) when Statuses is
	// expanded. Format: ["groupName:testName", ...].
	TestPairs []string `json:"-"`
}

// Server is the compatibility test HTTP server.
// Create via NewServer; all methods are safe for concurrent use.
type Server struct {
	mu           sync.Mutex
	buf          [][]byte                     // replay buffer: raw event bytes, one per event
	last         []byte                       // JSON of last completed RunReport
	clients      map[chan []byte]bool         // live SSE clients
	running      bool                         // true while a run is in progress
	runFn        func(filter RunFilter) error // set via SetRunFunc
	orchestrator *Orchestrator                // nil in legacy mode; set for interactive mode

	uiFS          fs.FS  // embedded or nil; when nil, /dist/ is served from disk
	workspaceRoot string // absolute path of the repo root, exposed via GET /config
}

// NewServer creates a Server backed by optional embedded UI files.
// Pass nil for uiFS to disable static file serving (useful in tests).
func NewServer(uiFS fs.FS) *Server {
	root, _ := os.Getwd()
	return &Server{
		clients:       make(map[chan []byte]bool),
		uiFS:          uiFS,
		workspaceRoot: root,
	}
}

// ResetRun prepares for a new test run.
//
// suites lists which suite names are about to be re-run. If empty, all suites
// are reset (full re-run). For a partial re-run (e.g. just "node-js-sdk"),
// pass only those suite names so the results for other suites are preserved
// in the replay buffer and remain visible in the UI while the new run proceeds.
//
// ResetRun broadcasts a run_reset event to all live clients so the UI can
// mark the affected suites' data as stale while preserving the rest.
func (s *Server) ResetRun(suites ...string) {
	type resetEvent struct {
		Event  string   `json:"event"`
		Suites []string `json:"suites"`
		TS     string   `json:"ts"`
	}
	evBytes, _ := json.Marshal(resetEvent{Event: "run_reset", Suites: suites, TS: time.Now().UTC().Format(time.RFC3339Nano)})

	suiteSet := make(map[string]bool, len(suites))
	for _, s := range suites {
		suiteSet[s] = true
	}

	s.mu.Lock()
	if len(suiteSet) == 0 {
		// Full reset — clear everything.
		s.buf = s.buf[:0]
	} else {
		// Partial reset — remove only events belonging to the affected suites.
		kept := s.buf[:0]
		for _, raw := range s.buf {
			var peek struct {
				Suite string `json:"suite"`
			}
			if json.Unmarshal(raw, &peek) == nil && suiteSet[peek.Suite] {
				continue // drop events from the suites being re-run
			}
			kept = append(kept, raw)
		}
		s.buf = kept
	}
	// Append the run_reset marker so replaying clients see which suites changed.
	s.buf = append(s.buf, evBytes)
	for ch := range s.clients {
		select {
		case ch <- evBytes:
		default:
		}
	}
	s.mu.Unlock()
}

// Broadcast delivers a raw NDJSON event line to all connected SSE clients and
// appends it to the replay buffer for clients that connect later.
// Safe to call from any goroutine.
func (s *Server) Broadcast(raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamped := injectTimestamp(raw)
	if len(s.buf) >= maxReplayBuf {
		// Drop oldest event to keep the buffer bounded.
		copy(s.buf, s.buf[1:])
		s.buf = s.buf[:len(s.buf)-1]
	}
	s.buf = append(s.buf, stamped)
	for ch := range s.clients {
		select {
		case ch <- stamped:
		default:
			// Slow client — drop rather than block the runner.
		}
	}
}

// injectTimestamp adds a "ts" field with the current time (RFC3339Nano, UTC)
// to a raw JSON event line. If the input is not a JSON object, it is returned
// unchanged (with a copy).
func injectTimestamp(raw []byte) []byte {
	// Fast path: if it looks like a JSON object, insert ts before the closing brace.
	// This avoids a full unmarshal/remarshal cycle on every event.
	n := len(raw)
	for n > 0 && (raw[n-1] == '\n' || raw[n-1] == '\r' || raw[n-1] == ' ') {
		n--
	}
	if n > 0 && raw[n-1] == '}' {
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		// ,"ts":"2026-04-03T10:01:28.123456789Z"}
		suffix := `,"ts":"` + ts + `"}`
		out := make([]byte, n-1, n-1+len(suffix))
		copy(out, raw[:n-1])
		out = append(out, suffix...)
		return out
	}
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp
}

// FinishRun stores the completed RunReport for GET /results responses and
// broadcasts a run_complete event to all connected SSE clients.
//
// When report only covers a subset of suites (partial re-run), FinishRun
// merges those results into the existing last report so that GET /results
// always returns the full picture across all suites, not just the ones that
// were just re-run.
func (s *Server) FinishRun(report *RunReport) {
	s.mu.Lock()
	merged := mergeRunReport(s.last, report)
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		s.mu.Unlock()
		return
	}
	s.last = b
	s.mu.Unlock()

	// Broadcast a synthetic event so the UI transitions to "done".
	s.Broadcast([]byte(`{"event":"run_complete"}`))
}

// mergeRunReport merges a partial run report into the existing last report.
// Suites present in partial replace their counterparts in prev; suites only
// in prev are kept unchanged. If prev is nil/empty, partial is returned as-is.
func mergeRunReport(prevJSON []byte, partial *RunReport) *RunReport {
	if len(prevJSON) == 0 || partial == nil {
		return partial
	}
	var prev RunReport
	if err := json.Unmarshal(prevJSON, &prev); err != nil {
		return partial
	}
	// Index suites from the partial run.
	newSuites := make(map[string]*SuiteReport, len(partial.Suites))
	for _, sr := range partial.Suites {
		newSuites[sr.Suite] = sr
	}
	merged := &RunReport{
		Endpoint:   partial.Endpoint,
		StartedAt:  partial.StartedAt,
		FinishedAt: partial.FinishedAt,
	}
	// Keep previous suites that were not re-run.
	for _, sr := range prev.Suites {
		if _, replaced := newSuites[sr.Suite]; !replaced {
			merged.Suites = append(merged.Suites, sr)
		}
	}
	// Append the freshly-run suites.
	merged.Suites = append(merged.Suites, partial.Suites...)
	return merged
}

// SaveResultsFile writes the last completed RunReport to path so it survives
// a server restart. The file is written atomically via a temp-file rename.
func (s *Server) SaveResultsFile(path string) error {
	s.mu.Lock()
	b := s.last
	s.mu.Unlock()
	if len(b) == 0 {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("compat: save results: %w", err)
	}
	return os.Rename(tmp, path)
}

// LoadResultsFile reads a previously saved results file and pre-populates the
// GET /results response so the dashboard shows the last run immediately after a
// server restart, before any new run has been performed.
func (s *Server) LoadResultsFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no prior results — that's fine
		}
		return fmt.Errorf("compat: load results: %w", err)
	}
	// Validate it's a real RunReport before accepting it.
	var report RunReport
	if err := json.Unmarshal(b, &report); err != nil {
		return fmt.Errorf("compat: load results: invalid JSON: %w", err)
	}
	b2, _ := json.MarshalIndent(&report, "", "  ")
	s.mu.Lock()
	s.last = b2
	s.mu.Unlock()
	return nil
}

// SetRunFunc registers the function the server calls when POST /run is received.
// fn is invoked in a new goroutine. It must call ResetRun(), Broadcast(), and
// FinishRun() itself (the main run loop does this naturally). Only one run
// at a time is allowed; POST /run returns 409 if one is already in progress.
func (s *Server) SetRunFunc(fn func(filter RunFilter) error) {
	s.mu.Lock()
	s.runFn = fn
	s.mu.Unlock()
}

// SetOrchestrator attaches the interactive-mode orchestrator to the server.
// When set, POST /run delegates to the orchestrator instead of the legacy
// runFn, and the new /suites, /queue, /cancel, /registry endpoints become
// available.
func (s *Server) SetOrchestrator(o *Orchestrator) {
	s.mu.Lock()
	s.orchestrator = o
	s.mu.Unlock()
}

// SetRunning marks the server as running or idle. Called by the run function
// before and after a run so POST /run can enforce single-concurrency.
func (s *Server) SetRunning(v bool) {
	s.mu.Lock()
	s.running = v
	s.mu.Unlock()
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", s.serveEvents)
	mux.HandleFunc("GET /results", s.serveResults)
	mux.HandleFunc("GET /config", s.serveConfig)
	mux.HandleFunc("POST /open", s.serveOpen)
	mux.HandleFunc("POST /run", s.serveRun)
	mux.HandleFunc("GET /suites", s.serveSuites)
	mux.HandleFunc("POST /cancel", s.serveCancel)
	mux.HandleFunc("POST /reset", s.serveReset)
	mux.HandleFunc("GET /registry", s.serveRegistry)
	mux.HandleFunc("GET /queue", s.serveQueue)
	mux.HandleFunc("POST /reload", s.serveReload)
	if s.orchestrator != nil {
		mcpSrv := NewMCPServer(s.orchestrator, filepath.Join(s.workspaceRoot, "compat", "suites", "registry.json"), s.workspaceRoot, slog.Default())
		mux.Handle("/mcp/", mcpSrv.Handler())
	}
	if s.uiFS != nil {
		mux.Handle("GET /", http.FileServer(http.FS(s.uiFS)))
	}
	return mux
}

// serveEvents streams Server-Sent Events.
// The response is an infinite stream; clients disconnect by closing the request.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	// SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Register the client and get a snapshot of the replay buffer atomically.
	ch := make(chan []byte, 256)
	s.mu.Lock()
	snapshot := make([][]byte, len(s.buf))
	copy(snapshot, s.buf)
	s.clients[ch] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, ch) // remove before close so Broadcast never sends to closed ch
		close(ch)
		s.mu.Unlock()
	}()

	// Replay past events.
	for _, ev := range snapshot {
		if err := writeSSELine(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	// Stream live events.
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSELine(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// serveRun handles POST /run — triggers a new test run with optional filters.
// Returns 409 if a run is already in progress, 501 if SetRunFunc was not called,
// or 202 Accepted when the run has been queued.
//
// In interactive mode (orchestrator is set), the request is delegated to the
// orchestrator's SubmitTests/SubmitFailingTests and returns immediately with
// the batch ID and queue counts.
func (s *Server) serveRun(w http.ResponseWriter, r *http.Request) {
	var filter RunFilter
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&filter); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
	}

	// Interactive mode: delegate to orchestrator.
	s.mu.Lock()
	orch := s.orchestrator
	s.mu.Unlock()

	if orch != nil {
		s.serveRunInteractive(w, orch, filter)
		return
	}

	// Legacy mode: use runFn.

	// Expand Statuses into concrete TestPairs using the last run results.
	if len(filter.Statuses) > 0 {
		filter.TestPairs = s.testPairsForStatuses(filter)
		filter.Statuses = nil
	}

	s.mu.Lock()
	fn := s.runFn
	alreadyRunning := s.running
	if fn != nil && !alreadyRunning {
		s.running = true
	}
	s.mu.Unlock()

	if fn == nil {
		http.Error(w, `{"error":"run function not configured"}`, http.StatusNotImplemented)
		return
	}
	if alreadyRunning {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"run already in progress"}`))
		return
	}

	go func() {
		defer s.SetRunning(false)
		_ = fn(filter)
	}()

	w.WriteHeader(http.StatusAccepted)
}

// serveRunInteractive handles POST /run when the orchestrator is active.
func (s *Server) serveRunInteractive(w http.ResponseWriter, orch *Orchestrator, filter RunFilter) {
	// Re-run tests matching the requested statuses.
	// Prefer in-memory results (o.results, populated during the current server
	// lifetime) but fall back to the persisted last-run report so retries work
	// after a server restart.
	if len(filter.Statuses) > 0 {
		statuses := make([]Status, len(filter.Statuses))
		for i, st := range filter.Statuses {
			statuses[i] = Status(st)
		}
		batchID, queued := orch.SubmitFailingTests(filter.Suite, filter.Service, statuses...)
		if batchID == "" {
			// o.results was empty (e.g. server restarted); fall back to the
			// persisted run report so retries always work.
			batchID, queued, _ = s.submitFailingFromReport(orch, filter)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"batch_id":           batchID,
			"queued":             queued,
			"skipped_duplicates": 0,
		})
		return
	}

	// Convert RunFilter into TestRef slice.
	var tests []TestRef
	var suites []string

	if filter.Suite != "" {
		suites = []string{filter.Suite}
	}

	if len(filter.TestPairs) > 0 {
		groupTests := make(map[string][]string)
		for _, pair := range filter.TestPairs {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				groupTests[parts[0]] = append(groupTests[parts[0]], parts[1])
			}
		}
		for group, tsts := range groupTests {
			tests = append(tests, TestRef{Group: group, Tests: tsts})
		}
	} else if filter.Group != "" {
		ref := TestRef{Group: filter.Group}
		if filter.Test != "" {
			ref.Tests = []string{filter.Test}
		}
		tests = append(tests, ref)
	}

	batchID, queued, skipped := orch.SubmitTests(suites, tests)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":           batchID,
		"queued":             queued,
		"skipped_duplicates": skipped,
	})
}

// serveResults returns the last completed RunReport as JSON.
func (s *Server) serveResults(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b := s.last
	s.mu.Unlock()

	if b == nil {
		http.Error(w, `{"error":"no run completed yet"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// serveConfig handles GET /config. Returns developer-facing context the UI
// needs but can't derive itself — notably the absolute workspace root and
// whether the server is running inside a dev container (which changes how
// the UI opens handler source files).
func (s *Server) serveConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"workspaceRoot":   s.workspaceRoot,
		"insideContainer": insideDevContainer(),
	})
}

// insideDevContainer returns true when the process is running inside a VS Code
// dev container or GitHub Codespace. Detected via the env vars the tooling
// injects on startup — REMOTE_CONTAINERS is set by the Dev Containers
// extension, CODESPACES by Codespaces.
func insideDevContainer() bool {
	return os.Getenv("REMOTE_CONTAINERS") != "" ||
		os.Getenv("CODESPACES") != "" ||
		os.Getenv("DEVCONTAINER") != ""
}

// serveOpen handles POST /open. Opens a workspace-relative path in the
// developer's editor via the `code` CLI. Inside a dev container this hits the
// VS Code Server shim which forwards the open request to the attached host
// window, so it works transparently whether the compat server runs on the
// host or inside the container — no vscode:// URL munging required on the
// client side.
func (s *Server) serveOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, `{"error":"path required"}`, http.StatusBadRequest)
		return
	}
	// Reject absolute paths and traversal — everything must stay under the
	// workspace root that the server was started in.
	if filepath.IsAbs(req.Path) || strings.Contains(req.Path, "..") {
		http.Error(w, `{"error":"path must be workspace-relative"}`, http.StatusBadRequest)
		return
	}
	abs := filepath.Join(s.workspaceRoot, filepath.FromSlash(req.Path))
	if _, err := os.Stat(abs); err != nil {
		http.Error(w, `{"error":"path not found"}`, http.StatusNotFound)
		return
	}
	cmd := exec.Command("code", "--goto", abs)
	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	// Don't block on the child; let code detach.
	go func() { _ = cmd.Wait() }()
	w.WriteHeader(http.StatusNoContent)
}

// serveSuites handles GET /suites — returns suite process states.
// Only available in interactive mode.
func (s *Server) serveSuites(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	orch := s.orchestrator
	s.mu.Unlock()
	if orch == nil {
		http.Error(w, `{"error":"not in interactive mode"}`, http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orch.SuiteStates())
}

// serveCancel handles POST /cancel — cancels queued/running tests.
// Only available in interactive mode.
func (s *Server) serveCancel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	orch := s.orchestrator
	s.mu.Unlock()
	if orch == nil {
		http.Error(w, `{"error":"not in interactive mode"}`, http.StatusNotImplemented)
		return
	}
	var req struct {
		BatchID string `json:"batch_id"`
		Suite   string `json:"suite"`
		Group   string `json:"group"`
		Test    string `json:"test"`
		All     bool   `json:"all"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
	}
	cancelled := orch.CancelTests(req.BatchID, req.Suite, req.Group, req.Test, req.All)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"cancelled": cancelled})
}

// serveReset handles POST /reset — cancels all running/queued tests and clears
// the replay buffer so that newly-connecting SSE clients start with a clean slate.
// Connected clients update themselves via the optimistic clear_results dispatch
// in the UI; we do not broadcast any events here to avoid overriding that.
func (s *Server) serveReset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	orch := s.orchestrator
	s.mu.Unlock()
	if orch != nil {
		orch.CancelTests("", "", "", "", true)
	}
	// Clear the replay buffer silently — do not broadcast run_reset.
	// The clicking client already dispatched clear_results optimistically.
	// Clients that connect fresh after this will see an empty state.
	s.mu.Lock()
	s.buf = s.buf[:0]
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// serveRegistry handles GET /registry — serves the canonical test registry.
func (s *Server) serveRegistry(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.workspaceRoot, "compat", "suites", "registry.json")
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, `{"error":"registry not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// serveQueue handles GET /queue — returns current queue state.
// Only available in interactive mode.
func (s *Server) serveQueue(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	orch := s.orchestrator
	s.mu.Unlock()
	if orch == nil {
		http.Error(w, `{"error":"not in interactive mode"}`, http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orch.QueueState())
}

// serveReload handles POST /reload — restarts a suite process (hot-swap).
// Body: {"suite": "name"} or {} to reload all.
func (s *Server) serveReload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	orch := s.orchestrator
	s.mu.Unlock()
	if orch == nil {
		http.Error(w, `{"error":"not in interactive mode"}`, http.StatusNotImplemented)
		return
	}
	var req struct {
		Suite string `json:"suite"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
	}
	if req.Suite != "" {
		if err := orch.ReloadSuite(req.Suite); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"reloaded": []string{req.Suite}})
		return
	}
	// Reload all suites.
	var reloaded []string
	for _, st := range orch.SuiteStates() {
		if err := orch.ReloadSuite(st.Name); err == nil {
			reloaded = append(reloaded, st.Name)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"reloaded": reloaded})
}

// writeSSELine writes a single SSE "data:" line.
func writeSSELine(w http.ResponseWriter, data []byte) error {
	_, err := fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// testPairsForStatuses reads the last RunReport and returns all
// "groupName:testName" pairs whose status matches one of f.Statuses, scoped
// by f.Suite / f.Service / f.Group / f.Test when those fields are set.
func (s *Server) testPairsForStatuses(f RunFilter) []string {
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	if last == nil {
		return nil
	}

	var report RunReport
	if err := json.Unmarshal(last, &report); err != nil {
		return nil
	}

	want := make(map[Status]bool, len(f.Statuses))
	for _, st := range f.Statuses {
		want[Status(st)] = true
	}

	var pairs []string
	seen := make(map[string]bool)
	for _, suite := range report.Suites {
		if f.Suite != "" && suite.Suite != f.Suite {
			continue
		}
		for _, grp := range suite.Groups {
			if f.Service != "" && grp.Service != f.Service {
				continue
			}
			if f.Group != "" && grp.Name != f.Group {
				continue
			}
			for _, t := range grp.Tests {
				if f.Test != "" && t.Test != f.Test {
					continue
				}
				if want[t.Status] {
					key := grp.Name + ":" + t.Test
					if !seen[key] {
						seen[key] = true
						pairs = append(pairs, key)
					}
				}
			}
		}
	}
	return pairs
}

// submitFailingFromReport reads the persisted last RunReport and submits
// tests whose status matches filter.Statuses to the orchestrator.
// Used as a fallback when o.results is empty (e.g. after a server restart).
func (s *Server) submitFailingFromReport(orch *Orchestrator, filter RunFilter) (batchID string, queued []QueueEntry, skipped int) {
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	if last == nil {
		return "", nil, 0
	}
	var report RunReport
	if err := json.Unmarshal(last, &report); err != nil {
		return "", nil, 0
	}

	want := make(map[Status]bool, len(filter.Statuses))
	for _, st := range filter.Statuses {
		want[Status(st)] = true
	}

	// Collect tests grouped by their originating suite so that each suite
	// only receives tests that belong to it.
	type suiteRefs struct {
		seen map[string][]string // group → tests (for building refs)
	}
	bySuite := make(map[string]*suiteRefs)
	for _, suite := range report.Suites {
		if filter.Suite != "" && suite.Suite != filter.Suite {
			continue
		}
		for _, grp := range suite.Groups {
			if filter.Service != "" && grp.Service != filter.Service {
				continue
			}
			for _, t := range grp.Tests {
				if !want[t.Status] {
					continue
				}
				sr := bySuite[suite.Suite]
				if sr == nil {
					sr = &suiteRefs{seen: make(map[string][]string)}
					bySuite[suite.Suite] = sr
				}
				sr.seen[grp.Name] = append(sr.seen[grp.Name], t.Test)
			}
		}
	}
	if len(bySuite) == 0 {
		return "", nil, 0
	}

	// Build per-suite TestRef slices and submit each suite individually.
	// Using the same batch ID across all submissions is not possible (each
	// SubmitTests call generates a fresh ID), so we submit all suites in one
	// call by passing the full test list and the list of relevant suites.
	var allRefs []TestRef
	groupTests := make(map[string][]string)
	seen := make(map[string]bool)
	var suiteNames []string
	for suiteName, sr := range bySuite {
		suiteNames = append(suiteNames, suiteName)
		for grp, tests := range sr.seen {
			for _, t := range tests {
				key := grp + ":" + t
				if !seen[key] {
					seen[key] = true
					groupTests[grp] = append(groupTests[grp], t)
				}
			}
		}
	}
	for grp, tests := range groupTests {
		allRefs = append(allRefs, TestRef{Group: grp, Tests: tests})
	}
	return orch.SubmitTests(suiteNames, allRefs)
}
