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

// configTestRoot builds a minimal root command carrying the same persistent
// --endpoint flag main.go registers, with newConfigCmd() attached. Mirrors
// servicesTestRoot in cmd_services_test.go.
func configTestRoot() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "overcast", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newConfigCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	return root, buf
}

// configFixtureJSON is a realistic (compact, unindented — as the daemon
// actually sends it) GET /_overcast/debug/config body. Field names mirror
// debugConfigResponse in internal/router/debug.go; hand-written rather than
// constructed via that struct because the CLI must not import
// internal/router.
const configFixtureJSON = `{"host":"127.0.0.1","port":4566,"services":["s3","sqs"],"state":"memory","debug":true,"tls_enabled":false}`

func configFixtureServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/debug/config" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestConfigCmd_prettyOutput covers the default rendering: valid,
// re-indented JSON containing every field from the fixture.
func TestConfigCmd_prettyOutput(t *testing.T) {
	srv := configFixtureServer(t, http.StatusOK, configFixtureJSON)
	defer srv.Close()

	root, buf := configTestRoot()
	root.SetArgs([]string{"config", "--endpoint", srv.URL})

	if err := root.Execute(); err != nil {
		t.Fatalf("config: %v (output: %q)", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "\n  ") {
		t.Errorf("output %q does not look indented", out)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("pretty output is not valid JSON: %v\n%s", err, out)
	}
	if got["host"] != "127.0.0.1" {
		t.Errorf("host = %v, want 127.0.0.1", got["host"])
	}
	if got["debug"] != true {
		t.Errorf("debug = %v, want true", got["debug"])
	}
}

// TestConfigCmd_jsonOutputIsRaw covers --output json: the daemon's exact
// bytes (plus a trailing newline), not re-indented.
func TestConfigCmd_jsonOutputIsRaw(t *testing.T) {
	srv := configFixtureServer(t, http.StatusOK, configFixtureJSON)
	defer srv.Close()

	root, buf := configTestRoot()
	root.SetArgs([]string{"config", "--endpoint", srv.URL, "--output", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("config --output json: %v (output: %q)", err, buf.String())
	}

	got := strings.TrimRight(buf.String(), "\n")
	if got != configFixtureJSON {
		t.Errorf("raw output = %q, want the fixture body unchanged: %q", got, configFixtureJSON)
	}
}

// TestConfigCmd_debugDisabled404 covers the daemon answering 404 (debug
// off): the error must say to start the daemon with OVERCAST_DEBUG=true, per
// the design brief — this is the one debug route callers hit "debug is off"
// on without first thinking about the flag.
func TestConfigCmd_debugDisabled404(t *testing.T) {
	srv := configFixtureServer(t, http.StatusNotFound, `{"error":"not found"}`)
	defer srv.Close()

	root, _ := configTestRoot()
	root.SetArgs([]string{"config", "--endpoint", srv.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("config against a debug-disabled daemon succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "OVERCAST_DEBUG=true") {
		t.Errorf("error %q does not mention OVERCAST_DEBUG=true", err)
	}
	if !strings.Contains(err.Error(), "enable config introspection") {
		t.Errorf("error %q does not match the design brief's wording", err)
	}
}

// TestConfigCmd_unreachableDaemon covers a closed connection.
func TestConfigCmd_unreachableDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nothing listening at srv.URL

	root, _ := configTestRoot()
	root.SetArgs([]string{"config", "--endpoint", srv.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("config against a closed server succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "overcast unreachable at "+srv.URL) {
		t.Errorf("error %q does not match the cmd_status.go wording", err)
	}
}

// TestConfigCmd_invalidOutputFlag covers an unrecognized --output value.
func TestConfigCmd_invalidOutputFlag(t *testing.T) {
	srv := configFixtureServer(t, http.StatusOK, configFixtureJSON)
	defer srv.Close()

	root, _ := configTestRoot()
	root.SetArgs([]string{"config", "--endpoint", srv.URL, "--output", "yaml"})

	err := root.Execute()
	if err == nil {
		t.Fatal("config --output yaml succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error %q does not name the bad value", err)
	}
}

// TestConfigCmd_commandShape pins the parts of the command declaration that
// don't need a server.
func TestConfigCmd_commandShape(t *testing.T) {
	cmd := newConfigCmd()
	if cmd.Args == nil {
		t.Fatal("newConfigCmd: Args is nil, want cobra.NoArgs")
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Error("newConfigCmd: Args accepted a positional argument")
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatal("newConfigCmd: ValidArgsFunction is nil")
	}
	_, directive := cmd.ValidArgsFunction(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("ValidArgsFunction directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
	if cmd.Flag("output") == nil {
		t.Fatal("newConfigCmd: --output flag not registered")
	}
}
