package main

import (
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// stopTestRoot builds a minimal root command with newStopCmd() attached,
// mirroring waitTestRoot in cmd_wait_test.go.
func stopTestRoot() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "overcast", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")
	root.AddCommand(newStopCmd())
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	return root, buf
}

func TestStopCommandShape(t *testing.T) {
	cmd := newStopCmd()
	if cmd.Args == nil {
		t.Fatal("newStopCmd: Args is nil")
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("newStopCmd: Args accepted two positional arguments, want at most one")
	}
	if err := cmd.Args(cmd, []string{"only-one"}); err != nil {
		t.Errorf("newStopCmd: Args rejected a single positional argument: %v", err)
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatal("newStopCmd: ValidArgsFunction is nil")
	}
}

func TestRestartCommandShape(t *testing.T) {
	cmd := newRestartCmd()
	if cmd.Args == nil {
		t.Fatal("newRestartCmd: Args is nil")
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("newRestartCmd: Args accepted two positional arguments, want at most one")
	}
	if err := cmd.Args(cmd, []string{"only-one"}); err != nil {
		t.Errorf("newRestartCmd: Args rejected a single positional argument: %v", err)
	}
}

func TestInstanceNameArg(t *testing.T) {
	if got := instanceNameArg(nil); got != "default" {
		t.Errorf("instanceNameArg(nil) = %q, want %q", got, "default")
	}
	if got := instanceNameArg([]string{"custom"}); got != "custom" {
		t.Errorf("instanceNameArg([custom]) = %q, want %q", got, "custom")
	}
}

func TestStartOptionsFromRecord(t *testing.T) {
	rec := instanceRecord{
		Name: "x", Backend: "native", Port: 4570, UIPort: 4571, State: "hybrid", DataDir: "/tmp/x",
		Env: map[string]string{"AWS_REGION": "eu-west-1"},
	}
	got := startOptionsFromRecord(rec)
	want := startOptions{name: "x", port: 4570, uiPort: 4571, state: "hybrid", dataDir: "/tmp/x", env: rec.Env}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("startOptionsFromRecord = %+v, want %+v", got, want)
	}
}

func TestStopInstance_UnknownName(t *testing.T) {
	withTestInstancesDir(t)
	root, _ := stopTestRoot()
	root.SetArgs([]string{"stop", "nope"})
	if err := root.Execute(); err == nil {
		t.Fatal("stop of an unknown instance succeeded, want an error")
	}
}

func TestStopInstance_NotRunningRemovesStaleRecord(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "stale", Backend: "native", PID: exitedPID(t)}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}

	root, buf := stopTestRoot()
	root.SetArgs([]string{"stop", "stale"})
	if err := root.Execute(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := loadInstance("stale"); err == nil {
		t.Error("stale record still present after stop")
	}
	if !strings.Contains(buf.String(), "was not running") {
		t.Errorf("expected a %q message, got %q", "was not running", buf.String())
	}
}

func TestStopInstance_DefaultsNameToDefault(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "default", Backend: "native", PID: exitedPID(t)}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	root, _ := stopTestRoot()
	root.SetArgs([]string{"stop"}) // no positional name given
	if err := root.Execute(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := loadInstance("default"); err == nil {
		t.Error("record for the default-named instance still present after `overcast stop` with no argument")
	}
}

// TestStopInstance_TerminatesRunningProcess spawns a real (but harmless,
// self-terminating-on-signal) child process and verifies `overcast stop`
// actually ends it and removes its record — exercising sendTerminate's
// platform-specific behavior end to end without needing a real daemon or
// binding any port.
func TestStopInstance_TerminatesRunningProcess(t *testing.T) {
	withTestInstancesDir(t)
	child := exec.Command(os.Args[0], "-test.run=TestHelperProcessSleep")
	child.Env = append(os.Environ(), "OVERCAST_GO_WANT_HELPER_SLEEP=1")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn sleeping helper: %v", err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	// Reap the child as soon as it dies. Unlike production — where the
	// daemon was reparented to init (its `overcast start` parent exited
	// long ago) and init reaps it — this helper is OUR child, and an
	// unreaped dead child on unix is a zombie whose pid still answers
	// Signal(0), so processAlive would read it as alive forever and stop's
	// two 10s exit-waits would both time out (exactly how this test failed
	// on Linux CI while passing on Windows, which has no zombie state).
	// Wait in the background so the pid is reaped the moment stop kills it.
	go func() { _ = child.Wait() }()

	rec := instanceRecord{Name: "running", Backend: "native", PID: child.Process.Pid}
	if err := saveInstance(rec); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	if !instanceRunning(rec) {
		t.Fatal("sanity: freshly spawned helper does not read as running")
	}

	root, buf := stopTestRoot()
	root.SetArgs([]string{"stop", "running"})
	if err := root.Execute(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(buf.String(), "stopped") {
		t.Errorf("expected a %q message, got %q", "stopped", buf.String())
	}
	if instanceRunning(rec) {
		t.Error("process still alive after stop")
	}
	if _, err := loadInstance("running"); err == nil {
		t.Error("record still present after stop")
	}
}

// TestHelperProcessSleep isn't a real test — like TestHelperProcess in
// instances_test.go, it's this test binary re-invoked as a child purely to
// have a real process for TestStopInstance_TerminatesRunningProcess to stop.
// It installs no signal handler, so both sendTerminate paths end it: SIGTERM
// on unix (Go's default disposition for an unhandled SIGTERM is to
// terminate) and Kill() on windows.
func TestHelperProcessSleep(t *testing.T) {
	if os.Getenv("OVERCAST_GO_WANT_HELPER_SLEEP") != "1" {
		return
	}
	time.Sleep(2 * time.Minute)
}
