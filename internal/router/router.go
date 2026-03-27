package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/events"
	"github.com/your-org/overcast/internal/middleware"
	"github.com/your-org/overcast/internal/services/dynamodb"
	"github.com/your-org/overcast/internal/services/lambda"
	"github.com/your-org/overcast/internal/services/s3"
	"github.com/your-org/overcast/internal/services/sns"
	"github.com/your-org/overcast/internal/services/sqs"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

// New builds and returns the root HTTP handler for the emulator.
// It wires together the middleware chain, health/debug endpoints, and all
// enabled service handlers.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) http.Handler {
	r := chi.NewRouter()

	// ---- Middleware chain --------------------------------------------------
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recovery(logger))
	r.Use(middleware.Logger(logger))
	r.Use(middleware.SigV4(cfg.SigV4Validate, logger))

	// ---- Internal endpoints (always available) ----------------------------
	// healthHandler is registered after the service loop so it can include
	// the list of enabled service names in the response.
	var enabledServiceNames []string
	// Suppress noisy 404s when browsers auto-request /favicon.ico.
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// ---- Debug endpoints (gated by OVERCAST_DEBUG) --------------------------
	// Equivalent to LocalStack's /_localstack/* namespace.
	// Always 404 when debug is disabled — safe to leave cfg.Debug=false in CI.
	if cfg.Debug {
		r.Route("/_debug", debugHandlers(cfg, store))
	}

	// ---- Event bus --------------------------------------------------------
	// NOTE: /_events is mounted after the bus is constructed below.
	// Shared across services. Handlers publish; dispatchers subscribe.
	// Constructed here so the router owns the lifecycle.
	bus := events.NewBus()

	// ---- SSE event stream (always available) --------------------------------
	// GET /_events?source=s3 — streams all internal events to connected clients.
	r.Get("/_events", eventsHandler(bus, logger))

	// ---- Service registry -------------------------------------------------
	// To add a new service: implement router.Service and append it here.
	s3Svc := s3.New(cfg, store, logger, clk, bus)
	sqsSvc := sqs.New(cfg, store, logger, clk)

	allServices := []Service{
		s3Svc,
		sqsSvc,
		dynamodb.New(cfg, store, logger, clk),
		sns.New(cfg, store, logger, clk),
		lambda.New(cfg, store, logger, clk),
	}

	// Collect target dispatchers for services that share POST /.
	var dispatchers []TargetDispatcher

	for _, svc := range allServices {
		svcLog := serviceutil.NewServiceLogger(logger, svc.Name())
		if !cfg.Services[svc.Name()] {
			svcLog.Info("service disabled")
			continue
		}
		svc.RegisterRoutes(r)
		if td, ok := svc.(TargetDispatcher); ok {
			dispatchers = append(dispatchers, td)
		}
		enabledServiceNames = append(enabledServiceNames, svc.Name())
		svcLog.Info("service enabled")
	}

	// ---- Event notification wiring ----------------------------------------
	// S3 notifications → SQS: connect after both services are constructed.
	if cfg.Services["s3"] && cfg.Services["sqs"] {
		s3Svc.InitNotifications(sqsSvc.Enqueuer(), bus, logger)
	}

	r.Get("/_health", newHealthHandler(enabledServiceNames))

	// Register a single POST / handler that dispatches by X-Amz-Target prefix.
	// SQS, DynamoDB, and SNS all share this endpoint.
	if len(dispatchers) > 0 {
		r.Post("/", targetDispatch(dispatchers))
	}

	r.NotFound(notFoundHandler)

	return r
}

// targetDispatch returns a handler that inspects the X-Amz-Target header and
// delegates to the correct service dispatcher. This solves the conflict where
// SQS, DynamoDB, and SNS all need POST / but chi only allows one handler per
// method+pattern.
func targetDispatch(dispatchers []TargetDispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		for _, td := range dispatchers {
			if strings.HasPrefix(target, td.TargetPrefix()) {
				td.Dispatch(w, r)
				return
			}
		}
		// No matching prefix — return an error.
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"__type":"UnknownOperationException","message":"Unknown target: ` + target + `"}`))
	}
}
