//go:build !windows

package main

// cmd_aws_exec.go — unix half of `overcast aws`'s process handoff to the
// real `aws` binary. syscall.Exec replaces this process's image entirely:
// no fork, no wrapper process staying around to relay signals or exit
// status, so Ctrl+C and the exit code are the child's without any code
// here having to do anything about it. It also means nothing after a
// successful call ever runs — see the defer in runAWS's caller comment for
// what that costs (the temp AWS config file is not cleaned up on this
// path).
//
// If syscall.Exec returns at all, the exec syscall itself failed (e.g. a
// permissions problem) — not something that produces an *awsExitError, that
// type belongs to the Windows path only. Since aws was already found via
// exec.LookPath, this is a rare, environment-level failure.

import "syscall"

func execAWS(binPath string, args []string, env []string) error {
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, binPath)
	argv = append(argv, args...)
	return syscall.Exec(binPath, argv, env) //nolint:gosec // binPath is resolved via exec.LookPath("aws"); args/env are the user's own CLI invocation, not attacker input
}
