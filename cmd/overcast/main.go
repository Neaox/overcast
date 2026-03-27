// Command overcast starts the AWS service emulator HTTP server.
//
// Usage:
//
//	overcast                                    # start with defaults (port 4566, memory state)
//	OVERCAST_STATE=sqlite overcast               # start with persistent SQLite state
//	OVERCAST_SERVICES=s3,sqs overcast           # start with only S3 and SQS enabled
//	OVERCAST_HOST=127.0.0.1 overcast            # bind to localhost only
//	OVERCAST_TLS_CERT=cert.pem OVERCAST_TLS_KEY=key.pem overcast  # HTTPS
//
// All configuration is via environment variables. See internal/config/config.go.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mattn/go-isatty"
	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/router"
	"github.com/your-org/overcast/internal/state"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "overcast: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ---- Config ------------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// ---- Logger ------------------------------------------------------------
	// Use the human-friendly development encoder when stdout is a terminal so
	// that coloured [SERVICE] tags and readable timestamps are shown. In
	// non-interactive environments (CI, Docker, pipe) use the JSON production
	// encoder so structured fields are machine-parseable.
	isTTY := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	var logger *zap.Logger
	if isTTY || cfg.LogLevel == "debug" {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Sync()

	if cfg.SigV4Validate {
		logger.Warn("OVERCAST_SIGV4_VALIDATE is set but SigV4 validation is not yet implemented — all requests are accepted")
	}
	if cfg.Debug {
		logger.Warn("debug endpoints enabled (/_debug/*) — do not expose this port publicly")
	}

	// ---- State backend -----------------------------------------------------
	var store state.Store
	switch cfg.State {
	case config.StateBackendSQLite:
		store, err = state.NewSQLiteStore(cfg.DataDir)
		if err != nil {
			return fmt.Errorf("init sqlite store: %w", err)
		}
		logger.Info("state backend: sqlite", zap.String("path", cfg.DataDir))
	default:
		store = state.NewMemoryStore()
		logger.Info("state backend: memory (data will not persist across restarts)")
	}
	defer store.Close()

	// ---- HTTP server -------------------------------------------------------
	handler := router.New(cfg, store, logger, clock.New())

	srv := &http.Server{
		Addr:    cfg.Addr(),
		Handler: handler,
	}

	// Signals for graceful shutdown (Ctrl+C or SIGTERM from Docker/Kubernetes).
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		proto := "http"
		if cfg.TLSEnabled() {
			proto = "https"
		}
		logger.Info("overcast listening",
			zap.String("addr", cfg.Addr()),
			zap.String("protocol", proto),
			zap.String("state", string(cfg.State)),
			zap.Bool("debug", cfg.Debug),
		)

		if cfg.TLSEnabled() {
			err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case sig := <-quit:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	logger.Info("server stopped cleanly")
	return nil
}
