//go:build slim

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newMCPCmd is the slim-build stub for `overcast mcp`.
//
// The workspace MCP server (internal/mcp + internal/mcp/providers) is a
// local development tool — repo browsing, symbol lookup, coverage and
// convention lookups — with no role in a slim/CI daemon. slim already
// excludes that same package tree from the daemon's runtime MCP for the
// identical reason (internal/router/mcp_routes_slim.go, gated so
// overcast-slim never exposes /_overcast/mcp); an unconditional import of
// internal/mcp here in cmd_mcp.go would undo that exclusion and pull the
// whole workspace-server dependency graph — including its LSP/symbol-finder
// tooling — back into the binary the slim flavour exists to keep small.
//
// This stub mirrors embed_web_slim.go's newUIHandler: the function is
// present with the same signature and the command is still registered (so
// `overcast --help` and its flags look the same across builds), but running
// it explains why the feature isn't here rather than silently doing
// nothing or failing with "unknown flag".
func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "mcp",
		Short:        "Run the workspace MCP server (not included in slim builds)",
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("workspace MCP server not included in slim build")
		},
	}
	cmd.Flags().String("workspace", ".", "workspace root path (unused: not included in slim build)")
	cmd.Flags().String("listen", "127.0.0.1:7778", "listen address (unused: not included in slim build)")
	cmd.Flags().Bool("stdio", false, "unused: not included in slim build")
	return cmd
}
