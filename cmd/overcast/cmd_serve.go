package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/inithooks"
	"github.com/Neaox/overcast/internal/router"
	"github.com/Neaox/overcast/internal/state"
)

const defaultUIPort = 4567

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the AWS service emulator daemon",
		Long: `Start the emulator daemon and serve AWS API requests on the configured port.

All configuration is via environment variables. See internal/config/config.go.

Examples:
  overcast serve
  OVERCAST_STATE=memory overcast serve
  OVERCAST_SERVICES=s3,sqs overcast serve
  OVERCAST_HOST=127.0.0.1 overcast serve`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			uiPort, _ := cmd.Flags().GetInt("ui-port")
			bridgeEnabled, _ := cmd.Flags().GetBool("bridge")
			bridgeBindIP, _ := cmd.Flags().GetString("bridge-bind-ip")
			return runServe(uiPort, bridgeEnabled, bridgeBindIP)
		},
	}
	cmd.Flags().Int("ui-port", defaultUIPort, "port for the web UI (0 = disable; env: OVERCAST_UI_PORT)")
	cmd.Flags().Bool("bridge", false, "also run the mDNS bridge and port-80 reverse proxy (see: overcast bridge --help)")
	cmd.Flags().String("bridge-bind-ip", "127.0.0.1", "IP to advertise in mDNS when --bridge is set")
	return cmd
}

func runServe(uiPortFlag int, bridgeEnabled bool, bridgeBindIPStr string) error {
	profileStartup := os.Getenv("OVERCAST_PROFILE_STARTUP") == "1"
	prof := newPhaseTimer(profileStartup)
	prof.mark("Go runtime + package init")

	// ---- Config ------------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.Version = version
	prof.mark("config.Load")

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
	prof.mark("logger init")

	if cfg.SigV4Validate {
		logger.Warn("OVERCAST_SIGV4_VALIDATE is set but SigV4 validation is not yet implemented — all requests are accepted")
	}
	if cfg.Debug {
		logger.Warn("debug endpoints enabled (/_debug/*) — do not expose this port publicly")
	}

	// ---- Init hooks -----------------------------------------------------------
	var hookRunner *inithooks.Runner
	if cfg.InitEnabled {
		hookRunner = inithooks.NewRunner(cfg.InitDirs, buildHookEnv(cfg), cfg.InitTimeout, logger)
		hookRunner.Discover()
		prof.mark("hookRunner.Discover")

		// START hooks run before store/router init — useful for pre-flight setup.
		hookRunner.Run(context.Background(), inithooks.StageStart)
		prof.mark("hookRunner.Run(START)")
	}

	// ---- State backend -----------------------------------------------------
	store, err := buildStore(cfg, cfg.State, cfg.DataDir, logger)
	if err != nil {
		return fmt.Errorf("init state backend: %w", err)
	}
	// closeStore performs a bounded, logged Close() on the final value of
	// `store` (which may be replaced by a NamespacedStore below — the closure
	// reads the variable, not a snapshot, so it always sees the final value).
	//
	// It is invoked explicitly at the end of the graceful shutdown sequence,
	// after cleanup() has run, so that both signal-triggered and error-triggered
	// shutdowns close the store the same bounded way. The deferred call here is
	// only a safety net for early-return paths above that point in the function
	// (e.g. a per-service buildStore failure, or the listener failing to bind) —
	// sync.Once makes the deferred call a no-op once the explicit call has
	// already run.
	var closeStoreOnce sync.Once
	closeStore := func() {
		closeStoreOnce.Do(func() {
			closeStoreBounded(store, cfg.ShutdownTimeout, logger)
		})
	}
	defer closeStore()

	logStoreMode(logger, cfg)
	prof.mark("buildStore")

	// Per-service overrides: build a NamespacedStore when any service requests
	// a different mode from the global default.
	if len(cfg.ServiceStates) > 0 {
		newStore, err := buildServiceRoutes(cfg, store, logger)
		if err != nil {
			return err
		}
		// Replace store with the (possibly wrapping) result; NamespacedStore.Close()
		// handles closing all underlying stores, so we swap the deferred Close target.
		store = newStore
		prof.mark("buildStore: per-svc overrides")
	}

	// ---- HTTP server -------------------------------------------------------
	handler, preShutdown, cleanup, _ := router.New(cfg, store, logger, clock.New(), hookRunner)
	prof.mark("router.New (full)")

	// When TLS is enabled the standard library automatically negotiates HTTP/2
	// via ALPN. For plain-text connections we wrap the handler with h2c so that
	// clients can use HTTP/2 via the Upgrade mechanism or HTTP/2 prior-knowledge.
	var srvHandler http.Handler
	if cfg.TLSEnabled() {
		srvHandler = handler
	} else {
		srvHandler = h2c.NewHandler(handler, &http2.Server{})
	}

	srv := &http.Server{
		Handler: srvHandler,
		// IdleTimeout governs how long a keep-alive connection may sit idle
		// between requests. Without this, every AWS SDK connection (which uses
		// HTTP keep-alive by default) holds a goroutine open indefinitely —
		// after a compat/integration test run this can accumulate hundreds of
		// goroutines. 60 s matches the AWS service default and is safe for all
		// SDK clients, which retry on connection reset.
		IdleTimeout: 60 * time.Second,
	}

	// Bind the listener explicitly so we know the port is ready before running
	// READY hooks and before entering the select loop.
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr(), err)
	}

	// ---- Web UI server -----------------------------------------------------
	// Resolve the UI port: env var overrides flag; 0 disables the UI server.
	// When the default port (4567) is taken we fall back to an ephemeral port
	// so the emulator still starts. An explicit non-zero port fails hard.
	uiPort := uiPortFlag
	if ep := os.Getenv("OVERCAST_UI_PORT"); ep != "" {
		fmt.Sscanf(ep, "%d", &uiPort)
	}
	var uiLn net.Listener
	if uiPort != 0 {
		uiHandler, err := newUIHandler(cfg.Port, cfg.Region, cfg.Debug)
		if err != nil {
			logger.Warn("web UI unavailable", zap.Error(err))
		} else {
			uiAddr := fmt.Sprintf(":%d", uiPort)
			uiLn, err = net.Listen("tcp", uiAddr)
			if err != nil {
				if uiPort == defaultUIPort {
					logger.Warn("web UI default port busy; selecting an ephemeral UI port",
						zap.Int("requested_port", defaultUIPort),
						zap.Error(err),
					)
					// Default port busy — pick any free port.
					uiLn, err = net.Listen("tcp", ":0")
					if err != nil {
						logger.Warn("web UI listener failed", zap.Error(err))
					} else {
						logger.Info("web UI fallback port selected",
							zap.String("addr", uiLn.Addr().String()),
						)
					}
				} else {
					return fmt.Errorf("listen (ui) %s: %w", uiAddr, err)
				}
			}
			if uiLn != nil {
				uiSrv := &http.Server{Handler: uiHandler, IdleTimeout: 60 * time.Second}
				go func() {
					logger.Info("web UI listening", zap.String("addr", uiLn.Addr().String()))
					if err := uiSrv.Serve(uiLn); err != nil && err != http.ErrServerClosed {
						logger.Warn("web UI server error", zap.Error(err))
					}
				}()
				defer uiSrv.Shutdown(context.Background()) //nolint:errcheck
			}
		}
	}

	// ---- Bridge (optional) ------------------------------------------------
	if bridgeEnabled {
		bindIP := net.ParseIP(bridgeBindIPStr)
		if bindIP == nil {
			return fmt.Errorf("invalid --bridge-bind-ip %q", bridgeBindIPStr)
		}
		apiAddr := fmt.Sprintf("http://%s", ln.Addr())
		uiAddr := fmt.Sprintf("http://localhost:%d", defaultUIPort)
		if uiLn != nil {
			uiAddr = fmt.Sprintf("http://%s", uiLn.Addr())
		}
		bridgeCtx, cancelBridge := context.WithCancel(context.Background())
		stopBridge, err := startBridge(bridgeCtx, apiAddr, bindIP, apiAddr, uiAddr, 80, logger)
		if err != nil {
			cancelBridge()
			logger.Warn("bridge unavailable", zap.Error(err))
		} else {
			defer func() {
				stopBridge()
				cancelBridge()
			}()
		}
	}

	// Signals for graceful shutdown (Ctrl+C or SIGTERM from Docker/Kubernetes).
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		proto := "h2c"
		if cfg.TLSEnabled() {
			proto = "https" // HTTP/2 over TLS via ALPN
		}
		logger.Info("overcast listening",
			zap.String("addr", ln.Addr().String()),
			zap.String("protocol", proto),
			zap.String("state", string(cfg.State)),
			zap.Bool("debug", cfg.Debug),
		)

		if cfg.TLSEnabled() {
			err = srv.ServeTLS(ln, cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// READY hooks run asynchronously after the port is bound so the server
	// can accept requests while init scripts execute (matches LocalStack).
	if hookRunner != nil {
		go hookRunner.Run(context.Background(), inithooks.StageReady)
	}

	// Both branches below converge on the same shutdown tail: preShutdown,
	// SHUTDOWN hooks, HTTP drain, cleanup, and the bounded store close. A
	// server-serve error must still surface as a non-nil return, but it must
	// not skip cleanup — cleanup() flushes service-level buffered state (e.g.
	// CloudWatch Logs' debounced write cache via Stop) that would otherwise be
	// lost if the daemon failed shortly after a partial start.
	var serveErr error
	select {
	case err := <-serverErr:
		serveErr = err
		logger.Error("server error; running shutdown sequence before exiting", zap.Error(err))
	case sig := <-quit:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))
	}

	shutdownStart := time.Now()

	// Unblock long-lived handlers (SSE) before asking the server to drain.
	preShutdown()
	logger.Info("pre-shutdown complete", zap.Duration("elapsed", time.Since(shutdownStart)))

	// SHUTDOWN hooks run after SSE is unblocked but before HTTP drain.
	if hookRunner != nil {
		shutdownHookCtx, shutdownHookCancel := context.WithTimeout(context.Background(), cfg.InitTimeout*10)
		hookRunner.Run(shutdownHookCtx, inithooks.StageShutdown)
		shutdownHookCancel()
	}

	// Give in-flight HTTP requests a short grace period to finish. After
	// preShutdown() all long-lived handlers (SSE) have already returned, so
	// only short-lived requests remain. 2 s is generous for a local dev tool.
	const drainGrace = 2 * time.Second
	drainCtx, drainCancel := context.WithTimeout(context.Background(), drainGrace)
	defer drainCancel()

	if err := srv.Shutdown(drainCtx); err != nil {
		logger.Warn("http drain grace exceeded, force-closing connections", zap.Error(err))
		srv.Close()
	}
	logger.Info("http server drained", zap.Duration("elapsed", time.Since(shutdownStart)))

	// Always run cleanup — even when srv.Shutdown times out — so background
	// resources (SMTP listener, Lambda Runtime API) are released promptly.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cleanupCancel()
	cleanup(cleanupCtx)
	logger.Info("cleanup complete", zap.Duration("elapsed", time.Since(shutdownStart)))

	// Explicit, bounded close on the normal shutdown path — see closeStore's
	// definition above, near where `store` is created. The deferred call
	// registered there is a sync.Once no-op by the time it runs, since it
	// already ran here.
	closeStore()
	logger.Info("store close sequence complete", zap.Duration("elapsed", time.Since(shutdownStart)))

	if serveErr != nil {
		return fmt.Errorf("server error: %w", serveErr)
	}
	logger.Info("server stopped cleanly")
	return nil
}

// closeStoreBounded closes store with a time budget so shutdown can never
// hang indefinitely on it. HybridStore.Close (and similarly-shaped persistent
// backends) perform a full synchronous flush of all dirty entries to SQLite
// before returning; on a slow disk — notably a bind-mounted /data volume
// under Docker Desktop — that flush can run long enough to exceed `docker
// stop`'s default ~10s grace period, which SIGKILLs the process mid-flush.
// That's survivable: SQLite writes are transactional and the hybrid store's
// pending log replays anything that didn't make it into SQLite on the next
// start. But an unbounded wait here would defeat the point of a "graceful"
// shutdown, so the wait is capped: on timeout we log loudly and let the
// process exit anyway rather than block indefinitely (or get SIGKILLed with
// no record of why).
func closeStoreBounded(store state.Store, timeout time.Duration, logger *zap.Logger) {
	pendingBefore := -1
	if health, ok := state.PersistentHealthSnapshot(store); ok {
		pendingBefore = health.PendingWrites
	}

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- store.Close() }()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			logger.Warn("store close returned error", zap.Error(err), zap.Duration("elapsed", elapsed))
			return
		}
		fields := []zap.Field{zap.Duration("elapsed", elapsed)}
		if pendingBefore >= 0 {
			fields = append(fields, zap.Int("pending_writes_at_close", pendingBefore))
		}
		logger.Info("store closed cleanly", fields...)
	case <-time.After(timeout):
		logger.Warn("store close exceeded shutdown timeout; process exiting with the close still in flight — pending writes will replay from the pending log on next start",
			zap.Duration("timeout", timeout),
			zap.Int("pending_writes_at_timeout", pendingBefore),
		)
	}
}

// buildServiceRoutes builds the per-service storage overrides described by
// cfg.ServiceStates and wraps defaultStore in a state.NamespacedStore when at
// least one service ends up needing a storage mode different from the global
// default (cfg.State). Services whose override mode matches cfg.State are
// skipped — there's no need for a separate store when it would behave
// identically to the default.
//
// When there are no overrides left after that skip (including the
// len(cfg.ServiceStates) == 0 case), defaultStore is returned unchanged so
// callers never need to special-case "no overrides configured".
//
// Namespaces route on storage-namespace prefix (state.NamespacedStore.storeFor
// matches the segment before ":", e.g. "cfn" in "cfn:stacks"), which is NOT
// always the same as the config service name used in cfg.ServiceStates — see
// config.ServiceNamespacePrefix for the handful of services (cloudformation,
// apigateway, eventbridge) where they differ. The route map returned here is
// therefore keyed by the resolved prefix, not by the raw config service name;
// keying by the config name would silently make the override for those three
// services a no-op, since NamespacedStore would never match their real
// namespace prefix. The on-disk sub-directory for each override's data
// (svcDir) is deliberately kept keyed by the config service name instead,
// since that's the human-readable name operators expect to find on disk.
//
// On a per-service buildStore failure, every store already built earlier in
// this call is closed before the error is returned, so a partial failure
// never leaks stores that were successfully constructed for other services.
//
// A handful of services accept an OVERCAST_STATE_<SERVICE> override that can
// never take effect regardless of prefix routing — see
// config.ServiceOverrideIneffective for the full list and why (e.g.
// dynamodbstreams is a store-less facade over dynamodb; sts writes under the
// "iam:sessions" namespace). The override store is still built and routed
// for these services (harmless, and keeps behavior uniform), but a Warn is
// logged so the no-op isn't silently mistaken for a working override.
func buildServiceRoutes(cfg *config.Config, defaultStore state.Store, logger *zap.Logger) (state.Store, error) {
	if len(cfg.ServiceStates) == 0 {
		return defaultStore, nil
	}

	routes := make(map[string]state.Store, len(cfg.ServiceStates))
	var perSvcStores []state.Store // tracked for cleanup on error

	for svc, mode := range cfg.ServiceStates {
		if mode == cfg.State {
			continue // same mode as global default — no need to create a separate store
		}
		// Each service gets its own sub-directory, keyed by the human-readable
		// config service name, so db files don't collide and stay easy to
		// find on disk.
		svcDir := filepath.Join(cfg.DataDir, svc)
		svcStore, err := buildStore(cfg, mode, svcDir, logger.With(zap.String("service_state", svc)))
		if err != nil {
			for _, s := range perSvcStores {
				s.Close()
			}
			return nil, fmt.Errorf("init state backend for service %s: %w", svc, err)
		}

		prefix := config.ServiceNamespacePrefix(svc)
		routes[prefix] = svcStore
		perSvcStores = append(perSvcStores, svcStore)

		logFields := []zap.Field{
			zap.String("service", svc),
			zap.String("mode", string(mode)),
		}
		if prefix != svc {
			logFields = append(logFields, zap.String("namespace_prefix", prefix))
		}
		logger.Info("service state override", logFields...)

		if reason, ineffective := config.ServiceOverrideIneffective(svc); ineffective {
			logger.Warn("service state override has no effect",
				zap.String("service", svc),
				zap.String("reason", reason),
			)
		}
	}

	if len(routes) == 0 {
		return defaultStore, nil
	}
	// NamespacedStore.Close() handles closing all underlying stores
	// (including defaultStore), so the caller only needs to track this
	// single returned value going forward.
	return state.NewNamespacedStore(defaultStore, routes), nil
}

// buildStore creates a Store for the given mode and dataDir.
// The caller is responsible for calling Close() on the returned store.
func buildStore(cfg *config.Config, mode config.StateBackend, dataDir string, logger *zap.Logger) (state.Store, error) {
	switch mode {
	case config.StateBackendPersistent:
		s, err := state.NewSQLiteStoreWithLogger(dataDir, logger)
		if err != nil {
			return nil, fmt.Errorf("persistent store: %w", err)
		}
		return s, nil
	case config.StateBackendWAL:
		s, err := state.NewWALStore(dataDir, state.WALOptions{
			SyncMode:     state.WALSyncMode(cfg.WALFsyncMode),
			SyncInterval: cfg.WALFsyncInterval,
			MaxLogBytes:  cfg.WALMaxLogBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("wal store: %w", err)
		}
		return s, nil
	case config.StateBackendHybrid:
		s, err := state.NewHybridStoreWithOptions(dataDir, state.HybridOptions{
			FlushInterval:       cfg.HybridFlushInterval,
			SyncMode:            state.WALSyncMode(cfg.HybridSyncMode),
			SyncInterval:        cfg.HybridSyncInterval,
			DirtyEntryThreshold: cfg.HybridDirtyEntryThreshold,
			DirtyByteThreshold:  cfg.HybridDirtyByteThreshold,
			MaintenanceInterval: cfg.HybridMaintenanceInterval,
		}, logger)
		if err != nil {
			return nil, fmt.Errorf("hybrid store: %w", err)
		}
		return s, nil
	default: // memory
		return state.NewMemoryStore(), nil
	}
}

// logStoreMode logs a structured INFO event describing the active storage backend.
func logStoreMode(logger *zap.Logger, cfg *config.Config) {
	switch cfg.State {
	case config.StateBackendPersistent:
		logger.Info("state backend: persistent (SQLite, synchronous writes)",
			zap.String("path", cfg.DataDir))
	case config.StateBackendWAL:
		logger.Info("state backend: wal (memory reads, append-log durability)",
			zap.String("path", cfg.DataDir),
			zap.String("wal_fsync", cfg.WALFsyncMode),
			zap.Duration("wal_fsync_interval", cfg.WALFsyncInterval),
			zap.Int64("wal_max_log_bytes", cfg.WALMaxLogBytes))
	case config.StateBackendHybrid:
		logger.Info("state backend: hybrid (memory reads, async SQLite flush)",
			zap.String("path", cfg.DataDir),
			zap.Duration("flush_interval", cfg.HybridFlushInterval),
			zap.String("hybrid_sync", cfg.HybridSyncMode),
			zap.Duration("hybrid_sync_interval", cfg.HybridSyncInterval),
			zap.Int("hybrid_dirty_entry_threshold", cfg.HybridDirtyEntryThreshold),
			zap.Int64("hybrid_dirty_byte_threshold", cfg.HybridDirtyByteThreshold),
			zap.Duration("hybrid_maintenance_interval", cfg.HybridMaintenanceInterval))
	default:
		logger.Info("state backend: memory (data will not persist across restarts)")
	}
}

// buildHookEnv returns the environment variables passed to init hook scripts.
func buildHookEnv(cfg *config.Config) []string {
	return []string{
		fmt.Sprintf("AWS_ENDPOINT_URL=http://localhost:%d", cfg.Port),
		"AWS_DEFAULT_REGION=" + cfg.Region,
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"LOCALSTACK_HOSTNAME=localhost",
		fmt.Sprintf("EDGE_PORT=%d", cfg.Port),
	}
}
