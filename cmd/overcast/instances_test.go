package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// withTestInstancesDir redirects instancesBaseDir into a fresh t.TempDir()
// for the duration of one test, so registry tests never touch the real
// ~/.overcast on the machine running the suite. Mirrors stubEnviron's seam
// pattern in cmd_env_test.go.
func withTestInstancesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := instancesBaseDir
	instancesBaseDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { instancesBaseDir = prev })
	return dir
}

// TestHelperProcess isn't a real test — it's this test binary re-invoked as
// a child by exitedPID below (the standard os/exec "helper process" idiom;
// see e.g. the Go standard library's own os/exec tests), purely so there is
// a real, cross-platform pid to spawn and immediately let exit. Running
// `go test -run=TestHelperProcess` directly reports it passed trivially,
// which is fine — it's never invoked that way outside exitedPID.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("OVERCAST_GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	os.Exit(0)
}

// exitedPID spawns a trivial child, waits for it to exit, and returns its
// pid. The pid is guaranteed dead the moment this returns and — having only
// just been reaped — cannot yet have been recycled onto some other live
// process on the machine running the suite.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "OVERCAST_GO_WANT_HELPER_PROCESS=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn helper process: %v", err)
	}
	return cmd.Process.Pid
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTestInstancesDir(t)
	want := instanceRecord{
		Name:      "default",
		Backend:   "native",
		PID:       1234,
		Port:      4566,
		UIPort:    4567,
		Endpoint:  "http://127.0.0.1:4566",
		DataDir:   "/tmp/data",
		State:     "hybrid",
		LogFile:   filepath.Join("default", "daemon.log"),
		StartedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Version:   "1.2.3",
		Env:       map[string]string{"OVERCAST_LOG_LEVEL": "debug"},
	}
	if err := saveInstance(want); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	got, err := loadInstance("default")
	if err != nil {
		t.Fatalf("loadInstance: %v", err)
	}
	if got.Name != want.Name || got.Backend != want.Backend || got.PID != want.PID || got.Port != want.Port ||
		got.UIPort != want.UIPort || got.Endpoint != want.Endpoint || got.DataDir != want.DataDir ||
		got.State != want.State || got.LogFile != want.LogFile || !got.StartedAt.Equal(want.StartedAt) ||
		got.Version != want.Version || got.Env["OVERCAST_LOG_LEVEL"] != "debug" {
		t.Errorf("round trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestLoadInstanceMissing(t *testing.T) {
	withTestInstancesDir(t)
	if _, err := loadInstance("nope"); err == nil {
		t.Fatal("loadInstance of a nonexistent instance succeeded, want an error")
	}
}

func TestListInstancesEmpty(t *testing.T) {
	withTestInstancesDir(t) // base dir does not exist yet — must not error
	recs, err := listInstances()
	if err != nil {
		t.Fatalf("listInstances on a nonexistent base dir: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("got %d instances, want 0", len(recs))
	}
}

func TestListInstancesSortedByName(t *testing.T) {
	withTestInstancesDir(t)
	for _, name := range []string{"zebra", "alpha", "mid"} {
		if err := saveInstance(instanceRecord{Name: name, Backend: "native", PID: os.Getpid()}); err != nil {
			t.Fatalf("saveInstance(%s): %v", name, err)
		}
	}
	recs, err := listInstances()
	if err != nil {
		t.Fatalf("listInstances: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d instances, want 3", len(recs))
	}
	want := []string{"alpha", "mid", "zebra"}
	for i, w := range want {
		if recs[i].Name != w {
			t.Errorf("recs[%d].Name = %q, want %q", i, recs[i].Name, w)
		}
	}
}

func TestListInstancesSkipsCorruptRecord(t *testing.T) {
	dir := withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "good", Backend: "native"}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	corruptDir := filepath.Join(dir, "corrupt")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "instance.json"), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}

	recs, err := listInstances()
	if err != nil {
		t.Fatalf("listInstances: %v", err)
	}
	if len(recs) != 1 || recs[0].Name != "good" {
		t.Errorf("got %+v, want exactly the one good record", recs)
	}
}

func TestRemoveInstance(t *testing.T) {
	dir := withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "gone", Backend: "native"}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	if err := removeInstance("gone"); err != nil {
		t.Fatalf("removeInstance: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone")); !os.IsNotExist(err) {
		t.Errorf("instance directory still exists after removeInstance: err=%v", err)
	}
	if _, err := loadInstance("gone"); err == nil {
		t.Error("loadInstance succeeded after removeInstance, want an error")
	}
}

func TestRemoveInstanceNonexistentIsNotAnError(t *testing.T) {
	withTestInstancesDir(t)
	if err := removeInstance("never-existed"); err != nil {
		t.Errorf("removeInstance of a name with no record: %v, want nil", err)
	}
}

func TestInstanceRunning_CurrentProcess(t *testing.T) {
	if !instanceRunning(instanceRecord{PID: os.Getpid()}) {
		t.Error("instanceRunning(own pid) = false, want true")
	}
}

func TestInstanceRunning_ExitedProcess(t *testing.T) {
	pid := exitedPID(t)
	if instanceRunning(instanceRecord{PID: pid}) {
		t.Errorf("instanceRunning(%d) = true for an already-exited process, want false", pid)
	}
}

func TestInstanceRunning_AbsurdPID(t *testing.T) {
	if instanceRunning(instanceRecord{PID: -1}) {
		t.Error("instanceRunning(-1) = true, want false")
	}
	if instanceRunning(instanceRecord{PID: 0}) {
		t.Error("instanceRunning(0) = true, want false")
	}
}

func TestCompleteInstanceNames_ListsEveryInstance(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "running", Backend: "native", PID: os.Getpid()}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	if err := saveInstance(instanceRecord{Name: "stopped", Backend: "native", PID: exitedPID(t)}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	got, _ := completeInstanceNames(nil, nil, "")
	want := map[string]bool{"running": true, "stopped": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want both instances listed", got)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected completion %q", name)
		}
	}
}

func TestCompleteRunningInstanceNames_OnlyRunning(t *testing.T) {
	withTestInstancesDir(t)
	if err := saveInstance(instanceRecord{Name: "running", Backend: "native", PID: os.Getpid()}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	if err := saveInstance(instanceRecord{Name: "stopped", Backend: "native", PID: exitedPID(t)}); err != nil {
		t.Fatalf("saveInstance: %v", err)
	}
	got, _ := completeRunningInstanceNames(nil, nil, "")
	if len(got) != 1 || got[0] != "running" {
		t.Errorf("got %v, want exactly [\"running\"]", got)
	}
}
