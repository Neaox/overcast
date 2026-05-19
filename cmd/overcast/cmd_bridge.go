package main

// cmd_bridge.go — `overcast bridge`. Runs the host-side bridge: connects to
// overcast's /_internal/domains/watch SSE feed, publishes fixed mDNS records
// for overcast.local and overcast-app.local, starts a port-80 reverse proxy,
// and drives the bridge until the user hits ^C.
//
// The same logic is reused by `overcast serve --bridge` via startBridge().

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/hostbridge"
	"github.com/Neaox/overcast/internal/hostbridge/mdns"
)

func newBridgeCmd() *cobra.Command {
	var bindIPFlag string
	var httpPortFlag int
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Publish overcast domains via mDNS and start a port-80 reverse proxy",
		Long: `Connects to a running overcast instance and:

  • Advertises overcast.local (emulator API) and overcast-app.local (web UI)
    on the host mDNS responder so browsers reach them without /etc/hosts edits.
  • Watches the emulator's domain registry and advertises every registered
    API Gateway custom domain on the same responder.
  • Starts an HTTP reverse proxy on port 80 (or --http-port) that routes
    requests to the correct backend based on the Host header.

Runs in the foreground. Ctrl+C withdraws every advertisement and exits.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			apiAddr, _ := cmd.Flags().GetString("api-addr")
			uiAddr, _ := cmd.Flags().GetString("ui-addr")
			bindIP := net.ParseIP(bindIPFlag)
			if bindIP == nil {
				return fmt.Errorf("invalid --bind-ip %q", bindIPFlag)
			}

			log, err := zap.NewDevelopment()
			if err != nil {
				return err
			}
			defer func() { _ = log.Sync() }()

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			stopBridge, err := startBridge(ctx, endpoint, bindIP, apiAddr, uiAddr, httpPortFlag, log)
			if err != nil {
				return err
			}
			defer stopBridge()

			log.Info("bridge: running — Ctrl+C to stop",
				zap.String("endpoint", endpoint),
				zap.Stringer("bind", bindIP),
			)
			<-ctx.Done()
			log.Info("bridge: stopped")
			return nil
		},
	}
	cmd.Flags().StringVar(&bindIPFlag, "bind-ip", "127.0.0.1", "IP address to advertise for every registered hostname")
	cmd.Flags().IntVar(&httpPortFlag, "http-port", 80, "port for the reverse proxy (0 = disable)")
	cmd.Flags().String("api-addr", "http://localhost:4566", "emulator API address for the reverse proxy backend")
	cmd.Flags().String("ui-addr", "http://localhost:4567", "web UI address for the reverse proxy backend")
	return cmd
}

// startBridge starts the mDNS bridge and HTTP reverse proxy. It returns a
// cleanup function that withdraws all fixed mDNS records and shuts down the
// proxy, plus a non-nil error if setup fails. The bridge goroutines run until
// ctx is cancelled.
//
// Called from both newBridgeCmd and from runServe (when --bridge is set).
func startBridge(
	ctx context.Context,
	endpoint string,
	bindIP net.IP,
	apiAddr, uiAddr string,
	httpPort int,
	log *zap.Logger,
) (stop func(), err error) {
	pub, err := mdns.New(log)
	if err != nil {
		if errors.Is(err, mdns.ErrUnsupported) {
			return nil, fmt.Errorf("no mDNS backend available on this platform — install dns-sd (macOS/Windows) or avahi-utils (Linux)")
		}
		return nil, err
	}

	// Publish fixed records for the two permanent well-known domains.
	fixedRecords := []mdns.Record{
		{Hostname: "overcast.local", IP: bindIP},
		{Hostname: "overcast-app.local", IP: bindIP},
	}
	for _, r := range fixedRecords {
		if pubErr := pub.Publish(ctx, r); pubErr != nil {
			pub.Close()
			return nil, fmt.Errorf("publish %s: %w", r.Hostname, pubErr)
		}
		log.Info("bridge: published fixed record", zap.String("hostname", r.Hostname), zap.Stringer("ip", r.IP))
	}

	// Start the SSE-driven bridge for user-registered custom domains.
	src := newSSESource(endpoint, bindIP, log)
	br := hostbridge.New(pub, src, log)
	go func() {
		if runErr := br.Run(ctx); runErr != nil && !errors.Is(runErr, context.Canceled) {
			log.Warn("bridge: domain watcher exited", zap.Error(runErr))
		}
	}()

	// Start the reverse proxy if requested.
	var proxySrv *http.Server
	if httpPort != 0 {
		proxySrv = startReverseProxy(apiAddr, uiAddr, httpPort, log)
	}

	cleanup := func() {
		if proxySrv != nil {
			proxySrv.Shutdown(context.Background()) //nolint:errcheck
		}
		for _, r := range fixedRecords {
			if unpubErr := pub.Unpublish(context.Background(), r); unpubErr != nil {
				log.Warn("bridge: unpublish fixed record",
					zap.String("hostname", r.Hostname), zap.Error(unpubErr))
			}
		}
	}
	return cleanup, nil
}

// startReverseProxy binds the HTTP reverse proxy on httpPort and returns the
// server so the caller can shut it down. On permission error it logs
// platform-specific advice — a missing proxy is not fatal; mDNS still works.
func startReverseProxy(apiAddr, uiAddr string, httpPort int, log *zap.Logger) *http.Server {
	handler := hostbridge.NewProxy(apiAddr, uiAddr)
	srv := &http.Server{Handler: handler}

	proxyAddr := fmt.Sprintf(":%d", httpPort)
	ln, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		log.Warn("bridge: reverse proxy unavailable — bind failed",
			zap.String("addr", proxyAddr),
			zap.Error(err),
			zap.String("hint", privilegeHint(httpPort)),
		)
		return nil
	}

	go func() {
		log.Info("bridge: reverse proxy listening", zap.String("addr", ln.Addr().String()))
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Warn("bridge: reverse proxy error", zap.Error(serveErr))
		}
	}()
	return srv
}

// privilegeHint returns a platform-specific suggestion for binding a
// privileged port when bind fails.
func privilegeHint(port int) string {
	if port >= 1024 {
		return ""
	}
	switch runtime.GOOS {
	case "linux":
		return "run: sudo setcap cap_net_bind_service+ep $(which overcast)"
	case "darwin":
		return "run: sudo overcast bridge"
	case "windows":
		return fmt.Sprintf("run (admin shell): netsh http add urlacl url=http://+:%d/ user=%%USERNAME%%", port)
	default:
		return "try running as root or with elevated privileges"
	}
}
