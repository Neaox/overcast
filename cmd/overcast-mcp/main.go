// Command overcast-mcp runs the workspace MCP server: local, repo-aware MCP
// tools and resources for agents and editors, backed by the workspace on
// disk rather than a running overcast instance.
//
// This is a different server from the one a running `overcast serve`
// exposes at /_overcast/mcp (internal/router/mcp_routes.go, non-slim builds
// only): that one answers questions about a live emulator instance; this
// one answers questions about the repository — service files, doc and test
// coverage, conventions, symbols, change impact, and discovery of nearby
// running overcast instances. See docs/plans/mcp.md for the full boundary
// between the two.
//
// This tool is dev-only and never ships in a release: it is not built by any
// Makefile/Dockerfile/workflow target that produces a distributed artifact
// (every one of those builds only ./cmd/overcast). It used to be a subcommand
// of the overcast binary itself (`overcast mcp`), which put dev-only code —
// including its LSP/symbol-finder tooling — in every release build. Moving it
// back to its own command keeps that dependency graph out of what ships.
//
// Usage:
//
//	go run ./cmd/overcast-mcp --stdio
//	go run ./cmd/overcast-mcp --listen 127.0.0.1:7778
//
// Flags:
//
//	--workspace   workspace root path (default ".")
//	--listen      listen address for the HTTP transport (default "127.0.0.1:7778")
//	--stdio       serve MCP over stdio instead of HTTP (the usual editor-launched mode)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	intmcp "github.com/overcast-sh/overcast/internal/mcp"
	"github.com/overcast-sh/overcast/internal/mcp/providers"
)

func main() {
	workspace := flag.String("workspace", ".", "workspace root path")
	listen := flag.String("listen", "127.0.0.1:7778", "listen address for the HTTP transport")
	stdioFlag := flag.Bool("stdio", false, "serve MCP over stdio instead of HTTP")
	flag.Parse()

	if err := run(*workspace, *listen, *stdioFlag); err != nil {
		fmt.Fprintf(os.Stderr, "overcast-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(workspace, listenAddr string, stdio bool) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server := intmcp.NewServer(logger, providers.NewRepoProvider(workspace))

	if stdio {
		logger.Info("starting workspace MCP server", "transport", "stdio", "workspace", workspace)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
			return fmt.Errorf("stdio transport: %w", err)
		}
		return nil
	}

	mux := newHandler(server)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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

// newHandler builds the HTTP surface for the workspace MCP server: the MCP
// endpoint itself plus a health check for local tooling that wants to
// confirm the server is up before dialing it. Split out from run so it can
// be exercised directly (httptest.NewServer) without binding a real port.
func newHandler(server *intmcp.Server) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp/", server.Handler())
	mux.HandleFunc("/_overcast/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}
