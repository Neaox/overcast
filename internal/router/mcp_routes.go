//go:build !slim

package router

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/mcp"
	"github.com/Neaox/overcast/internal/state"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// registerMCPRoutes mounts the runtime MCP surface at /_overcast/mcp for non-slim builds.
//
// Transport: Streamable HTTP (MCP 2025-11-25 §6.1).
//   - POST /_overcast/mcp        — JSON-RPC request/response; SSE response mode when client
//     sends Accept: text/event-stream.
//   - GET /_overcast/mcp         — SSE stream for server-initiated messages (requires
//     Accept: text/event-stream).
//   - DELETE /_overcast/mcp      — explicit session termination.
//   - GET /_overcast/mcp/sse     — legacy SSE compatibility endpoint for older clients.
//
// Session strategy (initial phase): stateless by default.
// The server issues MCP-Session-Id tokens and tracks them in memory for the
// lifetime of the process. Sessions are not persisted across restarts.
// This is intentional for the local-only initial phase.
//
// Auth posture: Origin validation is always enforced. When
// OVERCAST_MCP_REMOTE_EXPOSURE=true, runtime MCP additionally requires a
// configured bearer token (OVERCAST_MCP_AUTH_TOKEN) on all HTTP endpoints.
//
// The slim build tag excludes this file entirely so overcast-slim never
// exposes /_overcast/mcp.
func registerMCPRoutes(r chi.Router, cfg *config.Config, store state.Store, bus *events.Bus, _ *zap.Logger, shutdownCh <-chan struct{}) {
	provider := mcp.NewRuntimeProvider(cfg, store)
	provider.AttachEventBus(bus)
	root := sync.OnceValue(func() http.Handler {
		runtimeMCP := mcp.NewServer(nil, slog.Default(), provider)
		runtimeMCP.SetNotificationReplayLimit(cfg.MCPReplayLimit)
		// The MCP SSE stream is a long-lived handler, so it needs the same
		// pre-shutdown channel eventsHandler and domainsWatchHandler get.
		// Without it the stream ends only when the client disconnects, and a
		// shutdown that waits for in-flight handlers waits for that forever.
		runtimeMCP.SetShutdownSignal(shutdownCh)
		if cfg.MCPRemoteExposure || cfg.MCPAuthToken != "" {
			runtimeMCP.SetBearerAuthToken(cfg.MCPAuthToken)
		}
		return runtimeMCP.RootHandler()
	})
	strip := http.StripPrefix("/_overcast/mcp", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		root().ServeHTTP(w, req)
	}))
	r.Mount("/_overcast/mcp", strip)

	// Explicitly route the base path without trailing slash so clients can use
	// /_overcast/mcp and /_overcast/mcp/ interchangeably.
	rootPath := func(w http.ResponseWriter, req *http.Request) {
		rewritten := req.Clone(req.Context())
		urlCopy := *req.URL
		urlCopy.Path = "/"
		urlCopy.RawPath = "/"
		rewritten.URL = &urlCopy
		root().ServeHTTP(w, rewritten)
	}
	r.MethodFunc(http.MethodGet, "/_overcast/mcp", rootPath)
	r.MethodFunc(http.MethodPost, "/_overcast/mcp", rootPath)
	r.MethodFunc(http.MethodDelete, "/_overcast/mcp", rootPath)
}
