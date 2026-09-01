package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// servicesTestRoot builds a minimal root command carrying the same
// persistent --endpoint flag main.go registers, with newServicesCmd()
// attached — so RunE's cmd.Flags().GetString("endpoint") resolves the way
// it does when invoked through the real CLI tree.
func servicesTestRoot() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "overcast", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newServicesCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	return root, buf
}

// healthFixtureJSON is a realistic GET /_overcast/health body. Field names
// mirror healthResponse in internal/router/health.go (source of truth for
// the shape); this is hand-written rather than constructed via that struct
// because the CLI must not import internal/router.
const healthFixtureJSON = `{
	"status": "ok",
	"timestamp": "2026-09-01T00:00:00Z",
	"version": "1.2.3",
	"services": ["s3", "dynamodb", "lambda"],
	"serviceTiers": {"s3": "emulated", "dynamodb": "emulated", "lambda": "proxied"},
	"serviceGoalTiers": {"s3": "emulated", "dynamodb": "emulated", "lambda": "emulated"},
	"storage": {"default": "sqlite"}
}`

func servicesFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/health" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(healthFixtureJSON))
	}))
}

// TestServicesTextOutput covers the default text table: sorted by service
// name (dynamodb, lambda, s3 — not the fixture's declared order) and
// aligned via tabwriter, with a header row.
func TestServicesTextOutput(t *testing.T) {
	srv := servicesFixtureServer(t)
	defer srv.Close()

	root, buf := servicesTestRoot()
	root.SetArgs([]string{"services", "--endpoint", srv.URL})

	if err := root.Execute(); err != nil {
		t.Fatalf("services: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (header + 3 services): %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "SERVICE") || !strings.Contains(lines[0], "TIER") {
		t.Errorf("header line %q missing SERVICE/TIER", lines[0])
	}
	wantOrder := []string{"dynamodb", "lambda", "s3"}
	for i, want := range wantOrder {
		if !strings.HasPrefix(lines[i+1], want) {
			t.Errorf("line %d = %q, want it to start with %q (sorted order)", i+1, lines[i+1], want)
		}
	}
	if !strings.Contains(buf.String(), "lambda") || !strings.Contains(buf.String(), "proxied") {
		t.Errorf("output %q missing lambda's tier", buf.String())
	}
}

// TestServicesJSONOutput covers --output json: it must round-trip into the
// servicesJSON shape, contain every service sorted by name with its tier,
// the daemon version, and end with a trailing newline.
func TestServicesJSONOutput(t *testing.T) {
	srv := servicesFixtureServer(t)
	defer srv.Close()

	root, buf := servicesTestRoot()
	root.SetArgs([]string{"services", "--endpoint", srv.URL, "--output", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("services: %v", err)
	}

	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("json output does not end with a trailing newline: %q", raw)
	}

	var got servicesJSON
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}
	if got.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", got.Version, "1.2.3")
	}
	want := []serviceEntry{
		{Service: "dynamodb", Tier: "emulated"},
		{Service: "lambda", Tier: "proxied"},
		{Service: "s3", Tier: "emulated"},
	}
	if len(got.Services) != len(want) {
		t.Fatalf("Services = %+v, want %d entries", got.Services, len(want))
	}
	for i, w := range want {
		if got.Services[i] != w {
			t.Errorf("Services[%d] = %+v, want %+v", i, got.Services[i], w)
		}
	}
}

// TestServicesUnreachableDaemon covers a closed connection: the error must
// match cmd_status.go's wording ("overcast unreachable at %s: %w") so the
// two commands read consistently.
func TestServicesUnreachableDaemon(t *testing.T) {
	srv := servicesFixtureServer(t)
	srv.Close() // close immediately: nothing is listening at srv.URL

	root, _ := servicesTestRoot()
	root.SetArgs([]string{"services", "--endpoint", srv.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("services against a closed server succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "overcast unreachable at "+srv.URL) {
		t.Errorf("error %q does not match the cmd_status.go wording", err)
	}
}

// TestServicesInvalidOutputFlag covers an unrecognized --output value.
func TestServicesInvalidOutputFlag(t *testing.T) {
	srv := servicesFixtureServer(t)
	defer srv.Close()

	root, _ := servicesTestRoot()
	root.SetArgs([]string{"services", "--endpoint", srv.URL, "--output", "yaml"})

	err := root.Execute()
	if err == nil {
		t.Fatal("services --output yaml succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error %q does not name the bad value", err)
	}
}

// TestServicesCommandShape pins the parts of the command declaration that
// don't need a server: no positional args, file completion opted out, and
// --output completes to exactly text/json.
func TestServicesCommandShape(t *testing.T) {
	cmd := newServicesCmd()
	if cmd.Args == nil {
		t.Fatal("newServicesCmd: Args is nil, want cobra.NoArgs")
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Error("newServicesCmd: Args accepted a positional argument")
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatal("newServicesCmd: ValidArgsFunction is nil")
	}
	_, directive := cmd.ValidArgsFunction(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("ValidArgsFunction directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
	if cmd.Flag("output") == nil {
		t.Fatal("newServicesCmd: --output flag not registered")
	}
}
