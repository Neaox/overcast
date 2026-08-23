//go:build !linux

package lambdainit

import (
	"fmt"
	"os"
)

// Main is the init's entry point. Off Linux it is a stub: the init runs inside
// a Lambda container and nowhere else, and everything it does — non-blocking
// pipe reads through RawConn, SIGCHLD reaping, wait4 exit status decoding — is
// Linux-only. The stub exists so that `go build ./...`, `go vet ./...` and
// `go test ./...` keep working on a Windows or macOS development host, which
// AGENTS.md requires of a bare checkout.
//
// It returns the process exit code; argv and environ are os.Args and
// os.Environ().
func Main(argv []string, environ []string) int {
	_, _ = argv, environ
	fmt.Fprintln(os.Stderr, "[overcast-init] lambda-init runs only on linux")
	return exitConfig
}
