package main

// cmd_aws.go — `overcast aws <args...>`. Runs the host `aws` CLI (never
// bundled — resolved via exec.LookPath) against a running overcast, with the
// ambient AWS environment scrubbed first. This is scripts/awslocal.sh and
// scripts/awslocal.ps1 ported into the binary: same rationale (a developer
// machine usually carries AWS_PROFILE, AWS_REGION, SSO state or a stale
// AWS_ENDPOINT_URL that silently changes what an `aws` call does), same fix
// (unset every AWS_* variable, not a curated list, so a future SDK release
// needs no update here), just without the "activate a shell wrapper first"
// step.
//
// DisableFlagParsing is set so every argument after "aws" — including
// AWS-CLI-native flags like --debug or --profile — passes through to the
// child verbatim; overcast's own flag parser never gets a chance to
// misinterpret them. The one piece of overcast's own surface that can still
// appear on this command line is the root --endpoint flag, and only when
// given *before* "aws" (`overcast --endpoint X aws ...`) — Cobra resolves
// that positionally while locating the subcommand, but because this command
// disables flag parsing entirely, Cobra never actually *parses* it into the
// flag's Value (see extractLeadingEndpointFlag). --endpoint given after
// "aws" is just another passthrough argument, same as any other aws-CLI
// flag.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// caFetchTimeout bounds the CA bootstrap fetch below. Short, unlike
// trust.FetchRemoteCA's 10s: this runs on every `overcast aws` invocation
// against an https endpoint, not just an explicit one-off `trust install`,
// so a dead daemon must fail fast rather than stall every AWS call.
const caFetchTimeout = 3 * time.Second

// maxCAPemBytes bounds the fetch: a CA certificate PEM is a couple of KiB;
// anything near this is not one. Mirrors trust.maxCAPemBytes (unexported).
const maxCAPemBytes = 64 * 1024

func newAWSCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "aws [args...]",
		Short: "Run the host AWS CLI against overcast, with the AWS environment scrubbed first",
		Long: "Run the host AWS CLI against overcast, with the AWS environment scrubbed first.\n\n" +
			"Every AWS_* environment variable is unset (not just a curated list) and\n" +
			"replaced with the four an overcast call needs: dummy credentials, region,\n" +
			"and the overcast endpoint. This avoids the class of bug where a leftover\n" +
			"AWS_PROFILE or AWS_ENDPOINT_URL from something else silently redirects\n" +
			"or re-signs the call.\n\n" +
			"All arguments are passed through to `aws` verbatim, including flags such\n" +
			"as --debug. The root --endpoint flag works when given before \"aws\"\n" +
			"(overcast --endpoint http://localhost:4580 aws s3 ls); otherwise the\n" +
			"OVERCAST_ENDPOINT / OVERCAST_PORT environment variables apply, same as\n" +
			"scripts/awslocal.sh.",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE:               runAWS,
	}
}

// awsExitError carries a child process's exit code through the normal Go
// error path so the caller can propagate it exactly via os.Exit. Only the
// Windows exec path (cmd_aws_exec_windows.go) ever produces one: the unix
// path (cmd_aws_exec.go) replaces the process image with syscall.Exec, so a
// nonzero aws exit status is never seen by this process at all — it *is*
// this process's exit status.
type awsExitError struct{ code int }

func (e *awsExitError) Error() string { return fmt.Sprintf("aws exited with status %d", e.code) }

func runAWS(cmd *cobra.Command, args []string) error {
	binPath, err := exec.LookPath("aws")
	if err != nil {
		return errors.New("aws CLI not found on PATH — install it from https://docs.aws.amazon.com/cli/ or use 'overcast env' to configure another tool")
	}

	endpointOverride, overrideGiven, rest := extractLeadingEndpointFlag(args)
	rest = stripLeadingDoubleDash(rest)

	endpoint := resolveAWSEndpoint(cmd, endpointOverride, overrideGiven)
	region := resolveAWSRegion()

	configPath, err := createEmptyAWSConfigFile()
	if err != nil {
		return err
	}
	defer os.Remove(configPath) //nolint:errcheck // best-effort; see the awsExitError branch below for the one path that must clean up explicitly

	caBundle := ""
	if isHTTPSEndpoint(endpoint) {
		home, herr := os.UserHomeDir()
		if herr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "overcast: could not resolve home directory for the overcast CA cache: %v\n", herr)
		} else {
			path, warning := ensureCABundle(cmd.Context(), endpoint, home, nil)
			if warning != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), warning)
			}
			caBundle = path
		}
	}

	env, err := buildAWSEnv(os.Environ(), endpoint, region, configPath, caBundle)
	if err != nil {
		return err
	}

	execErr := execAWS(binPath, rest, env)
	var exitErr *awsExitError
	if errors.As(execErr, &exitErr) {
		// os.Exit skips deferred calls, so clean up explicitly before it.
		os.Remove(configPath) //nolint:errcheck
		os.Exit(exitErr.code)
	}
	return execErr
}

// extractLeadingEndpointFlag pulls a leading "--endpoint VALUE" or
// "--endpoint=VALUE" off the front of args. Cobra locates this command by
// stripping recognized root flags (including --endpoint) from the front of
// the raw argument list while searching for "aws", so if the user gave
// --endpoint before the subcommand it is necessarily the first (or first
// two) elements of args here — nothing later in args can be it, because
// DisableFlagParsing means Cobra never parses these tokens itself.
func extractLeadingEndpointFlag(args []string) (value string, given bool, rest []string) {
	if len(args) == 0 {
		return "", false, args
	}
	switch {
	case args[0] == "--endpoint" && len(args) >= 2:
		return args[1], true, args[2:]
	case strings.HasPrefix(args[0], "--endpoint="):
		return strings.TrimPrefix(args[0], "--endpoint="), true, args[1:]
	}
	return "", false, args
}

// stripLeadingDoubleDash drops one leading "--" separator, if present,
// before the args are handed to the aws CLI. A user disambiguating their
// own args from overcast's (`overcast aws -- s3 ls`) would otherwise pass a
// stray "--" through to aws.
func stripLeadingDoubleDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

// resolveAWSEndpoint applies the flag-value > OVERCAST_ENDPOINT >
// OVERCAST_PORT > flag-default precedence scripts/awslocal.sh and
// scripts/awslocal.ps1 use, given an explicit override extracted from a
// leading --endpoint (see extractLeadingEndpointFlag) since Cobra itself
// never parses that flag on this DisableFlagParsing command.
func resolveAWSEndpoint(cmd *cobra.Command, override string, overrideGiven bool) string {
	f := cmd.Flags().Lookup("endpoint")
	current, defaultValue := "", ""
	if f != nil {
		current, defaultValue = f.Value.String(), f.DefValue
	}
	return resolveAWSEndpointValue(current, defaultValue, override, overrideGiven)
}

// resolveAWSEndpointValue is the pure decision behind resolveAWSEndpoint,
// factored out so the precedence can be unit tested without building a
// cobra.Command.
func resolveAWSEndpointValue(currentFlagValue, defaultFlagValue, override string, overrideGiven bool) string {
	if overrideGiven {
		return override
	}
	if currentFlagValue != defaultFlagValue {
		// The flag was actually parsed to a non-default value — not
		// reachable today given DisableFlagParsing, but honored in case
		// that ever changes upstream.
		return currentFlagValue
	}
	if v := os.Getenv("OVERCAST_ENDPOINT"); v != "" {
		return v
	}
	if v := os.Getenv("OVERCAST_PORT"); v != "" {
		return "http://localhost:" + v
	}
	return currentFlagValue
}

// resolveAWSRegion applies the OVERCAST_REGION > us-east-1 precedence
// scripts/awslocal.sh and scripts/awslocal.ps1 use.
func resolveAWSRegion() string {
	if v := os.Getenv("OVERCAST_REGION"); v != "" {
		return v
	}
	return "us-east-1"
}

// createEmptyAWSConfigFile creates the empty file AWS_CONFIG_FILE and
// AWS_SHARED_CREDENTIALS_FILE both point at, so a profile is never resolved
// from ~/.aws even when no AWS_PROFILE is set (mirrors scripts/awslocal.sh's
// mktemp; unlike the shell scripts we don't need the cygpath dance, since
// this binary IS the native process, not something Git Bash's `aws` shim has
// to translate a path for).
func createEmptyAWSConfigFile() (string, error) {
	f, err := os.CreateTemp("", "overcast-aws-config-*")
	if err != nil {
		return "", fmt.Errorf("aws: create empty AWS config file: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("aws: close empty AWS config file: %w", err)
	}
	return path, nil
}

// buildAWSEnv builds the child process environment from environ (normally
// os.Environ()): every AWS_* variable is dropped, then the fixed set an
// overcast call needs is appended. Pure and independent of the OS process,
// so tests can drive it with a fabricated environ.
func buildAWSEnv(environ []string, endpoint, region, configFile, caBundle string) ([]string, error) {
	if endpoint == "" {
		return nil, errors.New("aws: empty endpoint")
	}
	if configFile == "" {
		return nil, errors.New("aws: empty AWS config file path")
	}
	if region == "" {
		region = "us-east-1"
	}

	out := make([]string, 0, len(environ)+10)
	for _, kv := range environ {
		if strings.HasPrefix(kv, "AWS_") {
			continue
		}
		out = append(out, kv)
	}

	out = append(out,
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_DEFAULT_REGION="+region,
		"AWS_REGION="+region,
		"AWS_ENDPOINT_URL="+endpoint,
		"AWS_PAGER=",
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE="+configFile,
		"AWS_SHARED_CREDENTIALS_FILE="+configFile,
	)
	if caBundle != "" {
		out = append(out, "AWS_CA_BUNDLE="+caBundle)
	}
	return out, nil
}

func isHTTPSEndpoint(endpoint string) bool {
	return strings.HasPrefix(strings.ToLower(endpoint), "https://")
}

// caCachePath is where a fetched CA for endpoint is cached:
// <homeDir>/.overcast/ca/<sha256(endpoint)>.pem. Keyed by the endpoint's
// full text (not just host:port) so http and https spellings of the same
// daemon do not collide — an unlikely mix-up, but the hash is free either
// way. Deliberately separate from internal/hostbridge/trust's own cache
// (<dataDir>/ca-remote/<host_port>/ca.crt, used by `overcast trust`): that
// one is a trust-store install target with install/uninstall lifecycle
// tracked against it, this one is just a fetch-and-reuse cache for a single
// process's AWS_CA_BUNDLE and has no business sharing that directory.
func caCachePath(homeDir, endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return filepath.Join(homeDir, ".overcast", "ca", hex.EncodeToString(sum[:])+".pem")
}

// ensureCABundle fetches endpoint's CA certificate (see the file comment on
// fetchCABundle for why the fetch itself is unverified) and caches it at
// caCachePath(homeDir, endpoint), returning that path. A failed fetch falls
// back to a cache entry from an earlier run if one exists; if neither is
// available it returns a one-line warning instead of an error, since a
// missing CA bundle degrades to "TLS verification may fail" rather than
// blocking the command entirely — the caller should print the warning and
// carry on without AWS_CA_BUNDLE.
//
// client is normally nil (the real insecure bootstrap client is built
// internally); tests pass one pointed at a local test server.
func ensureCABundle(ctx context.Context, endpoint, homeDir string, client *http.Client) (path, warning string) {
	path = caCachePath(homeDir, endpoint)

	body, fetchErr := fetchCABundle(ctx, endpoint, client)
	if fetchErr == nil {
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr == nil {
			if wErr := os.WriteFile(path, body, 0o644); wErr == nil {
				return path, ""
			}
		}
	}

	if _, statErr := os.Stat(path); statErr == nil {
		return path, ""
	}

	if fetchErr == nil {
		fetchErr = fmt.Errorf("could not write CA cache at %s", path)
	}
	return "", fmt.Sprintf(
		"overcast: could not obtain the overcast CA for %s (%v) — HTTPS aws calls may fail TLS verification; run 'overcast trust install --endpoint %s' or retry once the daemon is reachable",
		endpoint, fetchErr, endpoint)
}

// fetchCABundle GETs endpoint+trust.CAPemPath. TLS verification is
// deliberately disabled for this one request, same rationale as
// internal/hostbridge/trust/remote.go: the whole point of the fetch is to
// establish trust in the first place, so verifying it against that same
// not-yet-established trust would prove nothing. This is bounded to
// loopback/self-controlled endpoints by the same expectation trust install
// relies on (overcast's own --endpoint targets a daemon the caller chose).
func fetchCABundle(ctx context.Context, endpoint string, client *http.Client) ([]byte, error) {
	if client == nil {
		client = &http.Client{
			Timeout: caFetchTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see func comment
			},
		}
	}

	ctx, cancel := context.WithTimeout(ctx, caFetchTimeout)
	defer cancel()

	url := strings.TrimRight(endpoint, "/") + trust.CAPemPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build CA request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCAPemBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > maxCAPemBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes — not a CA certificate", url, maxCAPemBytes)
	}
	return body, nil
}
