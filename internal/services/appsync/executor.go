package appsync

// executor.go — interfaces and types for the GraphQL execution engine.
//
// These interfaces define the contracts for the execution pipeline:
//   - Schema parsing and validation (SDL → executable schema) — DONE
//   - GraphQL query/mutation execution against stored resolvers — DONE (NONE + HTTP + Lambda + DynamoDB)
//   - VTL mapping template evaluation (Apache Velocity) — DONE
//   - APPSYNC_JS code evaluation (JavaScript resolver runtime) — DONE
//   - Request authentication (API key, Cognito, OIDC, Lambda, IAM) — DONE
//   - Real-time subscriptions via WebSocket — DONE
//
// All six phases of the execution engine are implemented. The interfaces
// remain as the contracts each component fulfils.
//
// ─── Implementation roadmap ──────────────────────────────────────────────────
//
// Phase 1 — Schema parsing (P2): ✅ DONE
//   Uses github.com/vektah/gqlparser/v2 (BSD, zero deps, used by gqlgen).
//   Parses uploaded SDL into ast.Schema on StartSchemaCreation. Stores the
//   raw SDL in the store AND caches the parsed *ast.Schema in memory
//   (keyed by apiId). Invalid SDL is rejected on upload.
//
// Phase 2 — Query execution (P2): ✅ DONE (NONE + HTTP + Lambda + DynamoDB data sources, PIPELINE resolvers, arguments, nested resolution)
//   GraphQL requests arrive at POST /_appsync/{apiId}/graphql.
//   Handler parses the query string against the cached *ast.Schema, resolves
//   fields by looking up Resolvers in the store (type+field → Resolver),
//   then dispatches each resolver through the appropriate data source:
//     NONE            → return the mapping template payload directly
//     HTTP            → proxy to the configured endpoint
//     AMAZON_DYNAMODB → forward to the local DynamoDB emulator via DynamoDBInvoker
//     AWS_LAMBDA      → invoke via the local Lambda emulator via FunctionSyncInvoker
//   Supports operationName selection for multi-operation documents.
//   Mutations are handled via the same execution path as queries.
//
// Phase 3 — VTL runtime (P3): ✅ DONE
//   Full Go VTL interpreter (lexer → parser → evaluator) supporting:
//     $context/$ctx references, #set/#if/#elseif/#else/#foreach/#return,
//     $util (toJson, parseJson, autoId, isNull, matches, error, validate),
//     $util.time.*, $util.dynamodb.*, string/map/list methods, quiet $!refs.
//   Evaluates requestMappingTemplate/responseMappingTemplate on resolvers
//   and functions. EvaluateMappingTemplate API endpoint implemented.
//
// Phase 4 — APPSYNC_JS runtime (P3): ✅ DONE (full runtime with expanded utils)
//   Evaluates the `code` field on resolvers/functions with runtime.name =
//   "APPSYNC_JS". Uses the goja library (pure Go JS engine, no CGO).
//   Provides AppSync JS runtime utilities: util.autoId, util.toJson,
//   util.parseJson, util.time.*, util.dynamodb.* (full: toDynamoDB, toMapValues,
//   toBoolean, toNull, toList, toMap, toStringSet, toNumberSet, etc.),
//   util.str (toLower, toUpper, toReplace, normalize), util.math
//   (roundNum, minVal, maxVal, randomDouble, randomWithinRange),
//   type checking (isNull, isString, isList, isMap, isNumber, isBoolean),
//   null coalescing (defaultIfNull, defaultIfNullOrEmpty), util.matches,
//   util.validate. ctx.env injected from EnvironmentVariables store.
//   Supports both UNIT and PIPELINE resolvers with JS code.
//   EvaluateCode API endpoint implemented for interactive testing.
//
// Phase 5 — Authentication (P2): ✅ DONE (all auth types + multi-auth)
//   Validates incoming GraphQL requests based on the API's authenticationType:
//     API_KEY                    → x-api-key header against stored ApiKeys (expiry check)
//     AMAZON_COGNITO_USER_POOLS  → Bearer token from Authorization header, JWT claims parsed
//     OPENID_CONNECT             → Bearer token + JWT claims + issuer validation
//     AWS_LAMBDA                 → invoke configured Lambda authorizer (accept-all stub)
//     AWS_IAM                    → accept (SigV4 stub already passes all requests)
//   Multi-auth: primary authenticationType + additionalAuthenticationProviders
//   fallback chain. Identity ($context.identity) propagated through execution.
//
// Phase 6 — Subscriptions (P3): ✅ DONE (WebSocket real-time, mutation fan-out)
//   GET /_appsync/{apiId}/realtime upgrades to WebSocket using github.com/coder/websocket.
//   Protocol: connection_init→connection_ack, start→start_ack, stop→complete,
//   ka (30s keepalive). On subscription start, parses the subscription query
//   and registers in an in-memory subscription map. When a mutation resolves,
//   handler publishes results to all matching subscriptions (convention:
//   mutation createFoo → subscription onCreateFoo). Connection lifecycle
//   managed by subscriptionManager with goroutine-safe maps.

import (
	"context"
	"encoding/json"
	"net/http"
)

// ─── Schema parsing ──────────────────────────────────────────────────────────

// TODO(priority:P2): implement SchemaParser using github.com/vektah/gqlparser/v2.

// SchemaParser parses a GraphQL SDL string into an internal representation
// that can be used for query validation, introspection, and execution.
type SchemaParser interface {
	// Parse validates and parses raw SDL bytes into a ParsedSchema.
	// Returns an error with line/column info if the SDL is invalid.
	Parse(sdl []byte) (*ParsedSchema, error)

	// Merge combines multiple SDL sources into a single merged schema.
	// Used by the merged API feature (StartSchemaMerge).
	Merge(schemas [][]byte) (*ParsedSchema, error)
}

// ParsedSchema holds a parsed and validated GraphQL schema.
// The implementation should wrap *ast.Schema from gqlparser and expose
// only what the executor needs — keeping the parser dependency contained.
type ParsedSchema struct {
	// Raw is the original SDL source (for re-serialisation).
	Raw []byte

	// TypeNames lists all type names defined in the schema.
	TypeNames []string

	// QueryType is the name of the root query type (usually "Query").
	QueryType string
	// MutationType is the name of the root mutation type (usually "Mutation").
	MutationType string
	// SubscriptionType is the name of the root subscription type (usually "Subscription").
	SubscriptionType string

	// Opaque holds the parser-specific internal representation (e.g. *ast.Schema).
	// Typed as any to avoid importing the parser package in this interface file.
	Opaque any
}

// ─── Query execution ─────────────────────────────────────────────────────────

// TODO(priority:P2): implement QueryExecutor that resolves fields using stored resolvers + data sources.

// QueryExecutor executes a GraphQL operation (query, mutation, or subscription
// start) against a parsed schema, using the configured resolvers and data sources.
type QueryExecutor interface {
	// Execute runs a GraphQL operation and returns the result.
	// The executor is responsible for:
	//   1. Parsing and validating the query against the schema.
	//   2. Walking the selection set and resolving each field.
	//   3. Calling the appropriate MappingTemplateEvaluator or CodeEvaluator.
	//   4. Dispatching data source requests (DynamoDB, Lambda, HTTP, NONE).
	//   5. Assembling the response per the GraphQL spec.
	Execute(ctx context.Context, params ExecuteParams) (*ExecuteResult, error)
}

// ExecuteParams contains the inputs for a GraphQL execution request.
type ExecuteParams struct {
	API           *GraphqlAPI
	Schema        *ParsedSchema
	Query         string           `json:"query"`
	Variables     map[string]any   `json:"variables,omitempty"`
	OperationName string           `json:"operationName,omitempty"`
	Identity      *RequestIdentity // Populated by Authenticator.
}

// RequestIdentity holds the authenticated caller's identity, populated by
// the Authenticator and passed into the resolver context ($context.identity).
type RequestIdentity struct {
	// AccountId is the AWS account (always the emulator's configured account).
	AccountId string `json:"accountId,omitempty"`
	// Sub is the authenticated user's subject claim (from JWT).
	Sub string `json:"sub,omitempty"`
	// Issuer is the token issuer URL (Cognito or OIDC).
	Issuer string `json:"issuer,omitempty"`
	// Claims holds the full JWT claims map.
	Claims map[string]any `json:"claims,omitempty"`
}

// ExecuteResult is the GraphQL response envelope.
type ExecuteResult struct {
	Data       json.RawMessage `json:"data"`
	Errors     []GraphQLError  `json:"errors,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// GraphQLError represents a single error in the GraphQL response.
type GraphQLError struct {
	Message    string           `json:"message"`
	Locations  []SourceLocation `json:"locations,omitempty"`
	Path       []any            `json:"path,omitempty"`
	Extensions map[string]any   `json:"extensions,omitempty"`
}

// SourceLocation identifies a position in a GraphQL document.
type SourceLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// ─── VTL mapping template evaluation ─────────────────────────────────────────

// TODO(priority:P3): implement MappingTemplateEvaluator with a Go VTL interpreter.
// The VTL context must include:
//   $context.arguments   — field arguments from the GraphQL query
//   $context.source      — parent field's resolved value
//   $context.result      — data source response (response template only)
//   $context.stash       — cross-resolver stash map
//   $context.identity    — caller identity from Authenticator
//   $context.request     — HTTP headers from the original request
//   $context.info        — field info (fieldName, parentTypeName, selectionSetList)
//   $context.env         — environment variables from EnvironmentVariables store
//
// Built-in VTL utilities to implement:
//   $util.toJson(), $util.parseJson(), $util.autoId(), $util.time.nowISO8601(),
//   $util.dynamodb.toDynamoDB(), $util.dynamodb.toMapValues(), etc.
// See: https://docs.aws.amazon.com/appsync/latest/devguide/resolver-util-reference.html

// MappingTemplateEvaluator evaluates Apache Velocity Template Language (VTL)
// mapping templates used by AppSync resolvers and functions.
type MappingTemplateEvaluator interface {
	// Evaluate takes a VTL template string and a context map, and returns
	// the rendered output string. The context map is the $context variable
	// available to the template.
	Evaluate(template string, context map[string]any) (string, error)
}

// ─── APPSYNC_JS code evaluation ──────────────────────────────────────────────

// TODO(priority:P3): implement CodeEvaluator using goja (pure Go JS engine).
// The APPSYNC_JS runtime (version 1.0.0) supports:
//   - ES2020+ syntax (arrow functions, destructuring, optional chaining, etc.)
//   - Built-in @aws-appsync/utils module (util.autoId, util.time, util.dynamodb, etc.)
//   - Global runtime context object with source, args, result, stash, env, etc.
//   - Two entry points: request(ctx) and response(ctx) functions.
// See: https://docs.aws.amazon.com/appsync/latest/devguide/resolver-reference-js-version.html
//
// EvaluateCode and EvaluateMappingTemplate API endpoints use these interfaces
// to provide a standalone evaluation sandbox (no resolver/data source needed).

// CodeEvaluator evaluates APPSYNC_JS JavaScript resolver code.
type CodeEvaluator interface {
	// Evaluate runs a JS code module and calls the specified function
	// ("request" or "response") with the given context. Returns the
	// function's return value as a JSON string plus any console.log output.
	Evaluate(code string, function string, context map[string]any) (*EvaluationResult, error)
}

// EvaluationResult holds the output of EvaluateCode or EvaluateMappingTemplate.
type EvaluationResult struct {
	// EvaluationResult is the serialised return value of the evaluated code/template.
	EvaluationResult string `json:"evaluationResult,omitempty"`
	// Error is set when the evaluation fails (syntax error, runtime exception, etc.).
	Error *EvaluationError `json:"error,omitempty"`
	// Logs contains any console.log or #set($debug) output captured during evaluation.
	Logs []string `json:"logs,omitempty"`
}

// EvaluationError describes a failure during code/template evaluation.
type EvaluationError struct {
	Message string `json:"message"`
	// ErrorType is set when the code calls util.error() with an explicit error type.
	ErrorType string `json:"errorType,omitempty"`
	// Data holds additional error data from util.error().
	Data any `json:"data,omitempty"`
	// CodeErrors holds specific line-level errors for APPSYNC_JS evaluation.
	CodeErrors []CodeError `json:"codeErrors,omitempty"`
}

// CodeError pinpoints a specific error within evaluated JS code.
type CodeError struct {
	ErrorType string          `json:"errorType"`
	Value     string          `json:"value"`
	Location  *SourceLocation `json:"location,omitempty"`
}

// ─── Authentication ──────────────────────────────────────────────────────────

// TODO(priority:P2): implement Authenticator for API_KEY validation, then
// expand to support AMAZON_COGNITO_USER_POOLS, OPENID_CONNECT, AWS_LAMBDA.
//
// Implementation plan for API_KEY auth:
//   1. Extract x-api-key header from the incoming request.
//   2. List all ApiKeys for the target API from the store.
//   3. Match by key ID (the da2-xxx value IS the key).
//   4. Check expiry: if key.Expires < now, reject with UnauthorizedException.
//   5. On success, return a RequestIdentity with minimal info.
//
// For the GraphQL execution endpoint (POST /_appsync/{apiId}/graphql):
//   1. Load the API from the store.
//   2. Determine auth mode from the API's authenticationType.
//   3. Call Authenticator.Authenticate(r, api).
//   4. If multi-auth (additionalAuthenticationProviders), try each provider.
//   5. Attach the resulting RequestIdentity to the context for resolver access.

// Authenticator validates incoming GraphQL requests based on the API's
// authentication configuration.
type Authenticator interface {
	// Authenticate validates the request against the API's auth config.
	// Returns a RequestIdentity on success, or an error on failure.
	// The error should be an *protocol.AWSError with code "UnauthorizedException".
	Authenticate(r *http.Request, api *GraphqlAPI) (*RequestIdentity, error)
}

// ─── Subscription manager ────────────────────────────────────────────────────

// TODO(priority:P3): implement SubscriptionManager for real-time GraphQL subscriptions.
//
// Implementation plan:
//   1. Upgrade the REALTIME URI path to a WebSocket endpoint using gorilla/websocket
//      or github.com/coder/websocket. AppSync uses the graphql-ws sub-protocol.
//   2. On WebSocket connection_init, validate auth (same as HTTP requests).
//   3. On "subscribe" message, parse the subscription query against the schema,
//      validate the selection set, and register in an in-memory subscription map
//      keyed by (apiId, subscriptionId).
//   4. When a mutation is executed (via QueryExecutor), check if the mutation's
//      return type matches any active subscription's selection set. If so, evaluate
//      the subscription's selection against the mutation result and push to the
//      WebSocket connection.
//   5. Use events.Bus to decouple mutation execution from subscription delivery —
//      publish an "appsync:MutationExecuted" event with the mutation result, and
//      have the subscription manager subscribe to it.
//   6. Handle connection_terminate and client disconnect gracefully (remove
//      subscriptions from the map, close the WebSocket).

// SubscriptionManager tracks active GraphQL subscriptions and delivers
// real-time updates over WebSocket connections.
type SubscriptionManager interface {
	// Register adds a new subscription for the given API and connection.
	Register(ctx context.Context, apiID string, subscriptionID string, query string, variables map[string]any) error
	// Unregister removes a subscription.
	Unregister(apiID string, subscriptionID string)
	// Publish fans out a mutation result to all matching subscriptions.
	Publish(ctx context.Context, apiID string, typeName string, data json.RawMessage) error
}
