//go:build ignore

// Script: check-tools
// Verifies that all required development tools are installed and accessible.
// Run this after cloning to confirm your environment is ready.
//
// Usage: go run ./scripts/check-tools
package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type tool struct {
	name     string
	cmd      string
	args     []string
	required bool
	hint     string
}

var tools = []tool{
	{
		name:     "Go",
		cmd:      "go",
		args:     []string{"version"},
		required: true,
		hint:     "Install from https://go.dev/dl/ (1.24+ required)",
	},
	{
		name:     "Git",
		cmd:      "git",
		args:     []string{"--version"},
		required: true,
		hint:     "Install from https://git-scm.com",
	},
	{
		name:     "golangci-lint",
		cmd:      "golangci-lint",
		args:     []string{"version"},
		required: false, // required for lint, not for build/test
		hint:     "Install golangci-lint v1.64.8 from https://golangci-lint.run/usage/install/",
	},
	{
		name:     "Docker",
		cmd:      "docker",
		args:     []string{"--version"},
		required: false,
		hint:     "Install Docker Desktop from https://docs.docker.com/get-docker/",
	},
	{
		name:     "Task",
		cmd:      "task",
		args:     []string{"--version"},
		required: runtime.GOOS == "windows", // required on Windows, optional elsewhere
		hint:     "Mac/Linux: brew install go-task | Windows: scoop install task",
	},
	{
		name:     "Node.js",
		cmd:      "node",
		args:     []string{"--version"},
		required: false,
		hint:     "Only needed for Lambda work. Install from https://nodejs.org",
	},
	{
		name:     "air",
		cmd:      "air",
		args:     []string{"-v"},
		required: false,
		hint:     "GOTOOLCHAIN=auto go install github.com/air-verse/air@latest (requires Go 1.25+)",
	},
}

func main() {
	fmt.Printf("Checking development tools on %s/%s...\n\n", runtime.GOOS, runtime.GOARCH)

	allOK := true
	for _, t := range tools {
		status, version := check(t)
		indicator := "✓"
		if !status {
			indicator = "✗"
			if t.required {
				allOK = false
				indicator = "✗ REQUIRED"
			}
		}
		if version != "" {
			fmt.Printf("  %s  %-20s %s\n", indicator, t.name, version)
		} else {
			fmt.Printf("  %s  %-20s not found — %s\n", indicator, t.name, t.hint)
		}
	}

	fmt.Println()
	if allOK {
		fmt.Println("All required tools are available.")
		fmt.Println("Run `go mod tidy` then `go test ./...` to verify your setup.")
	} else {
		fmt.Println("Some required tools are missing. Install them before contributing.")
		os.Exit(1)
	}

	// Race detector check
	if runtime.GOOS == "windows" && runtime.GOARCH != "amd64" {
		fmt.Printf("\nNote: the race detector (-race) is not supported on %s/%s.\n", runtime.GOOS, runtime.GOARCH)
		fmt.Println("Tests will run without -race locally. CI handles race detection on supported platforms.")
	}
}

func check(t tool) (ok bool, version string) {
	cmd := exec.Command(t.cmd, t.args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, ""
	}
	// Extract the first line of output as the version string.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 {
		return true, lines[0]
	}
	return true, ""
}
