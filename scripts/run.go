//go:build ignore

// Script: run
// Builds and runs the overcast binary with dev defaults.
// Works identically on Mac, Linux, and Windows.
//
// Usage:
//
//	go run ./scripts/run
//	go run ./scripts/run --port=9000 --services=s3,sqs
//
// This replaces `make run` / `task run` for contributors who prefer not to
// install an additional tool. It is NOT part of the compiled binary.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

func main() {
	port := flag.String("port", envOr("OVERCAST_PORT", "4566"), "Port to listen on")
	services := flag.String("services", os.Getenv("OVERCAST_SERVICES"), "Comma-separated services to enable (default: all)")
	state := flag.String("state", envOr("OVERCAST_STATE", "hybrid"), "State backend: memory, persistent, hybrid or wal")
	logLevel := flag.String("log", envOr("OVERCAST_LOG_LEVEL", "debug"), "Log level: debug, info, warn, error")
	flag.Parse()

	// Resolve project root relative to this script file.
	// filepath.Dir(__file__) doesn't exist in Go, but os.Getwd() works when
	// running via `go run ./scripts/run` from the project root.
	root, err := os.Getwd()
	if err != nil {
		fatalf("cannot determine working directory: %v", err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fatalf("cannot create bin dir: %v", err)
	}

	binary := filepath.Join(binDir, "overcast")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	// Build — skip -trimpath and -ldflags="-w -s" during development.
	// Those flags are for release builds; in dev, they defeat the Go build
	// cache and add ~6s of extra compilation on every run.
	fmt.Println("→ building overcast...")
	buildStart := time.Now()
	build := exec.Command("go", "build",
		"-o", binary,
		"./cmd/overcast",
	)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fatalf("build failed: %v", err)
	}
	fmt.Printf("→ build complete (%.2fs)\n", time.Since(buildStart).Seconds())

	// Run
	fmt.Printf("→ starting overcast serve on :%s\n", *port)
	run := exec.Command(binary, "serve")
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Env = append(os.Environ(),
		"OVERCAST_PORT="+*port,
		"OVERCAST_STATE="+*state,
		"OVERCAST_LOG_LEVEL="+*logLevel,
		"OVERCAST_DEBUG=true",
	)
	if *services != "" {
		run.Env = append(run.Env, "OVERCAST_SERVICES="+*services)
	}

	// Start the child process and forward signals so it can shut down
	// cleanly before we exit. Without this, Go's default SIGINT handler
	// kills the wrapper and the child may not finish cleanup (e.g.
	// closing the SMTP listener), causing "address already in use" on
	// the next start.
	if err := run.Start(); err != nil {
		fatalf("failed to start server: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		// Forward signal to child; ignore error (process may already be gone).
		if run.Process != nil {
			run.Process.Signal(s)
		}
	}()

	if err := run.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return
		}
		fatalf("server exited: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "run: "+format+"\n", args...)
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
