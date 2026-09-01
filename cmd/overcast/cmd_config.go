package main

// cmd_config.go — `overcast config`. Fetches the running daemon's effective
// configuration from GET /_overcast/debug/config and prints it. Unlike
// `overcast reset` (see cmd_reset.go — moved out of the OVERCAST_DEBUG gate
// on 2026-09-01), this one legitimately stays debug-gated: echoing the
// daemon's config back is introspection, exactly the kind of thing
// OVERCAST_DEBUG exists to gate, not a cheap always-safe operation like
// reset.

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

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show the running daemon's effective configuration",
		Args:  cobra.NoArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			output, _ := cmd.Flags().GetString("output")
			if output != "pretty" && output != "json" {
				return fmt.Errorf("unknown --output %q: want pretty or json", output)
			}

			body, err := fetchDebugConfig(cmd.Context(), endpoint)
			if err != nil {
				return err
			}

			if output == "json" {
				// Raw: exactly what the daemon sent, no reformatting.
				out := body
				if len(out) == 0 || out[len(out)-1] != '\n' {
					out = append(out, '\n')
				}
				_, err := cmd.OutOrStdout().Write(out)
				return err
			}
			return writeConfigPretty(cmd.OutOrStdout(), body)
		},
	}
	cmd.Flags().String("output", "pretty", "output format: pretty or json")
	_ = cmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"pretty", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// fetchDebugConfig fetches the raw body of GET {endpoint}/_overcast/debug/config,
// bounded to 2s — this command makes exactly one request. A 404 gets a
// specific, actionable error: unlike every other /_overcast/debug/* route,
// callers of this command hit "debug is off" far more often than a genuine
// routing mistake, since config introspection is the one debug route people
// reach for without first thinking about OVERCAST_DEBUG.
func fetchDebugConfig(ctx context.Context, endpoint string) ([]byte, error) {
	url := strings.TrimRight(endpoint, "/") + "/_overcast/debug/config"

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

	if resp.StatusCode == http.StatusNotFound {
		// No "overcast:" prefix here — main.go's error path already adds one
		// (see main()'s fmt.Fprintln(os.Stderr, "overcast:", err)); this
		// string is never printed standalone.
		return nil, fmt.Errorf("config introspection is disabled at %s — start the daemon with OVERCAST_DEBUG=true to enable config introspection", endpoint)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("overcast returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read config response from %s: %w", endpoint, err)
	}
	return body, nil
}

// writeConfigPretty re-indents body (a JSON object) for human reading,
// independent of whatever whitespace the daemon sent it with.
func writeConfigPretty(w io.Writer, body []byte) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("decode config response: %w", err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
