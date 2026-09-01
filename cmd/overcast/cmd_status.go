package main

// cmd_status.go — `overcast status`. Pings overcast's /_overcast/health
// endpoint and reports whether the daemon is reachable. Deliberately minimal;
// the goal is a human-friendly one-liner, not a dashboard.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
			url := strings.TrimRight(endpoint, "/") + "/_overcast/health"

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

			// Enrich the line from fields the health endpoint already carries,
			// tolerating a body this build cannot parse (older daemon, proxy).
			var health struct {
				Status  string `json:"status"`
				Version string `json:"version"`
				Storage struct {
					Default string `json:"default"`
				} `json:"storage"`
			}
			statusWord := "OK"
			var details []string
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&health); err == nil {
				if health.Status != "" && health.Status != "ok" {
					statusWord = health.Status
				}
				if health.Version != "" {
					details = append(details, "version "+health.Version)
				}
				if health.Storage.Default != "" {
					details = append(details, "storage "+health.Storage.Default)
				}
			}
			line := fmt.Sprintf("overcast %s at %s", statusWord, endpoint)
			if len(details) > 0 {
				line += " (" + strings.Join(details, ", ") + ")"
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
			return nil
		},
	}
}
