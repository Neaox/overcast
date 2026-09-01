//go:build windows

package main

// detach_windows.go — windows half of the process-detachment/liveness pair
// (see detach_unix.go for the unix half and the shared rationale).

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// setDetachAttrs starts the child outside this process's console/process
// group: CREATE_NEW_PROCESS_GROUP so it does not receive this console's
// Ctrl+C, DETACHED_PROCESS so it has no console of its own — it would
// otherwise inherit ours, which `overcast start` may have already exited by
// the time the daemon has anything to print.
func setDetachAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

// stillActive is STILL_ACTIVE (259), the pseudo exit-code
// GetExitCodeProcess reports for a process that has not exited yet.
const stillActive = 259

// processAlive reports whether pid names a live process. Windows has no
// signal-0 equivalent for a plain liveness probe, so this opens the process
// with the smallest access right that still lets GetExitCodeProcess answer
// (PROCESS_QUERY_LIMITED_INFORMATION — obtainable even for a process this
// caller doesn't own or that's running elevated, unlike the older
// PROCESS_QUERY_INFORMATION) and checks the actual exit code rather than
// treating a successful OpenProcess as "alive": a handle can still open
// successfully for a moment after the process object exits but before the
// last handle to it closes.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // no such process, or one this caller isn't permitted to query.
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}

// sendTerminate has no graceful equivalent on Windows for a detached process
// with no console of its own: there is no signal delivery, and
// GenerateConsoleCtrlEvent only reaches processes attached to the sender's
// own console, which a DETACHED_PROCESS child never is. Killing directly is
// a known v1 limitation, not an oversight — the daemon's state backends
// (SQLite/WAL/hybrid) all tolerate an unclean stop already: the hybrid
// store's pending log replays whatever didn't reach SQLite on the next
// start, the same tolerance cmd_serve.go's closeStoreBounded relies on for
// its own graceful-shutdown timeout path.
func sendTerminate(proc *os.Process) error {
	return proc.Kill()
}
