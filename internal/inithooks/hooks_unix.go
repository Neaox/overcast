//go:build !windows

package inithooks

import (
	"context"
	"os/exec"
	"syscall"
)

// buildScriptCmd constructs the exec.Cmd for running a shell script.
// On Unix, the script runs in its own process group so a timeout can kill
// the entire tree (shell + children) rather than just the shell process.
func buildScriptCmd(ctx context.Context, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}

// runScriptCmd starts and waits for cmd. On Unix, buildScriptCmd already set
// up everything needed (process group + Cancel) for a timeout to take down
// the whole tree, so this is a plain Run.
func runScriptCmd(cmd *exec.Cmd) error {
	return cmd.Run()
}
