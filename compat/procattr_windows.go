//go:build windows

package compat

import (
	"os/exec"
	"syscall"
)

// detachProcGroup is the Windows analogue of the Unix Setpgid variant:
// CREATE_NEW_PROCESS_GROUP keeps the child out of the console's Ctrl+C
// signal group, so the orchestrator can shut it down gracefully.
func detachProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
