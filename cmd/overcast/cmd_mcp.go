//go:build !slim

package main

// cmd_mcp.go — `overcast mcp`. Runs the workspace MCP server: local,
// repo-aware MCP tools and resources for agents and editors, backed by
// internal/mcp/providers.RepoProvider and the workspace on disk rather than
// a running overcast instance.
//
// This used to be the separate cmd/overcast-mcp binary. It is folded into
// the unified CLI here so overcast ships one binary instead of two; flag
// names and defaults are unchanged from that binary, and this file is a
// thin cobra shim over the same internal/mcp server code the standalone
// main() called — no server logic lives here.
//
// This is a different server from the one a running `overcast serve`
// exposes at /_overcast/mcp (internal/router/mcp_routes.go, non-slim builds
// only): that one answers questions about a live emulator instance; this
// one answers questions about the repository. See docs/plans/mcp.md for the
// full boundary between the two.
//
// Excluded from slim builds — see cmd_mcp_slim.go for the stub and why.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	intmcp "github.com/overcast-sh/overcast/internal/mcp"
	"github.com/overcast-sh/overcast/internal/mcp/providers"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the workspace MCP server (repo-aware tools for agents and editors)",
		Long: `Run the workspace MCP server: local, repo-aware MCP tools and resources for
agents and editors, backed by the workspace on disk rather than a running
overcast instance.

This is not the runtime MCP server a running "overcast serve" exposes at
/_overcast/mcp (non-slim builds only) — that one answers questions about a
live emulator instance. This one answers repository questions: service
files, doc and test coverage, conventions, symbols, change impact, and
discovery of nearby running overcast instances. See docs/plans/mcp.md.

Transports:
  --stdio     serve over stdio (the usual editor-launched mode)
  (default)   serve over HTTP at --listen, with health at /_overcast/health

Flag names and defaults match the formerly-standalone overcast-mcp binary
this command replaces; none needed renaming for cobra.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workspace, _ := cmd.Flags().GetString("workspace")
			listen, _ := cmd.Flags().GetString("listen")
			stdio, _ := cmd.Flags().GetBool("stdio")
			return runMCP(cmd, workspace, listen, stdio)
		},
	}
	cmd.Flags().String("workspace", ".", "workspace root path")
	cmd.Flags().String("listen", "127.0.0.1:7778", "listen address for the HTTP transport")
	cmd.Flags().Bool("stdio", false, "serve MCP over stdio instead of HTTP")
	return cmd
}

func runMCP(cmd *cobra.Command, workspace, listenAddr string, stdio bool) error {
	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelDebug}))
	server := intmcp.NewServer(logger, providers.NewRepoProvider(workspace))

	if stdio {
		logger.Info("starting workspace MCP server", "transport", "stdio", "workspace", workspace)
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := server.ServeStdio(ctx, cmd.InOrStdin(), cmd.OutOrStdout()); err != nil {
			return fmt.Errorf("stdio transport: %w", err)
		}
		return nil
	}

	mux := newMCPHandler(server)

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout intentionally omitted: SSE connections stream indefinitely.
		IdleTimeout: 120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	logger.Info("starting workspace MCP server", "listen", ln.Addr().String(), "workspace", workspace)
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http transport: %w", err)
	}
	return nil
}

// newMCPHandler builds the HTTP surface for the workspace MCP server: the
// MCP endpoint itself plus a health check for local tooling that wants to
// confirm the server is up before dialing it. Split out from runMCP so it
// can be exercised directly (httptest.NewServer) without binding a real
// port.
func newMCPHandler(server *intmcp.Server) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp/", server.Handler())
	mux.HandleFunc("/_overcast/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}
