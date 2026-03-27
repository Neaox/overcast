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
	"path/filepath"
	"runtime"
)

func main() {
	port := flag.String("port", envOr("OVERCAST_PORT", "4566"), "Port to listen on")
	services := flag.String("services", os.Getenv("OVERCAST_SERVICES"), "Comma-separated services to enable (default: all)")
	state := flag.String("state", envOr("OVERCAST_STATE", "memory"), "State backend: memory or sqlite")
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

	// Build
	fmt.Println("→ building overcast...")
	build := exec.Command("go", "build",
		"-trimpath",
		"-ldflags=-w -s",
		"-o", binary,
		"./cmd/overcast",
	)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fatalf("build failed: %v", err)
	}

	// Run
	fmt.Printf("→ starting overcast on :%s\n", *port)
	run := exec.Command(binary)
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	run.Env = append(os.Environ(),
		"OVERCAST_PORT="+*port,
		"OVERCAST_STATE="+*state,
		"OVERCAST_LOG_LEVEL="+*logLevel,
	)
	if *services != "" {
		run.Env = append(run.Env, "OVERCAST_SERVICES="+*services)
	}

	if err := run.Run(); err != nil {
		// Exit code 1 when the server is killed by signal is expected.
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
