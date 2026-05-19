//go:build ignore

// Script: debug
// Queries debug endpoints on a running Overcast instance.
// Requires OVERCAST_DEBUG=true on the server.
//
// Usage:
//
//	go run ./scripts/debug.go                     # list commands
//	go run ./scripts/debug.go goroutines          # goroutine stack dump
//	go run ./scripts/debug.go goroutines full     # verbose stacks
//	go run ./scripts/debug.go heap                # heap profile (text)
//	go run ./scripts/debug.go cpu [seconds]       # CPU profile (saves to file)
//	go run ./scripts/debug.go health              # health check
//	go run ./scripts/debug.go config              # server config
//	go run ./scripts/debug.go metrics             # runtime metrics
//	go run ./scripts/debug.go state               # all state namespaces
//	go run ./scripts/debug.go state <ns>          # single namespace
//	go run ./scripts/debug.go reset               # reset all state
//	go run ./scripts/debug.go reset <service>     # reset one service
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var base string

func init() {
	port := envOr("OVERCAST_PORT", "4566")
	host := envOr("OVERCAST_HOST", "localhost")
	base = fmt.Sprintf("http://%s:%s", host, port)
}

type command struct {
	name  string
	args  string
	desc  string
	run   func(args []string)
}

var commands = []command{
	{"goroutines", "[full]", "Dump goroutine stacks (full = verbose with all frames)", cmdGoroutines},
	{"heap", "", "Heap profile in text format", cmdHeap},
	{"cpu", "[seconds]", "Capture CPU profile (default: 10s, saves to cpu.prof)", cmdCPU},
	{"health", "", "Server health check", cmdHealth},
	{"config", "", "Server configuration", cmdConfig},
	{"metrics", "", "Runtime metrics snapshot", cmdMetrics},
	{"state", "[namespace]", "Dump state (all or single namespace)", cmdState},
	{"reset", "[service]", "Reset state (all or single service)", cmdReset},
	{"pprof", "<profile>", "Raw pprof profile (goroutine, heap, allocs, block, mutex)", cmdPprof},
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	name := os.Args[1]
	for _, cmd := range commands {
		if cmd.name == name {
			cmd.run(os.Args[2:])
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", name)
	printUsage()
	os.Exit(1)
}

func printUsage() {
	fmt.Println("Overcast Debug Tool")
	fmt.Println()
	fmt.Printf("  Target: %s\n", base)
	fmt.Printf("  Set OVERCAST_HOST / OVERCAST_PORT to change.\n")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println()
	for _, cmd := range commands {
		args := cmd.name
		if cmd.args != "" {
			args += " " + cmd.args
		}
		fmt.Printf("  %-28s %s\n", args, cmd.desc)
	}
	fmt.Println()
	fmt.Println("Usage: go run ./scripts/debug.go <command> [args]")
}

// ── Commands ──────────────────────────────────────────────────────────────

func cmdGoroutines(args []string) {
	debug := "1"
	if len(args) > 0 && args[0] == "full" {
		debug = "2"
	}
	get(fmt.Sprintf("/_debug/pprof/goroutine?debug=%s", debug))
}

func cmdHeap(args []string) {
	get("/_debug/pprof/heap?debug=1")
}

func cmdCPU(args []string) {
	seconds := "10"
	if len(args) > 0 {
		seconds = args[0]
	}
	outFile := "cpu.prof"
	fmt.Fprintf(os.Stderr, "Capturing CPU profile for %ss → %s ...\n", seconds, outFile)

	resp, err := httpClient().Get(base + "/_debug/pprof/profile?seconds=" + seconds)
	if err != nil {
		fatal("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fatal("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	f, err := os.Create(outFile)
	if err != nil {
		fatal("create %s: %v", outFile, err)
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		fatal("write %s: %v", outFile, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %d bytes to %s\n", n, outFile)
	fmt.Fprintf(os.Stderr, "Analyze with: go tool pprof %s\n", outFile)
}

func cmdHealth(_ []string) {
	get("/_debug/health")
}

func cmdConfig(_ []string) {
	get("/_debug/config")
}

func cmdMetrics(_ []string) {
	get("/_metrics")
}

func cmdState(args []string) {
	if len(args) > 0 {
		get("/_debug/state/" + args[0])
	} else {
		get("/_debug/state")
	}
}

func cmdReset(args []string) {
	path := "/_debug/reset"
	if len(args) > 0 {
		path += "/" + args[0]
	}
	fmt.Fprintf(os.Stderr, "POST %s%s\n", base, path)
	resp, err := httpClient().Post(base+path, "", nil)
	if err != nil {
		fatal("request failed: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func cmdPprof(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: debug pprof <profile>")
		fmt.Fprintln(os.Stderr, "Profiles: goroutine, heap, allocs, block, mutex, threadcreate")
		os.Exit(1)
	}
	get("/_debug/pprof/" + args[0] + "?debug=1")
}

// ── Helpers ───────────────────────────────────────────────────────────────

func get(path string) {
	resp, err := httpClient().Get(base + path)
	if err != nil {
		fatal("request failed: %v\nIs the server running at %s with OVERCAST_DEBUG=true?", err, base)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fatal("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
