//go:build unix

package compat

import (
	"os/exec"
	"syscall"
)

// detachProcGroup runs the child in its own process group so it does not
// receive SIGINT when the user presses Ctrl+C on the parent. This lets the
// orchestrator gracefully send shutdown commands before the OS kills the
// child.
func detachProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
