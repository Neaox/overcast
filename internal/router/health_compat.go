package router

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
)

// health_compat.go — the two health URLs Overcast answers that are not its own.
//
// Overcast's health endpoint is GET /_overcast/health. It has not always been:
// it was /_health until docs/plans/non-canonical-url-namespace.md phase 2
// (#927) collapsed the router's roots into one reserved prefix. And for anyone
// arriving from LocalStack it never was — their compose healthcheck, their
// Testcontainers wait strategy and their `localstack wait` all poll
// /_localstack/health.
//
// Neither of those callers is a client Overcast can fix by shipping a better
// SDK. They are healthchecks: infrastructure that reads a 404 as "this
// container is dead" and acts on it. Before this file both paths fell through
// to the S3 catch-all and answered NoSuchBucket/NoSuchKey — a 404 — so a
// carried-over healthcheck marked a perfectly healthy Overcast unhealthy, and
// the orchestrator restarted it. With the default in-memory state backend a
// restart is a wipe, so a deploy in flight loses the stack it was creating and
// the client polling it is told the stack no longer exists. That failure looks
// nothing like a healthcheck problem from the outside, which is why the two
// URLs are worth serving rather than documenting.
//
// The response bodies differ deliberately. /_health is Overcast's own former
// path, so it returns Overcast's own body, byte-identical to
// /_overcast/health. /_localstack/health is somebody else's contract, so it
// returns their shape — a `services` map, plus `edition` and `version` — which
// is what the LocalStack CLI and the wait strategies built around it parse.

// localStackHealthResponse is the body GET /_localstack/health returns.
//
// The three fields are LocalStack's; `emulator` is Overcast's own, added so
// that anything reading the body rather than just its status code can tell
// what actually answered. A superset is safe in a way a rename is not: clients
// read the fields they know by name and ignore the rest, but a missing
// `services` map is what turns a wait strategy into a timeout.
type localStackHealthResponse struct {
	// Services maps each enabled service to its state. LocalStack's vocabulary
	// is disabled/available/running/starting/error, where "available" means a
	// service that would start on first use and "running" one that already
	// has. Overcast wires every enabled service in-process at startup, so an
	// enabled service is always "running" and there is no lazy tier to report.
	Services map[string]string `json:"services"`
	// Edition is LocalStack's feature tier. Overcast has no paid tier to
	// distinguish, and the callers that read this field render it as a label
	// or compare it against "community", so that is what it says.
	Edition string `json:"edition"`
	Version string `json:"version"`
	// Emulator is Overcast's own marker, so a body that looks like
	// LocalStack's is never mistaken for LocalStack itself.
	Emulator string `json:"emulator"`
}

// localStackServiceStatus is the state reported for every enabled service.
// See the Services field above for why there is only one value.
const localStackServiceStatus = "running"

// localStackEndpointMap names the Overcast endpoint that replaces each
// LocalStack one, for the hint served in place of a bare 404. It mirrors the
// table in docs/migration-from-localstack.md.
//
// A path Overcast actually serves does not belong here — health, init and
// state/reset are answered rather than pointed at (see localstack_compat.go),
// so an entry for one would be dead text that could only ever go stale.
// TestLocalStackEndpointMapNamesOnlyUnservedPaths enforces that.
var localStackEndpointMap = map[string]string{
	"info":     "/_overcast/debug/config (requires OVERCAST_DEBUG=true)",
	"state":    "/_overcast/debug/state (requires OVERCAST_DEBUG=true)",
	"diagnose": "/_overcast/debug/state and /_overcast/debug/config (requires OVERCAST_DEBUG=true)",
	"config":   "/_overcast/debug/config (requires OVERCAST_DEBUG=true); configuration is read-only at runtime",
	"usage":    "/_overcast/metrics",
}

// aliasHinter logs at most one line per compatibility path, the first time
// something reaches Overcast through it.
//
// Once, not every time: these are healthchecks on a five-second interval, and
// a hint repeated every five seconds is noise an operator learns to filter
// rather than advice they act on. Once per path per process is enough for the
// line to be in the log the first time someone reads it.
type aliasHinter struct {
	logger *zap.Logger
	seen   sync.Map
}

func (h *aliasHinter) hint(path string, fields ...zap.Field) {
	if h == nil || h.logger == nil {
		return
	}
	if _, already := h.seen.LoadOrStore(path, struct{}{}); already {
		return
	}
	h.logger.Info("served a compatibility URL — update the caller to Overcast's own endpoint",
		append([]zap.Field{zap.String("path", path)}, fields...)...)
}

// newLegacyHealthHandler serves GET /_health as an alias of
// /_overcast/health, with the same body. health is the canonical handler.
func newLegacyHealthHandler(health http.HandlerFunc, hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint(middleware.LegacyHealthPath,
			zap.String("use", "/_overcast/health"),
			zap.String("note", "/_health moved to /_overcast/health in #927 and is served as an alias"))
		health(w, r)
	}
}

// newLocalStackHealthHandler serves GET /_localstack/health in LocalStack's
// response shape, so a healthcheck or wait strategy written against LocalStack
// works unchanged against Overcast.
func newLocalStackHealthHandler(cfg *config.Config, enabledServices []string, hinter *aliasHinter) http.HandlerFunc {
	// The service map is fixed for the process lifetime — services are wired
	// at startup and none of them can start, stop or fail later — so build it
	// once rather than on every five-second poll.
	services := make(map[string]string, len(enabledServices))
	for _, name := range enabledServices {
		services[name] = localStackServiceStatus
	}
	body := localStackHealthResponse{
		Services: services,
		Edition:  "community",
		Version:  cfg.Version,
		Emulator: "overcast",
	}
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint(middleware.LocalStackHealthPath,
			zap.String("use", "/_overcast/health"),
			zap.String("note", "LocalStack's health endpoint, served for compatibility; /_overcast/health carries more detail"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// newLocalStackNotFoundHandler answers everything under /_localstack/ that
// Overcast does not serve.
//
// It exists so the rest of the namespace 404s as itself rather than as S3's
// NoSuchBucket, and so the 404 says where the endpoint went. The alternative —
// leaving these to the catch-all — is how a migrated compose file's
// /_localstack/health came to report "The specified bucket does not exist",
// which tells the reader nothing about what is actually wrong.
func newLocalStackNotFoundHandler(hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint := strings.Trim(strings.TrimPrefix(r.URL.Path, middleware.LocalStackPrefix), "/")
		replacement, known := localStackEndpointMap[endpoint]
		hinter.hint(r.URL.Path, zap.String("note", "no Overcast endpoint serves this LocalStack path"))

		message := "Overcast does not serve this LocalStack endpoint. Overcast's own endpoints live under /_overcast/ — see docs/migration-from-localstack.md."
		if known {
			message = "Overcast serves this as " + replacement + ". See docs/migration-from-localstack.md."
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":   message,
			"path":      r.URL.Path,
			"health":    middleware.LocalStackHealthPath + " and /_overcast/health are both served",
			"endpoints": localStackEndpointList(),
		})
	}
}

// localStackEndpointList renders localStackEndpointMap in a stable order, so
// the 404 body does not reshuffle between calls.
func localStackEndpointList() []string {
	out := make([]string, 0, len(localStackEndpointMap))
	for from, to := range localStackEndpointMap {
		out = append(out, middleware.LocalStackPrefix+from+" -> "+to)
	}
	sort.Strings(out)
	return out
}
