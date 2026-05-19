// Package awscli provides helpers for calling the AWS CLI as a subprocess.
// Each helper runs `aws <service> <subcommand> ...`, configures endpoint/region
// via flags, and parses the JSON output. A non-zero exit code is returned as an
// error; the raw stderr is included in the error message.
package awscli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunOutput runs `aws [args...]` and returns the parsed JSON output.
// endpoint and region are injected automatically.
func RunOutput(endpoint, region string, args ...string) (map[string]any, error) {
	b, err := runRaw(endpoint, region, args...)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("awscli: parse output: %w\nraw: %s", err, b)
	}
	return out, nil
}

// Run runs `aws [args...]` and discards the output. Returns an error on non-zero exit.
func Run(endpoint, region string, args ...string) error {
	_, err := runRaw(endpoint, region, args...)
	return err
}

// RunWithStdin runs `aws [args...]` with the given string piped to stdin.
// Returns an error on non-zero exit.
func RunWithStdin(endpoint, region string, input string, args ...string) error {
	_, err := runRawWithStdin(endpoint, region, input, args...)
	return err
}

// RunOutputWithStdin runs `aws [args...]` with input piped to stdin and returns the parsed JSON output.
func RunOutputWithStdin(endpoint, region string, input string, args ...string) (map[string]any, error) {
	b, err := runRawWithStdin(endpoint, region, input, args...)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("awscli: parse output: %w\nraw: %s", err, b)
	}
	return out, nil
}

func runRaw(endpoint, region string, args ...string) ([]byte, error) {
	return runRawWithStdin(endpoint, region, "", args...)
}

func runRawWithStdin(endpoint, region string, input string, args ...string) ([]byte, error) {
	// When input is provided alongside /dev/stdin in args, the AWS CLI v2
	// cannot read from /dev/stdin (it only accepts regular file paths for blob
	// parameters). Write the input to a temp file and substitute its path.
	resolvedArgs := args
	if input != "" {
		for _, a := range args {
			if a == "/dev/stdin" {
				f, err := os.CreateTemp("", "awscli-body-*")
				if err != nil {
					return nil, fmt.Errorf("awscli: create temp body file: %w", err)
				}
				defer os.Remove(f.Name()) //nolint:errcheck
				if _, err := f.WriteString(input); err != nil {
					f.Close() //nolint:errcheck
					return nil, fmt.Errorf("awscli: write temp body file: %w", err)
				}
				f.Close() //nolint:errcheck
				// Replace /dev/stdin with the temp file path.
				newArgs := make([]string, len(args))
				copy(newArgs, args)
				for i, v := range newArgs {
					if v == "/dev/stdin" {
						newArgs[i] = f.Name()
					}
				}
				resolvedArgs = newArgs
				input = "" // consumed by temp file; no longer pipe stdin
				break
			}
		}
	}

	all := append(
		[]string{
			"--endpoint-url", endpoint,
			"--region", region,
			"--output", "json",
			"--no-sign-request",
		},
		resolvedArgs...,
	)
	cmd := exec.Command("aws", all...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	if err := cmd.Run(); err != nil {
		raw := strings.TrimSpace(stderr.String())
		// Include stdout too — the AWS CLI sometimes puts error JSON there.
		if stdout.Len() > 0 {
			raw += "\n" + strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("aws %s: %w: %s", strings.Join(args[:minArgs(args, 2)], " "), err, raw)
	}
	return stdout.Bytes(), nil
}

func minArgs(args []string, n int) int {
	if len(args) < n {
		return len(args)
	}
	return n
}
