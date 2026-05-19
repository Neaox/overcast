package main

// cmd_status.go — `overcast status`. Pings overcast's /health endpoint
// and reports whether the daemon is reachable. Deliberately minimal; the
// goal is a human-friendly one-liner, not a dashboard.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check that overcast is reachable",
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			url := strings.TrimRight(endpoint, "/") + "/health"

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("overcast unreachable at %s: %w", endpoint, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("overcast returned %s", resp.Status)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "overcast OK at %s\n", endpoint)
			return nil
		},
	}
}
