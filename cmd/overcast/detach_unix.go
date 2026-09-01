//go:build !windows

package main

// detach_unix.go — unix half of the process-detachment/liveness pair (see
// detach_windows.go for the windows half and the shared rationale).
// `overcast start` needs both: a way to make the spawned `overcast serve`
// survive this process exiting (setDetachAttrs), and a way for `overcast
// stop`/instanceRunning to later check whether a recorded pid is still alive
// (processAlive) or ask it to shut down (sendTerminate).

import (
	"os"
	"os/exec"
	"syscall"
)

// setDetachAttrs puts the child in its own session (setsid) so it is not
// part of this process's process group and does not receive signals — e.g.
// Ctrl+C in the terminal that ran `overcast start` — intended for the CLI,
// not the daemon it just spawned.
func setDetachAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// processAlive reports whether pid names a live process. os.FindProcess
// always succeeds on unix — it's a handle wrapper, not a lookup — so the
// actual check is Signal(0): sent to a real process it succeeds without
// delivering anything; ESRCH (no such process) is what makes it report dead.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// sendTerminate asks proc to shut down gracefully. stopInstance
// (cmd_stop.go) follows up with a forced Kill() if it hasn't exited after
// its own grace period.
func sendTerminate(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
