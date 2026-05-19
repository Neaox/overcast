package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	intmcp "github.com/Neaox/overcast/internal/mcp"
)

func main() {
	workspace := flag.String("workspace", ".", "workspace root path")
	listen := flag.String("listen", "127.0.0.1:7778", "listen address")
	stdioFlag := flag.Bool("stdio", false, "run MCP over stdio transport")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server := intmcp.NewServer(nil, logger, intmcp.NewRepoProvider(*workspace))

	if *stdioFlag {
		logger.Info("starting workspace MCP server", "transport", "stdio", "workspace", *workspace)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "overcast-mcp: %v\n", err)
			os.Exit(1)
		}
		return
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp/", server.Handler())
	mux.HandleFunc("/_health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	httpSrv := &http.Server{
		Addr:              *listen,
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

	logger.Info("starting workspace MCP server", "listen", *listen, "workspace", *workspace)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "overcast-mcp: %v\n", err)
		os.Exit(1)
	}
}
