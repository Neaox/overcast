//go:build linux

package lambdainit

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExitCodeForWaitStatus(t *testing.T) {
	tests := []struct {
		name string
		ws   syscall.WaitStatus
		want int
	}{
		{name: "clean exit", ws: syscall.WaitStatus(0), want: 0},
		{name: "exit 3", ws: syscall.WaitStatus(3 << 8), want: 3},
		{name: "exit 127", ws: syscall.WaitStatus(127 << 8), want: 127},
		{name: "killed by SIGTERM", ws: syscall.WaitStatus(syscall.SIGTERM), want: 143},
		{name: "killed by SIGKILL", ws: syscall.WaitStatus(syscall.SIGKILL), want: 137},
		{name: "killed by SIGSEGV", ws: syscall.WaitStatus(syscall.SIGSEGV), want: 139},
	}
	for _, tc := range tests {
		if got := exitCodeFor(tc.ws); got != tc.want {
			t.Errorf("%s: exitCodeFor = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestReaperCollectsEveryChild(t *testing.T) {
	var diag lockedBuffer
	r := newReaper(&diagLog{w: &diag})
	r.start()
	defer r.shutdown()

	codes := []int{0, 3, 7}
	statuses := make([]<-chan syscall.WaitStatus, len(codes))
	for i, code := range codes {
		cmd := exec.Command(childCmd()[0]) //nolint:noctx // the test binary, re-executed
		cmd.Env = childEnviron("exit", strconv.Itoa(code))
		ch, err := r.spawn(cmd)
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		statuses[i] = ch
	}

	// One SIGCHLD can stand for several exits, so the reaper has to keep
	// calling wait4 until the kernel says there is nothing left.
	for i, ch := range statuses {
		select {
		case ws := <-ch:
			if got := exitCodeFor(ws); got != codes[i] {
				t.Errorf("child %d exited %d, want %d", i, got, codes[i])
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("child %d was never reaped\n%s", i, diag.String())
		}
	}
}

func TestReaperCollectsOrphanedGrandchildren(t *testing.T) {
	// The init is PID 1 in a container, so an orphan re-parents to it. Here it
	// is not, so the test process takes PID 1's role for the duration: without
	// this the grandchild would re-parent to the container's own init and there
	// would be nothing to prove.
	setChildSubreaper(t, true)
	defer setChildSubreaper(t, false)

	var diag lockedBuffer
	r := newReaper(&diagLog{w: &diag})
	r.start()
	defer r.shutdown()

	cmd := exec.Command(childCmd()[0]) //nolint:noctx // the test binary, re-executed
	cmd.Env = childEnviron("orphan", "")
	ch, err := r.spawn(cmd)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	select {
	case ws := <-ch:
		if got := exitCodeFor(ws); got != 0 {
			t.Fatalf("the child exited %d", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the child was never reaped")
	}

	// The grandchild becomes the init's problem the moment its parent goes.
	deadline := time.Now().Add(20 * time.Second)
	for r.orphanCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.orphanCount() == 0 {
		t.Fatalf("the orphaned grandchild was never reaped\n%s", diag.String())
	}
	if !strings.Contains(diag.String(), "[overcast-init] reaped orphan pid=") {
		t.Fatalf("reaping an orphan was not reported:\n%s", diag.String())
	}
}

// setChildSubreaper makes (or unmakes) this process the one orphans re-parent
// to. PR_SET_CHILD_SUBREAPER is 36 and has been in Linux since 3.4.
func setChildSubreaper(t *testing.T, on bool) {
	t.Helper()
	const prSetChildSubreaper = 36
	var v uintptr
	if on {
		v = 1
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, prSetChildSubreaper, v, 0); errno != 0 {
		t.Fatalf("prctl(PR_SET_CHILD_SUBREAPER, %d): %v", v, errno)
	}
}
