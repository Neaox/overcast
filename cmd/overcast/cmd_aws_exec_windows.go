//go:build windows

package main

// cmd_aws_exec_windows.go — Windows half of `overcast aws`'s handoff to the
// real `aws` binary. Windows has no exec(2): there is no way to replace this
// process's image with the child's, so unlike cmd_aws_exec.go this runs aws
// as a real child process and waits for it.
//
// Two things that fall out of running as a child instead of exec'ing:
//
//   - Ctrl+C: without intervention, Go's default SIGINT/console-control-event
//     handling could tear this process down while the child is mid-request.
//     signal.Ignore(os.Interrupt) makes this process immune to it, so the
//     event reaches only the child (Windows delivers CTRL_C_EVENT to the
//     whole console process group, aws included) — the aws CLI decides for
//     itself how to handle its own interrupt.
//   - Exit code: os/exec does not let a child's exit code become this
//     process's exit code by itself. cmd.Run() folds a nonzero exit into a
//     generic *exec.ExitError, so it is unwrapped here into an *awsExitError
//     carrying just the code; runAWS (cmd_aws.go) is the one place that
//     understands that type and calls os.Exit with it after its own cleanup
//     (a plain `return err` would otherwise print the error AND still exit
//     1, losing the real code and doubling up on noise).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

func execAWS(binPath string, args []string, env []string) error {
	signal.Ignore(os.Interrupt)

	cmd := exec.Command(binPath, args...) //nolint:gosec // binPath is resolved via exec.LookPath("aws"); args/env are the user's own CLI invocation, not attacker input
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &awsExitError{code: exitErr.ExitCode()}
	}
	return fmt.Errorf("aws: run %s: %w", binPath, err)
}
