//go:build windows

package inithooks

// canExecHooks reports whether this machine can execute .sh hooks with real
// POSIX semantics. Without a findable sh, execution falls back to the .sh
// file association, whose behaviour is machine-dependent and untestable.
func canExecHooks() bool { return findSh() != "" }
