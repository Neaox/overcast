package apigateway

// handler.go contains the Handler struct, ID generation, and shared helpers.
// Implemented handlers are split by concern:
//   handler_rest.go      — REST API v1 management (CreateRestApi, Resources, Methods, Integrations)
//   handler_http.go      — HTTP API v2 management (CreateApi, Routes, Integrations)
//   handler_stages.go    — Stages and Deployments (both v1 and v2)
//   handler_execution.go — Request execution engine (Lambda proxy, MOCK)

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/domainregistry"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds API Gateway handler dependencies.
type Handler struct {
	cfg              *config.Config
	store            *apigatewayStore
	log              *serviceutil.ServiceLogger
	clk              clock.Clock
	bus              *events.Bus
	invoker          events.FunctionSyncInvoker   // nil until InitLambdaInvoker is called
	cognitoValidator events.CognitoTokenValidator // nil until InitCognitoValidator is called
	domainRegistry   *domainregistry.Registry     // nil until InitDomainRegistry is called
	hydrateOnce      sync.Once                    // guards lazy domain-registry hydration
}

// ensureRegistryHydrated lazily loads persisted domain names into the
// domain registry on first access. No-op if the registry is nil.
func (h *Handler) ensureRegistryHydrated() {
	if h.domainRegistry == nil {
		return
	}
	h.hydrateOnce.Do(func() {
		ctx := context.Background()
		if items, aerr := h.store.listAllDomainNames(ctx); aerr == nil {
			for _, dn := range items {
				h.domainRegistry.Put(domainregistry.Record{Name: dn.DomainName, Source: "apigateway.v1"})
			}
		}
		if items, aerr := h.store.listAllV2DomainNames(ctx); aerr == nil {
			for _, dn := range items {
				h.domainRegistry.Put(domainregistry.Record{Name: dn.DomainName, Source: "apigateway.v2"})
			}
		}
	})
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	return &Handler{
		cfg:   cfg,
		store: newAPIGatewayStore(store, cfg.Region),
		log:   log,
		clk:   clk,
	}
}

// ---- ID generation --------------------------------------------------------

// generateAPIID returns a 10-character lowercase alphanumeric ID matching AWS
// REST API identifier format (e.g. "a1b2c3d4e5").
func generateAPIID() string {
	return generateRandomID(5) // 5 bytes = 10 hex chars
}

// generateShortID returns a 6-character lowercase alphanumeric ID matching AWS
// resource identifier format (e.g. "abc123").
func generateShortID() string {
	return generateRandomID(3) // 3 bytes = 6 hex chars
}

// generateRandomID returns a lowercase hex string of length n*2.
func generateRandomID(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; if it does, panic is appropriate
		// since we cannot generate unique identifiers.
		panic("apigateway: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ---- Lambda URI helpers ---------------------------------------------------

// lambdaFunctionNameFromURI extracts the function name from a Lambda
// integration URI. AWS Lambda integration URIs look like:
//
//	arn:aws:apigateway:{region}:lambda:path/2015-03-31/functions/{arn}/invocations
//
// or simply a function ARN. This helper handles both formats.
func lambdaFunctionNameFromURI(uri string) string {
	// Try the full API Gateway lambda integration URI format.
	if idx := strings.Index(uri, "/functions/"); idx >= 0 {
		remainder := uri[idx+len("/functions/"):]
		// Strip /invocations suffix if present.
		remainder = strings.TrimSuffix(remainder, "/invocations")
		// The remainder is a Lambda ARN — extract function name.
		return functionNameFromARN(remainder)
	}
	// Treat as plain function name or ARN.
	return functionNameFromARN(uri)
}

// functionNameFromARN extracts the function name from a Lambda ARN.
// Input: "arn:aws:lambda:us-east-1:000000000000:function:my-function".
// Output: "my-function".
func functionNameFromARN(arn string) string {
	// Full ARN: split on ":function:" and take the second half.
	if idx := strings.Index(arn, ":function:"); idx >= 0 {
		name := arn[idx+len(":function:"):]
		// Strip version/alias qualifier if present (e.g. ":$LATEST", ":1").
		if colonIdx := strings.IndexByte(name, ':'); colonIdx >= 0 {
			name = name[:colonIdx]
		}
		return name
	}
	// Not an ARN — treat the entire input as a function name.
	return arn
}
