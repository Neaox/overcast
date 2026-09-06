// Package awscli provides helpers for calling the AWS CLI as a subprocess.
// Each helper runs `aws <service> <subcommand> ...`, configures endpoint/region
// via flags, and parses the JSON output. A non-zero exit code is returned as an
// error; the raw stderr is included in the error message.
package awscli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// RunOutput runs `aws [args...]` and returns the parsed JSON output.
// endpoint and region are injected automatically.
func RunOutput(endpoint, region string, args ...string) (map[string]any, error) {
	b, err := runRaw(endpoint, region, args...)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
}

// RunOutputContext is RunOutput bound to a context: the `aws` process is killed
// when the context is cancelled, and a context already done spawns nothing.
//
// The uncontexted helpers keep their behaviour and are what a hand-written
// group uses. This one exists for the scenario interpreter, which makes every
// call in a generated group: one `aws` that never answers would otherwise hold
// its parallel slot until the whole suite is killed — past the per-group
// timeout RunSuite sets, and past a dashboard cancellation.
func RunOutputContext(ctx context.Context, endpoint, region string, args ...string) (map[string]any, error) {
	b, _, err := runCLI(ctx, endpoint, region, runOpts{}, args...)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
}

// RunOutputContextSigned is RunOutputContext with SigV4 signing enabled, and it
// is what the scenario interpreter uses for every generated call.
//
// The suite's default of --no-sign-request is a convenience, and for most
// services it is also the harsher test. It is neither for a REST service whose
// root segments detectService does not claim: Overcast's REST fallback treats
// unsigned traffic as S3's (internal/router, addressesNonS3), so an unsigned
// call to an operation Overcast has not implemented reaches S3's wildcard and
// comes back 405 MethodNotAllowed with an S3 XML body — a `fail`, reported
// against the service under test, where a 501 would have been the honest
// `unimplemented`. internal/groups/eks.go signs by hand for exactly this
// reason; a generated group has no hand to do it with, and every operation of
// a Tier 0 service is in that position at once. Measured on `batch`, the
// corpus's first REST-JSON service: 11 of its 45 generated tests reported
// `fail` unsigned and `unimplemented` signed, against the same emulator.
//
// Signing is also simply what a production client does — the go-sdk suite,
// which agrees with this one test for test, has never had the option not to.
func RunOutputContextSigned(ctx context.Context, endpoint, region string, args ...string) (map[string]any, error) {
	b, _, err := runCLI(ctx, endpoint, region, runOpts{sign: true}, args...)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
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
	return decodeJSON(b)
}

// RunSigned runs `aws [args...]` with a SigV4-signed request instead of the
// suite's usual --no-sign-request, and discards the output.
//
// Signing is not cosmetic. Overcast serves several services off one listener,
// so where two AWS services model the same URI it picks the owner from the
// SigV4 credential scope — /applications is AppConfig's when the request is
// signed for appconfig and Service Catalog AppRegistry's otherwise. An
// unsigned caller therefore reaches the wrong service, which no production
// client ever does. Use these variants for a path a real client could only
// reach signed; everywhere else keep the unsigned default so that a route the
// emulator has not bound still falls through to whatever would answer it.
func RunSigned(endpoint, region string, args ...string) error {
	_, _, err := runCLI(context.Background(), endpoint, region, runOpts{sign: true}, args...)
	return err
}

// RunOutputSigned is RunOutput with SigV4 signing enabled — see RunSigned.
func RunOutputSigned(endpoint, region string, args ...string) (map[string]any, error) {
	b, _, err := runCLI(context.Background(), endpoint, region, runOpts{sign: true}, args...)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
}

// RunStatus runs `aws [args...]` and reports the HTTP status of the last
// response the CLI received, alongside the usual error.
//
// The AWS CLI prints an error's code but never its HTTP status, and the status
// is part of the contract: alpha.35 moved several of them (OpenSearch's
// ResourceNotFoundException is 409, Backup's errors are all 400), and a test
// that only matched the code would not have noticed. The status is read back
// out of `--debug`'s urllib3 log line rather than by hand-rolling the request,
// so the URI still comes from the pinned model. A status of 0 means the CLI
// failed before it reached the wire (a parameter validation error, say).
func RunStatus(endpoint, region string, args ...string) (int, error) {
	_, stderr, err := runCLI(context.Background(), endpoint, region, runOpts{debug: true}, args...)
	return lastHTTPStatus(stderr), err
}

// RunStatusSigned is RunStatus with SigV4 signing enabled — see RunSigned.
//
// A service reached only when signed needs both: unsigned, its negative paths
// are answered by whichever other service owns the URI, so the status read back
// would be that service's rather than the one under test.
func RunStatusSigned(endpoint, region string, args ...string) (int, error) {
	_, stderr, err := runCLI(context.Background(), endpoint, region, runOpts{sign: true, debug: true}, args...)
	return lastHTTPStatus(stderr), err
}

// httpStatusRe matches urllib3's request log line, e.g.
// `http://127.0.0.1:4566 "GET /configuration?configuration_token=x HTTP/1.1" 400 84`.
var httpStatusRe = regexp.MustCompile(`HTTP/1\.1" (\d{3})`)

// debugLineRe matches a Python logging line emitted under `--debug`, so the
// error a caller sees is the CLI's own message rather than the whole trace.
var debugLineRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d+ - `)

// lastHTTPStatus returns the status of the final request logged in stderr, or
// 0 when none was logged. The last one is the one that matters: botocore
// retries, and each attempt logs its own line.
func lastHTTPStatus(stderr string) int {
	matches := httpStatusRe.FindAllStringSubmatch(stderr, -1)
	if len(matches) == 0 {
		return 0
	}
	code, err := strconv.Atoi(matches[len(matches)-1][1])
	if err != nil {
		return 0
	}
	return code
}

// stripDebugLog drops the logging lines `--debug` adds, leaving the CLI's own
// output. Without it a failing RunStatus call would carry a megabyte of trace
// into the NDJSON `error` field.
func stripDebugLog(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if debugLineRe.MatchString(line) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// signingEnv returns the environment a signed invocation runs with.
//
// The suite configures no credentials, so a signed call has to supply a pair.
// The values are arbitrary — the emulator reads the credential scope, never the
// signature — and match what the go-sdk suite uses. Anything the developer's
// own shell exported is dropped rather than blanked: an empty AWS_PROFILE is
// not "no profile" to the CLI, it is a profile named "" that does not exist,
// and every signed call dies with "The config profile () could not be found".
func signingEnv() []string {
	drop := map[string]bool{
		"AWS_ACCESS_KEY_ID":     true,
		"AWS_SECRET_ACCESS_KEY": true,
		"AWS_SESSION_TOKEN":     true,
		"AWS_PROFILE":           true,
		"AWS_DEFAULT_PROFILE":   true,
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if drop[strings.ToUpper(name)] {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"AWS_ACCESS_KEY_ID=overcast",
		"AWS_SECRET_ACCESS_KEY=overcast",
	)
}

// commandEnv is the environment every `aws` invocation runs with: the process
// environment, the signing overrides when the call is signed, and AWS_PAGER
// blanked on all of them.
//
// The pager is not optional politeness. The CLI pipes output through
// $AWS_PAGER, $PAGER or its built-in default, and a pager with no terminal to
// draw on blocks forever holding the pipe runCLI is reading — the hang the
// repository's own awslocal wrapper exists to prevent (root AGENTS.md
// § Calling the AWS CLI). It applies to the unsigned majority as much as to a
// signed call, so it is set here rather than in signingEnv.
func commandEnv(sign bool) []string {
	env := os.Environ()
	if sign {
		env = signingEnv()
	}
	return append(env, "AWS_PAGER=")
}

func decodeJSON(b []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("awscli: parse output: %w\nraw: %s", err, b)
	}
	return out, nil
}

// runOpts carries the per-call variations on top of the standard invocation.
type runOpts struct {
	// input is piped to the CLI's stdin, or substituted into a /dev/stdin
	// argument as a temp file — see runCLI.
	input string
	// sign sends a SigV4-signed request instead of --no-sign-request.
	sign bool
	// debug adds --debug so the caller can read the HTTP status back out of
	// the trace.
	debug bool
}

func runRaw(endpoint, region string, args ...string) ([]byte, error) {
	return runRawWithStdin(endpoint, region, "", args...)
}

func runRawWithStdin(endpoint, region string, input string, args ...string) ([]byte, error) {
	b, _, err := runCLI(context.Background(), endpoint, region, runOpts{input: input}, args...)
	return b, err
}

func runCLI(ctx context.Context, endpoint, region string, o runOpts, args ...string) ([]byte, string, error) {
	input := o.input
	// When input is provided alongside /dev/stdin in args, the AWS CLI v2
	// cannot read from /dev/stdin (it only accepts regular file paths for blob
	// parameters). Write the input to a temp file and substitute its path.
	resolvedArgs := args
	if input != "" {
		for _, a := range args {
			if a == "/dev/stdin" {
				f, err := os.CreateTemp("", "awscli-body-*")
				if err != nil {
					return nil, "", fmt.Errorf("awscli: create temp body file: %w", err)
				}
				defer os.Remove(f.Name()) //nolint:errcheck
				if _, err := f.WriteString(input); err != nil {
					f.Close() //nolint:errcheck
					return nil, "", fmt.Errorf("awscli: write temp body file: %w", err)
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

	flags := []string{
		"--endpoint-url", endpoint,
		"--region", region,
		"--output", "json",
	}
	if !o.sign {
		flags = append(flags, "--no-sign-request")
	}
	if o.debug {
		flags = append(flags, "--debug")
	}
	cmd := exec.CommandContext(ctx, "aws", append(flags, resolvedArgs...)...)
	cmd.Env = commandEnv(o.sign)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	if err := cmd.Run(); err != nil {
		raw := strings.TrimSpace(stderr.String())
		if o.debug {
			raw = stripDebugLog(raw)
		}
		// Include stdout too — the AWS CLI sometimes puts error JSON there.
		if stdout.Len() > 0 {
			raw += "\n" + strings.TrimSpace(stdout.String())
		}
		return nil, stderr.String(), fmt.Errorf("aws %s: %w: %s", strings.Join(args[:minArgs(args, 2)], " "), err, raw)
	}
	return stdout.Bytes(), stderr.String(), nil
}

func minArgs(args []string, n int) int {
	if len(args) < n {
		return len(args)
	}
	return n
}
