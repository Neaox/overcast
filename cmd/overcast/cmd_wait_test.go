package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// waitTestRoot builds a minimal root command carrying the same persistent
// --endpoint flag main.go registers, with newWaitCmd() attached — so
// RunE's cmd.Flags().GetString("endpoint") resolves the way it does when
// invoked through the real CLI tree.
func waitTestRoot() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "overcast", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newWaitCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	return root, buf
}

// TestWaitSucceedsAfterInitialFailures covers the common case: the daemon
// is still starting up (a handful of 503s), then becomes healthy — `wait`
// must poll through the failures rather than giving up on the first one.
func TestWaitSucceedsAfterInitialFailures(t *testing.T) {
	var requests int32
	const failFor = 3
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/health" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		n := atomic.AddInt32(&requests, 1)
		if n <= failFor {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	root, buf := waitTestRoot()
	root.SetArgs([]string{"wait", "--endpoint", srv.URL, "--interval", "10ms", "--timeout", "5s"})

	if err := root.Execute(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got <= failFor {
		t.Errorf("expected wait to poll past the initial failures, only saw %d requests", got)
	}
	if !strings.Contains(buf.String(), "overcast ready at "+srv.URL) {
		t.Errorf("output %q does not report readiness at %s", buf.String(), srv.URL)
	}
}

// TestWaitTimesOut covers a daemon that never becomes healthy: `wait` must
// give up once --timeout elapses and return an error naming the endpoint,
// per the CLI contract that main.go prints the error and exits 1.
func TestWaitTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	root, _ := waitTestRoot()
	root.SetArgs([]string{"wait", "--endpoint", srv.URL, "--interval", "5ms", "--timeout", "50ms"})

	start := time.Now()
	err := root.Execute()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("wait against an always-503 server succeeded, want a timeout error")
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error %q does not name the endpoint", err)
	}
	// Generous upper bound: the timeout is 50ms, this just guards against the
	// poll loop hanging well past it.
	if elapsed > 5*time.Second {
		t.Errorf("wait took %s to time out on a 50ms budget", elapsed)
	}
}

// TestWaitQuietSuppressesOutput covers --quiet: success should still exit 0
// but print nothing, for scripts that only care about the exit code.
func TestWaitQuietSuppressesOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	root, buf := waitTestRoot()
	root.SetArgs([]string{"wait", "--endpoint", srv.URL, "--interval", "10ms", "--timeout", "5s", "--quiet"})

	if err := root.Execute(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("--quiet: expected no output, got %q", buf.String())
	}
}

// TestWaitCommandShape pins the parts of the command declaration that don't
// need a server: no positional args, and completion opts out of file
// completion (there's nothing to complete for a daemon-readiness check).
func TestWaitCommandShape(t *testing.T) {
	cmd := newWaitCmd()
	if cmd.Args == nil {
		t.Fatal("newWaitCmd: Args is nil, want cobra.NoArgs")
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Error("newWaitCmd: Args accepted a positional argument")
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatal("newWaitCmd: ValidArgsFunction is nil")
	}
	_, directive := cmd.ValidArgsFunction(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("ValidArgsFunction directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
}
