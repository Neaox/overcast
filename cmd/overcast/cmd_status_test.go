package main

// cmd_status_test.go — pins `overcast status` to the daemon's real health
// route. The command once probed a bare /health, a path the router never
// registers, so every request fell through to the AWS dispatch fallback and
// the command reported an error against a perfectly healthy daemon. Building
// the actual router here keeps both ends of that contract in one test: if
// either the command's URL or the router's route moves, this fails.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/router"
	"github.com/Neaox/overcast/internal/state"
)

// runStatusCmd executes `overcast status` against endpoint and returns its
// stdout. The endpoint flag is registered locally because in production it is
// a persistent flag on the root command (see main.go).
func runStatusCmd(t *testing.T, endpoint string) (string, error) {
	t.Helper()
	cmd := newStatusCmd()
	cmd.Flags().String("endpoint", endpoint, "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	return out.String(), err
}

func TestStatusCmd_againstRealRouter(t *testing.T) {
	// Given: the real router, exactly as `overcast serve` builds it
	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",
		Version:   "1.2.3-test",
	}
	handler, preShutdown, cleanup, _ := router.New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	t.Cleanup(func() {
		preShutdown()
		cleanup(t.Context())
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// When: `overcast status` runs against it
	out, err := runStatusCmd(t, srv.URL)

	// Then: it reports the daemon healthy, in one line, enriched with the
	// version and storage backend the health endpoint already reports.
	if err != nil {
		t.Fatalf("status against a healthy daemon: %v (output: %q)", err, out)
	}
	if !strings.Contains(out, "overcast OK at "+srv.URL) {
		t.Errorf("output %q does not report OK at %s", out, srv.URL)
	}
	if !strings.Contains(out, "1.2.3-test") {
		t.Errorf("output %q does not include the daemon version", out)
	}
	if !strings.Contains(out, "memory") {
		t.Errorf("output %q does not include the storage backend", out)
	}
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 0 {
		t.Errorf("output is %d lines, want a one-liner:\n%s", n+1, out)
	}
}

// TestStatusCmd_probesHealthRoute asserts the exact path the command requests,
// so a wrong URL is diagnosed as such rather than as a generic non-200 from
// the full router's fallback.
func TestStatusCmd_probesHealthRoute(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","version":"9.9.9","storage":{"default":"wal"}}`))
	}))
	defer srv.Close()

	out, err := runStatusCmd(t, srv.URL+"/") // trailing slash must not double up
	if err != nil {
		t.Fatalf("status: %v (output: %q)", err, out)
	}
	if gotPath != "/_overcast/health" {
		t.Errorf("status requested %q, want /_overcast/health", gotPath)
	}
}

// TestStatusCmd_non200 keeps the failure path honest: a daemon answering the
// probe with anything but 200 is reported as an error, not swallowed.
func TestStatusCmd_non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer srv.Close()

	if _, err := runStatusCmd(t, srv.URL); err == nil {
		t.Fatal("status against a non-200 daemon returned nil error")
	}
}

// TestStatusCmd_plainBody covers a health body the command cannot enrich from
// (older daemon, proxy in the way): a 200 still reports OK, without details.
func TestStatusCmd_plainBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	out, err := runStatusCmd(t, srv.URL)
	if err != nil {
		t.Fatalf("status: %v (output: %q)", err, out)
	}
	if !strings.Contains(out, "overcast OK at "+srv.URL) {
		t.Errorf("output %q does not report OK at %s", out, srv.URL)
	}
}
