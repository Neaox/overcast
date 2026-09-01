package main

// cmd_wait.go — `overcast wait`. Polls the daemon's readiness endpoint until
// it answers 200, then exits 0 — the CI-friendly counterpart to `overcast
// status`, which checks once and reports rather than blocking. Useful in
// scripts that start `overcast serve` in the background and need to know
// when it's safe to send traffic.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until overcast is ready to accept requests",
		Args:  cobra.NoArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			interval, _ := cmd.Flags().GetDuration("interval")
			quiet, _ := cmd.Flags().GetBool("quiet")

			start := time.Now()
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			if err := waitForHealthy(ctx, endpoint, interval); err != nil {
				return fmt.Errorf("overcast did not become ready at %s after %s: %w", endpoint, time.Since(start).Round(time.Millisecond), err)
			}

			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "overcast ready at %s (%s)\n", endpoint, time.Since(start).Round(time.Millisecond))
			}
			return nil
		},
	}
	cmd.Flags().Duration("timeout", 60*time.Second, "give up waiting after this long")
	cmd.Flags().Duration("interval", 500*time.Millisecond, "how often to poll the health endpoint")
	cmd.Flags().Bool("quiet", false, "print nothing on success")
	return cmd
}

// waitForHealthy polls GET {endpoint}/_overcast/health every interval until
// it returns 200, or ctx is done. Connection failures and non-200 responses
// are both treated as "not ready yet" and simply retried — only ctx
// expiring (or being cancelled) ends the loop with an error.
func waitForHealthy(ctx context.Context, endpoint string, interval time.Duration) error {
	url := strings.TrimRight(endpoint, "/") + "/_overcast/health"
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if healthCheckOnce(ctx, url) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// healthCheckOnce makes one attempt against url, bounded to 2s regardless of
// the overall wait timeout, and reports whether it returned 200.
func healthCheckOnce(ctx context.Context, url string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Connection refused, DNS failure, timeout, or ctx cancellation —
		// all just mean "not ready yet".
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
