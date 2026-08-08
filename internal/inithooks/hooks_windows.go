//go:build windows

package inithooks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// buildScriptCmd constructs the exec.Cmd for running a script on Windows.
// Hook scripts are POSIX shell scripts, so a real sh is preferred: the .sh
// file association that cmd.exe would use typically points at git-bash.exe,
// which opens a terminal window per script and does not propagate exit
// codes, so a failing hook would be reported successful. cmd.exe remains
// the fallback for machines with an association but no findable sh.
// Process group management is omitted; exec.CommandContext cancels via
// TerminateProcess on timeout.
func buildScriptCmd(ctx context.Context, path string) *exec.Cmd {
	if sh := findSh(); sh != "" {
		return exec.CommandContext(ctx, sh, path)
	}
	return exec.CommandContext(ctx, "cmd.exe", "/c", path)
}

// findSh locates a POSIX shell: sh on PATH, or the sh.exe bundled with Git
// for Windows (which installs git.exe on PATH but not its shell).
var findSh = sync.OnceValue(func() string {
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if git, err := exec.LookPath("git"); err == nil {
		sh := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "sh.exe")
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return ""
})
