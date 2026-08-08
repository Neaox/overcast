//go:build !windows

package inithooks

// canExecHooks reports whether this machine can execute .sh hooks with real
// POSIX semantics. On Unix /bin/sh is always available.
func canExecHooks() bool { return true }
