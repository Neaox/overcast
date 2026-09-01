package main

// cmd_services.go — `overcast services`. Lists the AWS services a running
// daemon has enabled and the emulation tier each is running at, by reading
// GET /_overcast/health once. Deliberately a plain HTTP client rather than
// an import of internal/router: the CLI only needs a handful of fields, and
// decoding into a local struct here keeps it free to add fields to the
// health response without touching this command.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// servicesHealthResponse is the subset of GET /_overcast/health's body this
// command needs. Field names and JSON tags mirror healthResponse in
// internal/router/health.go — kept in sync by hand since the CLI must not
// import that package.
type servicesHealthResponse struct {
	Version      string            `json:"version"`
	Services     []string          `json:"services"`
	ServiceTiers map[string]string `json:"serviceTiers"`
}

// serviceEntry is one row of `overcast services` output, either rendered as
// a text table or marshalled to JSON.
type serviceEntry struct {
	Service string `json:"service"`
	Tier    string `json:"tier"`
}

// servicesJSON is the --output json structure: the enabled services (name +
// tier, sorted by name) alongside the daemon version they came from.
type servicesJSON struct {
	Version  string         `json:"version"`
	Services []serviceEntry `json:"services"`
}

func newServicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "List the AWS services overcast has enabled and their emulation tiers",
		Args:  cobra.NoArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			output, _ := cmd.Flags().GetString("output")
			if output != "text" && output != "json" {
				return fmt.Errorf("unknown --output %q: want text or json", output)
			}

			health, err := fetchServicesHealth(cmd.Context(), endpoint)
			if err != nil {
				return err
			}
			entries := servicesEntries(health)

			if output == "json" {
				return writeServicesJSON(cmd.OutOrStdout(), servicesJSON{Version: health.Version, Services: entries})
			}
			writeServicesText(cmd.OutOrStdout(), entries)
			return nil
		},
	}
	cmd.Flags().String("output", "text", "output format: text or json")
	_ = cmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// fetchServicesHealth fetches and decodes GET {endpoint}/_overcast/health,
// bounded to 2s — this command makes exactly one request.
func fetchServicesHealth(ctx context.Context, endpoint string) (*servicesHealthResponse, error) {
	url := strings.TrimRight(endpoint, "/") + "/_overcast/health"

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("overcast unreachable at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overcast returned %s", resp.Status)
	}

	var health servicesHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decode health response from %s: %w", endpoint, err)
	}
	return &health, nil
}

// servicesEntries turns the health response's services/tiers into a
// name-sorted slice, the shared shape for both output formats.
func servicesEntries(health *servicesHealthResponse) []serviceEntry {
	entries := make([]serviceEntry, 0, len(health.Services))
	for _, svc := range health.Services {
		entries = append(entries, serviceEntry{Service: svc, Tier: health.ServiceTiers[svc]})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Service < entries[j].Service })
	return entries
}

// writeServicesText renders entries as an aligned two-column table.
func writeServicesText(w io.Writer, entries []serviceEntry) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tTIER")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\n", e.Service, e.Tier)
	}
	_ = tw.Flush()
}

// writeServicesJSON emits the stable machine-readable structure, with a
// trailing newline so shell output and file redirects end cleanly.
func writeServicesJSON(w io.Writer, out servicesJSON) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
