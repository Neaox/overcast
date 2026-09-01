package main

// cmd_reset.go — `overcast reset [service]`. POSTs to the daemon's always-on
// reset endpoint (POST /_overcast/reset, or /_overcast/reset/{service} for a
// single service) — see internal/router/reset.go. Reset was moved out from
// under the OVERCAST_DEBUG gate on 2026-09-01: OVERCAST_DEBUG exists to gate
// expensive or leaky instrumentation (state dumps, request tracing, pprof),
// and reset is neither — it also grants no destructive power beyond what the
// unauthenticated AWS API surface already exposes (any caller can already
// delete every resource one at a time through ordinary AWS calls). It is
// still destructive to run by accident, though, so an interactive terminal
// is asked to confirm unless --yes is given; a non-interactive caller (CI,
// scripts, a pipe) proceeds without prompting.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// resetHTTPTimeout bounds the reset request itself. Longer than the 2s used
// by the plain-GET commands (status, services): a reset can have real work
// to do — sweeping every namespace of a large SQLite-backed store — so a
// short timeout would fail a reset that was actually still in progress.
const resetHTTPTimeout = 30 * time.Second

// resetCompletionTimeout bounds the /_overcast/health probe used for
// [service] completion. Short and deliberately so: a completion function
// runs on every tab-press, and must never make the prompt feel like it hung
// — see completeResetServiceArgs.
const resetCompletionTimeout = 1 * time.Second

// resetStdinIsTerminal reports whether the real process stdin is an
// interactive terminal. A package-level seam (mirroring the withTLSSeams
// pattern in tls_settings_test.go) so tests can simulate both an
// interactive and a non-interactive (CI, piped) stdin without needing an
// actual terminal.
var resetStdinIsTerminal = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

func newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset [service]",
		Short: "Wipe emulated state",
		Long: "Wipe all emulated state, or the state for a single service.\n\n" +
			"This is destructive. In an interactive terminal you are asked to\n" +
			"confirm what will be wiped unless --yes is given; a non-interactive\n" +
			"caller (CI, scripts, a pipe) proceeds without prompting.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeResetServiceArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			yes, _ := cmd.Flags().GetBool("yes")
			var service string
			if len(args) > 0 {
				service = args[0]
			}

			if !yes && resetStdinIsTerminal() {
				confirmed, err := confirmReset(cmd, endpoint, service)
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "aborted")
					return nil
				}
			}

			return runReset(cmd, endpoint, service)
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// confirmReset prints what will be wiped and reads a y/N answer from stdin,
// reporting whether the caller confirmed. It reads via cmd.InOrStdin() (not
// os.Stdin directly) so tests can supply canned input.
func confirmReset(cmd *cobra.Command, endpoint, service string) (bool, error) {
	if service != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "This will wipe all %s state at %s.\n", service, endpoint)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "This will wipe all emulated state at %s.\n", endpoint)
	}
	fmt.Fprint(cmd.OutOrStdout(), "Continue? [y/N] ")

	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		// EOF (e.g. stdin closed mid-prompt) reads the same as a bare Enter:
		// the default answer, "no".
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

// resetResponse is the JSON body POST /_overcast/reset[/{service}] returns on
// success. Field names mirror the map[string]string the router hand-builds
// in internal/router/reset.go — kept in sync by hand since the CLI must not
// import that package.
type resetResponse struct {
	Status  string `json:"status"`
	Service string `json:"service,omitempty"`
}

// resetErrorResponse is the JSON body POST /_overcast/reset/{service} returns
// for an unrecognized service (400).
type resetErrorResponse struct {
	Error string `json:"error"`
}

// runReset issues the actual POST and reports the outcome. endpoint and
// service are exactly what the caller already confirmed (or skipped
// confirming via --yes / a non-interactive stdin).
func runReset(cmd *cobra.Command, endpoint, service string) error {
	url := strings.TrimRight(endpoint, "/") + "/_overcast/reset"
	if service != "" {
		url += "/" + service
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), resetHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("overcast unreachable at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest {
			var errBody resetErrorResponse
			// No "overcast:" prefix — main.go's error path already adds one.
			if err := json.NewDecoder(resp.Body).Decode(&errBody); err == nil && errBody.Error != "" {
				return fmt.Errorf("%s", errBody.Error)
			}
		}
		return fmt.Errorf("overcast returned %s", resp.Status)
	}

	var result resetResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// The reset itself already succeeded (200) — a body this build
		// cannot parse (older daemon, proxy) is not worth failing over.
		fmt.Fprintf(cmd.OutOrStdout(), "overcast reset OK at %s\n", endpoint)
		return nil
	}
	if result.Service != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "overcast reset %s state OK at %s\n", result.Service, endpoint)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "overcast reset all state OK at %s\n", endpoint)
	}
	return nil
}

// completeResetServiceArgs completes the [service] argument from the
// daemon's own enabled-services list (GET /_overcast/health), the same
// source `overcast services` reads. Mirrors completeAWSArgs in cmd_aws.go:
// a completion function must never error or hang the prompt, so any failure
// — daemon unreachable, bad response, timeout — falls back to no
// candidates rather than surfacing an error.
func completeResetServiceArgs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		// Args: cobra.MaximumNArgs(1) — one positional value is all this
		// command ever takes.
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	endpoint, _ := cmd.Flags().GetString("endpoint")
	services, err := fetchResetCompletionServices(cmd.Context(), endpoint)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return services, cobra.ShellCompDirectiveNoFileComp
}

// fetchResetCompletionServices fetches and decodes GET
// {endpoint}/_overcast/health, bounded to resetCompletionTimeout, returning
// just the enabled-service names.
func fetchResetCompletionServices(ctx context.Context, endpoint string) ([]string, error) {
	url := strings.TrimRight(endpoint, "/") + "/_overcast/health"

	reqCtx, cancel := context.WithTimeout(ctx, resetCompletionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overcast returned %s", resp.Status)
	}

	var health struct {
		Services []string `json:"services"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, err
	}
	return health.Services, nil
}
