package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// startTestRoot builds a minimal root command carrying the same persistent
// --endpoint flag main.go registers, with newStartCmd() attached — mirrors
// waitTestRoot in cmd_wait_test.go.
func startTestRoot() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "overcast", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newStartCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	return root, buf
}

func TestStartCommandShape(t *testing.T) {
	cmd := newStartCmd()
	if cmd.Args == nil {
		t.Fatal("newStartCmd: Args is nil, want cobra.NoArgs")
	}
	if err := cmd.Args(cmd, []string{"unexpected"}); err == nil {
		t.Error("newStartCmd: Args accepted a positional argument")
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatal("newStartCmd: ValidArgsFunction is nil")
	}
	_, directive := cmd.ValidArgsFunction(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("ValidArgsFunction directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}
}

// TestStartCmd_RefusesWhenAlreadyRunning exercises newStartCmd's RunE
// end-to-end against a fake record pointed at this test process's own pid
// (guaranteed alive) — the refusal happens before any spawn is attempted, so
// this never risks actually starting `overcast serve`.
func TestStartCmd_RefusesWhenAlreadyRunning(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "default", Backend: "native", PID: os.Getpid()}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}

	root, _ := startTestRoot()
	root.SetArgs([]string{"start"})
	err := root.Execute()
	if err == nil {
		t.Fatal("start over an already-running instance succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "overcast stop") {
		t.Errorf("error %q does not point at `overcast stop`", err)
	}

	// Confirm the refusal happened before any spawn attempt: the saved
	// record is untouched.
	got, loadErr := loadInstance("default")
	if loadErr != nil {
		t.Fatalf("loadInstance: %v", loadErr)
	}
	if got.PID != os.Getpid() {
		t.Errorf("record was modified despite the refusal: pid = %d, want %d", got.PID, os.Getpid())
	}
}

// TestStaleRecordDoesNotTriggerRefusal covers the gate newStartCmd's RunE
// uses to decide "replace silently" vs. "refuse": a record whose pid is no
// longer running must read as not-running, so start proceeds instead of
// erroring. Actually spawning `overcast serve` is out of scope for this
// hermetic test (see the task brief's "otherwise test the assembly
// functions").
func TestStaleRecordDoesNotTriggerRefusal(t *testing.T) {
	withTestInstancesDir(t)
	stale := instanceRecord{Name: "default", Backend: "native", PID: exitedPID(t)}
	if err := saveInstance(stale); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	loaded, err := loadInstance("default")
	if err != nil {
		t.Fatalf("loadInstance: %v", err)
	}
	if instanceRunning(loaded) {
		t.Fatal("stale record (exited pid) reads as running — start would wrongly refuse it")
	}
}

func TestParseStartEnv_AllowsOvercastAndAWSPrefixes(t *testing.T) {
	got, err := parseStartEnv([]string{"OVERCAST_LOG_LEVEL=debug", "AWS_REGION=us-west-2"})
	if err != nil {
		t.Fatalf("parseStartEnv: %v", err)
	}
	if got["OVERCAST_LOG_LEVEL"] != "debug" || got["AWS_REGION"] != "us-west-2" {
		t.Errorf("got %+v", got)
	}
}

func TestParseStartEnv_RejectsDisallowedPrefix(t *testing.T) {
	for _, kv := range []string{"PATH=/evil", "LD_PRELOAD=/evil.so", "HOME=/root"} {
		if _, err := parseStartEnv([]string{kv}); err == nil {
			t.Errorf("parseStartEnv accepted %q, want an error", kv)
		}
	}
}

func TestParseStartEnv_RejectsMissingEquals(t *testing.T) {
	if _, err := parseStartEnv([]string{"OVERCAST_STATE"}); err == nil {
		t.Fatal("parseStartEnv accepted a flag with no '=', want an error")
	}
}

func TestBuildChildEnv_IncludesPortStateDataDirAndPassthrough(t *testing.T) {
	env := buildChildEnv(startOptions{
		port:    4570,
		state:   "memory",
		dataDir: "/tmp/data",
		env:     map[string]string{"OVERCAST_LOG_LEVEL": "trace"},
	})
	assertHasEnv := func(want string) {
		t.Helper()
		for _, kv := range env {
			if kv == want {
				return
			}
		}
		t.Errorf("child env missing %q; got %v", want, env)
	}
	assertHasEnv("OVERCAST_PORT=4570")
	assertHasEnv("OVERCAST_STATE=memory")
	assertHasEnv("OVERCAST_DATA_DIR=/tmp/data")
	assertHasEnv("OVERCAST_LOG_LEVEL=trace")
}

func TestBuildChildEnv_OmitsEmptyStateAndDataDir(t *testing.T) {
	env := buildChildEnv(startOptions{port: 4566})
	for _, kv := range env {
		if strings.HasPrefix(kv, "OVERCAST_STATE=") || strings.HasPrefix(kv, "OVERCAST_DATA_DIR=") {
			t.Errorf("unexpected %q in child env when --state/--data-dir were not given", kv)
		}
	}
}

func TestBuildInstanceRecord(t *testing.T) {
	startedAt := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)
	rec := buildInstanceRecord(startOptions{
		name:    "default",
		port:    4566,
		uiPort:  4567,
		state:   "hybrid",
		dataDir: "/tmp/data",
		env:     map[string]string{"AWS_REGION": "eu-west-1"},
	}, 4242, "/tmp/instances/default/daemon.log", startedAt)

	if rec.Name != "default" || rec.Backend != "native" || rec.PID != 4242 ||
		rec.Port != 4566 || rec.UIPort != 4567 || rec.DataDir != "/tmp/data" ||
		rec.State != "hybrid" || rec.LogFile != "/tmp/instances/default/daemon.log" ||
		!rec.StartedAt.Equal(startedAt) || rec.Env["AWS_REGION"] != "eu-west-1" {
		t.Errorf("unexpected record: %+v", rec)
	}
	if want := "http://127.0.0.1:4566"; rec.Endpoint != want {
		t.Errorf("Endpoint = %q, want %q (127.0.0.1, not localhost)", rec.Endpoint, want)
	}
}

func TestStartBackend_UnknownBackend(t *testing.T) {
	if _, err := startBackend("docker", startOptions{name: "x"}); err == nil {
		t.Fatal(`startBackend("docker", ...) succeeded, want an error — the docker backend does not exist yet`)
	}
}
