//go:build windows

package inithooks

import (
	"context"
	"os/exec"
)

// buildScriptCmd constructs the exec.Cmd for running a script on Windows.
// Uses cmd.exe since /bin/sh is unavailable. Process group management is
// omitted; exec.CommandContext cancels via TerminateProcess on timeout.
func buildScriptCmd(ctx context.Context, path string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd.exe", "/c", path)
}
