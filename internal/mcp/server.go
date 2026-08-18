package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Tool struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description"`
	Annotations  map[string]any   `json:"annotations,omitempty"`
	Execution    map[string]any   `json:"execution,omitempty"`
	InputSchema  json.RawMessage  `json:"inputSchema"`
	OutputSchema json.RawMessage  `json:"outputSchema,omitempty"`
	Icons        []map[string]any `json:"icons,omitempty"`
}

// ToolResult is a first-class tools/call result envelope.
// Providers can return this directly when they need explicit control over the
// human-readable content alongside machine-readable structuredContent.
type ToolResult struct {
	Content           []map[string]any `json:"content,omitempty"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

type ToolProvider interface {
	Tools() []Tool
	Handler(name string) (HandlerFunc, bool)
}

// ResourceProvider allows a provider to back MCP resources/* methods.
// When absent, the server keeps returning empty baseline responses.
type ResourceProvider interface {
	ListResources(ctx context.Context) ([]map[string]any, error)
	ReadResource(ctx context.Context, uri string) ([]map[string]any, error)
	ListResourceTemplates(ctx context.Context) ([]map[string]any, error)
}

// PromptProvider allows a provider to back MCP prompts/* methods.
type PromptProvider interface {
	ListPrompts(ctx context.Context) ([]map[string]any, error)
	GetPrompt(ctx context.Context, name string) ([]map[string]any, bool, error)
}

// PromptListChangedEmitterProvider allows providers to request outbound
// notifications/prompts/list_changed emissions when prompt inventories change
// outside MCP prompts/list requests.
type PromptListChangedEmitterProvider interface {
	SetPromptListChangedEmitter(cb func())
}

// ResourceListChangedEmitterProvider allows providers to request outbound
// notifications/resources/list_changed emissions when they detect mutations
// that occur outside MCP tools/call execution.
type ResourceListChangedEmitterProvider interface {
	SetResourceListChangedEmitter(cb func())
}

// ResourceUpdatedEmitterProvider allows providers to request outbound
// notifications/resources/updated emissions for concrete resource URIs.
type ResourceUpdatedEmitterProvider interface {
	SetResourceUpdatedEmitter(cb func(uri string))
}

type Server struct {
	logger   *slog.Logger
	mu       sync.RWMutex
	tools    []Tool
	handlers map[string]HandlerFunc
	// Capability advertisement - what the server supports
	capabilities   ServerCapabilities
	inFlight       map[string]*inFlightRequest
	activeProgress map[string]string
	// subscriptions are the open `subscriptions/listen` streams. Each carries
	// its own filter, which is what lets a notification be discarded before it
	// is ever serialised — see subscriptions.go on why that matters to the
	// emulator and not just to MCP.
	subscriptions     map[*subscription]struct{}
	resourceProviders []ResourceProvider
	promptProviders   []PromptProvider
	requestTimeout    time.Duration
	authBearerToken   string
	// shutdown is closed by the host when the process is going down, and is
	// what releases a `subscriptions/listen` stream. The request context cannot
	// do that job on its own: it ends when the client goes away, and an attached
	// MCP client that has simply gone quiet never does. Nil when no host wired
	// one, which a select reads as "never fires" — the right behaviour for a
	// Server nobody is shutting down.
	shutdown <-chan struct{}
}

type inFlightRequest struct {
	rawRequestID  any
	cancel        context.CancelFunc
	progressToken string
	cancelled     bool

	// stream is the response this request is being answered on, or nil if the
	// client did not ask for one. Its notifications go here and nowhere else.
	stream *requestStream
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Meta    map[string]any  `json:"_meta,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`

	// httpStatus overrides the status this error is written with, for the cases
	// where 2026-07-28 pins one to a code that otherwise carries another.
	//
	// A missing required `_meta` field is the case that needs it: the revision
	// says such a request "MUST" be rejected with -32602 *and* "the response
	// status MUST be 400 Bad Request" — but -32602 is also plain JSON-RPC
	// invalid-params, which this server has always answered 200 for on a bad
	// tool argument. The code alone cannot distinguish them, so the site that
	// knows which one it is says so. Never serialised; the wire carries only
	// the JSON-RPC error.
	httpStatus int
}

type promptDefinition struct {
	Name        string
	Title       string
	Description string
	Messages    []map[string]any
}

// ServerCapabilities represents what the server supports.
type ServerCapabilities struct {
	Tools       *ToolCapability        `json:"tools,omitempty"`
	Resources   *ResourceCapability    `json:"resources,omitempty"`
	Prompts     *PromptsCapability     `json:"prompts,omitempty"`
	Completions *CompletionsCapability `json:"completions,omitempty"`
	Logging     *LoggingCapability     `json:"logging,omitempty"`
	Tasks       *TasksCapability       `json:"tasks,omitempty"`
}

type logMessageParams struct {
	Level  string `json:"level"`
	Logger string `json:"logger,omitempty"`
	Data   any    `json:"data"`
}

// ToolCapability describes the server's tools support.
type ToolCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourceCapability describes the server's resources support.
type ResourceCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability describes the server's prompts support.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// CompletionsCapability describes the server's completions support.
type CompletionsCapability struct {
}

// LoggingCapability describes the server's logging support.
type LoggingCapability struct {
}

// TasksCapability describes the server's tasks support.
type TasksCapability struct {
	List     bool                   `json:"list,omitempty"`
	Cancel   bool                   `json:"cancel,omitempty"`
	Requests map[string]interface{} `json:"requests,omitempty"`
}

const (
	RPCParseError     = -32700
	RPCInvalidRequest = -32600
	RPCMethodNotFound = -32601
	RPCInvalidParams  = -32602
	RPCInternalError  = -32603

	// The codes 2026-07-28 allocates from the -32020..-32099 range it reserves
	// for the specification. Implementations "MUST NOT emit any code from this
	// sub-range that is not defined by this specification".
	RPCHeaderMismatch                  = -32020
	RPCMissingRequiredClientCapability = -32021
	RPCUnsupportedProtocolVersion      = -32022

	// ProtocolVersion is the revision this server speaks, and the one every
	// request must declare for itself in `_meta`.
	//
	// There is exactly one. During the migration there were two names here,
	// because there were two eras to tell apart — the spec's own "legacy" and
	// "modern", the distinction being whether version and capabilities arrive
	// per request or are negotiated once. Phase 4 removed the legacy half, and a
	// qualifier that no longer distinguishes anything is worse than none.
	ProtocolVersion = "2026-07-28"

	// The reserved `_meta` keys this server reads. The `io.modelcontextprotocol`
	// prefix is reserved for MCP's own use, so these names cannot collide with
	// anything a caller puts alongside them.
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaLogLevel           = "io.modelcontextprotocol/logLevel"
	DefaultRequestTimeout  = 30 * time.Second
)

// sseHeartbeatInterval is how often an idle SSE stream emits a keep-alive, and
// matches the interval the emulator's other two SSE endpoints use — see
// eventsHandler and domainsWatchHandler in internal/router.
//
// # Why the keep-alive is an SSE comment
//
// Because that is the shape MCP itself points at. Revision 2026-07-28 says of
// long-lived streams that "servers are encouraged to periodically emit an SSE
// comment line (a line beginning with a colon, e.g. `:\r\n`) as a keep-alive.
// This keeps the connection from being closed by intermediaries or client idle
// timeouts during quiet periods when no notifications are flowing. Per the SSE
// specification, any line beginning with a colon is a comment that carries no
// event data; clients must ignore such lines and must not treat them as
// malformed input."
//
// That passage is guidance rather than a requirement — "encouraged", not SHOULD
// or MUST — so it is not license to call this mandated. What it settles is the
// question that matters here: the client-side half ("clients must ignore such
// lines") is stated flatly, so a comment cannot be mistaken for a message by a
// conforming client. The cadence is left entirely undefined, which is why the
// interval below is chosen to match this repo rather than the spec.
//
// The stream it is written for is the one that survives. A
// `subscriptions/listen` response stays open for as long as its client wants
// notifications, and with `ping` removed the keep-alive is the only liveness
// mechanism left on it: nothing else obliges either end to say anything during
// a quiet period.
//
// Two shapes were rejected. A `data:` event carrying a heartbeat payload — what
// eventsHandler sends on the emulator's own event stream — would put a frame on
// this stream that clients parse as JSON-RPC, so it would be a protocol
// violation rather than a keep-alive. `ping` was the other, and it is gone with
// the rest of the utility methods; it was a JSON-RPC request that obliged the
// receiver to answer, which is more than a liveness probe needs to be.
//
// The comment carries no `id:`, which now costs nothing to keep: event IDs were
// the resumability cursor, and 2026-07-28 removes resumability along with the
// GET stream it belonged to.
//
// A var rather than a const so tests can wind it down; nothing else writes it.
var sseHeartbeatInterval = 15 * time.Second

func NewServer(logger *slog.Logger, providers ...ToolProvider) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:         logger,
		handlers:       make(map[string]HandlerFunc),
		capabilities:   defaultServerCapabilities(),
		inFlight:       make(map[string]*inFlightRequest),
		activeProgress: make(map[string]string),
		requestTimeout: DefaultRequestTimeout,
	}
	for _, provider := range providers {
		s.registerProvider(provider)
	}
	return s
}

func (s *Server) registerProvider(provider ToolProvider) {
	if provider == nil {
		return
	}
	tools := provider.Tools()
	addedTools := len(tools) > 0
	addedResources := false
	addedPrompts := false
	s.mu.Lock()
	for i := range tools {
		if strings.TrimSpace(tools[i].Title) == "" {
			tools[i].Title = humanizeToolName(tools[i].Name)
		}
		if (strings.HasPrefix(tools[i].Name, "repo_") || strings.HasPrefix(tools[i].Name, "runtime_")) && len(tools[i].OutputSchema) == 0 {
			// Use a permissive schema when providers omit output contracts so
			// clients still receive an explicit outputSchema field.
			tools[i].OutputSchema = json.RawMessage(`true`)
		}
		if (strings.HasPrefix(tools[i].Name, "repo_") || strings.HasPrefix(tools[i].Name, "runtime_")) && len(tools[i].Icons) == 0 {
			tools[i].Icons = defaultToolIcons(tools[i])
		}
		if (strings.HasPrefix(tools[i].Name, "repo_") || strings.HasPrefix(tools[i].Name, "runtime_")) && tools[i].Annotations == nil {
			tools[i].Annotations = map[string]any{"readOnlyHint": true}
		}
		if (strings.HasPrefix(tools[i].Name, "repo_") || strings.HasPrefix(tools[i].Name, "runtime_")) && tools[i].Execution == nil {
			tools[i].Execution = map[string]any{
				"readOnlyHint":    true,
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   false,
			}
		}
		enrichToolExecutionMetadata(&tools[i])
	}
	s.tools = append(s.tools, tools...)
	if rp, ok := provider.(ResourceProvider); ok {
		s.resourceProviders = append(s.resourceProviders, rp)
		addedResources = true
	}
	if pp, ok := provider.(PromptProvider); ok {
		s.promptProviders = append(s.promptProviders, pp)
		addedPrompts = true
	}
	if emitterAware, ok := provider.(ResourceListChangedEmitterProvider); ok {
		emitterAware.SetResourceListChangedEmitter(s.emitResourceListChanged)
	}
	if emitterAware, ok := provider.(ResourceUpdatedEmitterProvider); ok {
		emitterAware.SetResourceUpdatedEmitter(s.emitResourceUpdated)
	}
	if emitterAware, ok := provider.(PromptListChangedEmitterProvider); ok {
		emitterAware.SetPromptListChangedEmitter(s.emitPromptsListChanged)
	}
	for _, tool := range tools {
		if handler, ok := provider.Handler(tool.Name); ok {
			s.handlers[tool.Name] = handler
		}
	}
	s.mu.Unlock()

	// Emission is unconditional, and this is where the handshake stopped being
	// load-bearing.
	//
	// It used to be gated on `s.initDone && s.ready` — do not tell a client the
	// list changed before it has finished initialising. In 2026-07-28 there is no
	// such moment to wait for: "There is no negotiation handshake", so the
	// condition has no successor to be rewritten into. Nor does it need one. A
	// client hears about a change because it opened a `subscriptions/listen`
	// stream and named the type, which is a stronger statement of readiness than
	// a handshake ever was — it cannot have subscribed before it was ready to
	// receive.
	//
	// This costs nothing when nobody is listening: emitToSubscriptions returns on
	// a length check before it serialises anything. See subscriptions.go.
	if addedTools {
		s.emitToolsListChanged()
	}
	if addedResources {
		s.emitResourceListChanged()
	}
	if addedPrompts {
		s.emitPromptsListChanged()
	}
}

func (s *Server) Handler() http.Handler {
	return http.StripPrefix("/mcp", s.RootHandler())
}

// RootHandler serves MCP routes at handler root ("/") without path rewriting.
// Callers that mount under a prefix should wrap it with http.StripPrefix.
func (s *Server) RootHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", s.handleRPC)
	return mux
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if err := s.validateAuthorizationHeader(r.Header.Get("Authorization")); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="overcast-mcp"`)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !isOriginAllowed(r.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		// The capture path below buffers the whole response and writes it as
		// one SSE event, which is right for a request that has an answer and
		// wrong for one that is a stream. `subscriptions/listen` stays open
		// indefinitely and must write as it goes, so it needs the real
		// ResponseWriter — captureResponseWriter is not a Flusher, and a stream
		// that cannot flush is a stream nothing arrives on.
		// Peeking consumes the body, so the replacement is put back whatever
		// the answer is — the path not taken still has to be able to read it.
		if body, method, ok := peekMethod(r); body != nil {
			r.Body = body
			if ok && method == subscriptionsListenMethod {
				s.handleRPCInternal(w, r)
				return
			}
		}
		// Everything else gets a real stream: the request's own notifications as
		// they happen, then its answer. The result is still captured rather than
		// written straight through, because it must be the *last* event — a
		// client reading in order should see the work reported before the answer
		// to it. See request_stream.go.
		flusher, streamable := w.(http.Flusher)
		if !streamable {
			s.writeRPCError(w, nil, RPCInternalError, "streaming unsupported")
			return
		}
		stream := &requestStream{out: sseWriter{w: w, flusher: flusher}, http: w}
		capture := &captureResponseWriter{headers: make(http.Header), statusCode: http.StatusOK}
		s.handleRPCInternal(capture, r.WithContext(withRequestStream(r.Context(), stream)))

		// Nothing was streamed and the handler wants a non-OK status, so this
		// never became a stream and should answer as itself. That is how a
		// rejected protocol version stays an HTTP error rather than becoming a
		// 200 with the refusal buried in an event.
		if !stream.hasStarted() && capture.statusCode != http.StatusOK && capture.statusCode != http.StatusNoContent {
			for k, vals := range capture.headers {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(capture.statusCode)
			_, _ = io.WriteString(w, capture.body.String())
			return
		}

		for k, vals := range capture.headers {
			if strings.EqualFold(k, "Content-Type") {
				continue
			}
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		stream.finish(strings.TrimSpace(capture.body.String()))
		return
	}

	s.handleRPCInternal(w, r)
}

func (s *Server) handleRPCInternal(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeRPCError(w, nil, RPCParseError, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeRPCError(w, req.ID, RPCInvalidRequest, `jsonrpc must be "2.0"`)
		return
	}

	// JSON-RPC notifications have no "id". The server MUST NOT reply.
	// Spec §1.1: "The Server MUST NOT reply to Notifications"
	if req.ID == nil {
		s.handleNotification(req)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// For requests, ID MUST be non-null (spec §1.2)
	// ID is already non-null here since we handle nil above

	reqIDKey, err := requestIDKey(req.ID)
	if err != nil {
		s.writeRPCError(w, req.ID, RPCInvalidRequest, "invalid request id")
		return
	}

	// What the request says about itself: its version, the client's identity and
	// that client's capabilities, on every request. There is no handshake to
	// have completed and no session to belong to — "Every request carries its
	// protocol version, and the server accepts or rejects each request
	// independently". See request_meta.go.
	//
	// This comes before any of the per-request state below because a request
	// this server will not serve should not be registered as one it is serving.
	// It also settles the ordering the log threshold needs: the stream is
	// configured here, before registerInFlight publishes it to the goroutine
	// that may cancel this request, so nothing can be reading it as it is
	// written.
	meta, metaErr := parseRequestMeta(req.Params)
	if metaErr != nil {
		writeJSONRPCError(w, req.ID, metaErr)
		return
	}
	if versionErr := meta.validate(r.Header.Get("MCP-Protocol-Version")); versionErr != nil {
		writeJSONRPCError(w, req.ID, versionErr)
		return
	}
	if headerErr := validateStandardHeaders(r, req, meta); headerErr != nil {
		writeJSONRPCError(w, req.ID, headerErr)
		return
	}
	stream := requestStreamFrom(r.Context())
	stream.setLogLevel(meta.logLevel)

	progressToken, rawProgressToken, hasProgressToken, progressTokenErr := extractProgressToken(req.Params)
	if progressTokenErr != nil {
		s.writeRPCError(w, req.ID, RPCInvalidParams, progressTokenErr.Error())
		return
	}
	if hasProgressToken {
		if !s.registerProgressToken(reqIDKey, progressToken) {
			s.writeRPCError(w, req.ID, RPCInvalidParams, "progressToken must be unique across active requests")
			return
		}
		defer s.unregisterProgressToken(reqIDKey)
	}

	requestTimeout := s.requestTimeout
	var requestCtx context.Context
	var cancel context.CancelFunc
	if requestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(r.Context(), requestTimeout)
	} else {
		requestCtx, cancel = context.WithCancel(r.Context())
	}
	defer cancel()
	s.registerInFlight(reqIDKey, req.ID, progressToken, cancel, stream)
	defer s.unregisterInFlight(reqIDKey)

	switch req.Method {
	case discoverMethod:
		s.handleDiscover(w, req, r)
	case subscriptionsListenMethod:
		s.handleSubscriptionsListen(w, req, r)
	case "tools/list":
		s.handleToolsList(w, req)
	case "tools/call":
		s.handleToolsCall(requestCtx, reqIDKey, rawProgressToken, w, req)
	case "resources/list":
		s.handleResourcesList(requestCtx, w, req)
	case "resources/templates/list":
		s.handleResourceTemplatesList(requestCtx, w, req)
	case "resources/read":
		s.handleResourcesRead(requestCtx, w, req)
	case "prompts/list":
		s.handlePromptsList(requestCtx, w, req)
	case "prompts/get":
		s.handlePromptsGet(requestCtx, w, req)
	case "completion/complete":
		s.handleCompletionComplete(requestCtx, w, req)
	default:
		s.writeRPCError(w, req.ID, RPCMethodNotFound, "method not found: "+req.Method)
	}
}

func defaultPromptCatalog() []promptDefinition {
	return []promptDefinition{
		{
			Name:        "example",
			Title:       "Example Prompt",
			Description: "Example baseline prompt for MCP prompt discovery and completion tests.",
			Messages: []map[string]any{{
				"role": "user",
				"content": []map[string]any{{
					"type": "text",
					"text": "Summarize the current Overcast MCP state and suggest the next validation step.",
				}},
			}},
		},
		{
			Name:        "validate_next_step",
			Title:       "Validate Next Step",
			Description: "Summarize the current MCP work and choose the next focused validation or implementation step.",
			Messages: []map[string]any{{
				"role": "user",
				"content": []map[string]any{{
					"type": "text",
					"text": "Propose one focused validation check for the latest MCP changes, then suggest the next smallest implementation step.",
				}},
			}},
		},
	}
}

func promptCatalogEntries() []map[string]any {
	catalog := defaultPromptCatalog()
	prompts := make([]map[string]any, 0, len(catalog))
	for _, prompt := range catalog {
		prompts = append(prompts, map[string]any{
			"name":        prompt.Name,
			"title":       prompt.Title,
			"description": prompt.Description,
		})
	}
	return prompts
}

func promptCatalogNames() []string {
	catalog := defaultPromptCatalog()
	names := make([]string, 0, len(catalog))
	for _, prompt := range catalog {
		names = append(names, prompt.Name)
	}
	return names
}

func promptByName(name string) (promptDefinition, bool) {
	lookup := strings.TrimSpace(name)
	for _, prompt := range defaultPromptCatalog() {
		if prompt.Name == lookup {
			return prompt, true
		}
	}
	return promptDefinition{}, false
}

func defaultPrompts() []map[string]any { return promptCatalogEntries() }

func dedupePromptEntriesByName(prompts []map[string]any) []map[string]any {
	if len(prompts) == 0 {
		return prompts
	}
	filtered := make([]map[string]any, 0, len(prompts))
	seen := make(map[string]struct{}, len(prompts))
	for _, prompt := range prompts {
		name, _ := prompt["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		filtered = append(filtered, prompt)
	}
	return filtered
}

func promptMessages(name string) ([]map[string]any, bool) {
	prompt, ok := promptByName(name)
	if !ok {
		return nil, false
	}
	return prompt.Messages, true
}

func (s *Server) snapshotResourceProviders() []ResourceProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ResourceProvider(nil), s.resourceProviders...)
}

func (s *Server) snapshotPromptProviders() []PromptProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]PromptProvider(nil), s.promptProviders...)
}

func (s *Server) listPromptsFromProviders(ctx context.Context, tolerateErrors bool) ([]map[string]any, error) {
	providers := s.snapshotPromptProviders()
	prompts := make([]map[string]any, 0, 16)
	for _, provider := range providers {
		items, err := provider.ListPrompts(ctx)
		if err != nil {
			if tolerateErrors {
				continue
			}
			return nil, err
		}
		prompts = append(prompts, items...)
	}
	return prompts, nil
}

func (s *Server) promptMessagesFromProviders(ctx context.Context, name string, tolerateErrors bool) ([]map[string]any, bool, error) {
	providers := s.snapshotPromptProviders()
	for _, provider := range providers {
		messages, found, err := provider.GetPrompt(ctx, name)
		if err != nil {
			if tolerateErrors {
				continue
			}
			return nil, false, err
		}
		if found {
			return messages, true, nil
		}
	}
	return nil, false, nil
}

func (s *Server) suggestPromptNameValues(ctx context.Context, prefix string) []any {
	candidates := promptCatalogNames()
	providerPrompts, err := s.listPromptsFromProviders(ctx, true)
	if err != nil {
		return uniquePrefixMatches(candidates, prefix)
	}
	for _, prompt := range providerPrompts {
		name, _ := prompt["name"].(string)
		name = strings.TrimSpace(name)
		if name != "" {
			candidates = append(candidates, name)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestPromptTitleValues(ctx context.Context, prefix string) []any {
	catalog := defaultPromptCatalog()
	candidates := make([]string, 0, len(catalog))
	for _, prompt := range catalog {
		title := strings.TrimSpace(prompt.Title)
		if title != "" {
			candidates = append(candidates, title)
		}
	}
	providerPrompts, err := s.listPromptsFromProviders(ctx, true)
	if err == nil {
		for _, prompt := range providerPrompts {
			title, _ := prompt["title"].(string)
			title = strings.TrimSpace(title)
			if title != "" {
				candidates = append(candidates, title)
			}
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestPromptDescriptionValues(ctx context.Context, prefix string) []any {
	catalog := defaultPromptCatalog()
	candidates := make([]string, 0, len(catalog))
	for _, prompt := range catalog {
		description := strings.TrimSpace(prompt.Description)
		if description != "" {
			candidates = append(candidates, description)
		}
	}
	providerPrompts, err := s.listPromptsFromProviders(ctx, true)
	if err == nil {
		for _, prompt := range providerPrompts {
			description, _ := prompt["description"].(string)
			description = strings.TrimSpace(description)
			if description != "" {
				candidates = append(candidates, description)
			}
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) listResourceTemplatesFromProviders(ctx context.Context, tolerateErrors bool) ([]map[string]any, error) {
	providers := s.snapshotResourceProviders()
	templates := make([]map[string]any, 0, 32)
	for _, provider := range providers {
		items, err := provider.ListResourceTemplates(ctx)
		if err != nil {
			if tolerateErrors {
				continue
			}
			return nil, err
		}
		templates = append(templates, items...)
	}
	return templates, nil
}

func (s *Server) listResourcesFromProviders(ctx context.Context, tolerateErrors bool) ([]map[string]any, error) {
	providers := s.snapshotResourceProviders()
	resources := make([]map[string]any, 0, 64)
	for _, provider := range providers {
		items, err := provider.ListResources(ctx)
		if err != nil {
			if tolerateErrors {
				continue
			}
			return nil, err
		}
		resources = append(resources, items...)
	}
	return resources, nil
}

func (s *Server) readResourceFromProviders(ctx context.Context, uri string, tolerateErrors bool) ([]map[string]any, error) {
	providers := s.snapshotResourceProviders()
	contents := make([]map[string]any, 0, 8)
	for _, provider := range providers {
		items, err := provider.ReadResource(ctx, uri)
		if err != nil {
			if tolerateErrors {
				continue
			}
			return nil, err
		}
		if len(items) > 0 {
			contents = append(contents, items...)
		}
	}
	return contents, nil
}

func (s *Server) suggestResourceTemplateValues(ctx context.Context, prefix string) []any {
	templates, err := s.listResourceTemplatesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, 8)
	for _, template := range templates {
		uriTemplate, _ := template["uriTemplate"].(string)
		candidates = append(candidates, uriTemplate)
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceTemplateNames(ctx context.Context, prefix string) []any {
	templates, err := s.listResourceTemplatesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(templates))
	for _, template := range templates {
		name, _ := template["name"].(string)
		name = strings.TrimSpace(name)
		if name != "" {
			candidates = append(candidates, name)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceTemplateMimeTypes(ctx context.Context, prefix string) []any {
	templates, err := s.listResourceTemplatesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(templates))
	for _, template := range templates {
		mimeType, _ := template["mimeType"].(string)
		mimeType = strings.TrimSpace(mimeType)
		if mimeType != "" {
			candidates = append(candidates, mimeType)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceTemplateDescriptions(ctx context.Context, prefix string) []any {
	templates, err := s.listResourceTemplatesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(templates))
	for _, template := range templates {
		description, _ := template["description"].(string)
		description = strings.TrimSpace(description)
		if description != "" {
			candidates = append(candidates, description)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceValues(ctx context.Context, prefix string) []any {
	resources, err := s.listResourcesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(resources))
	for _, resource := range resources {
		uri, _ := resource["uri"].(string)
		uri = strings.TrimSpace(uri)
		if uri != "" {
			candidates = append(candidates, uri)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceNames(ctx context.Context, prefix string) []any {
	resources, err := s.listResourcesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(resources))
	for _, resource := range resources {
		name, _ := resource["name"].(string)
		name = strings.TrimSpace(name)
		if name != "" {
			candidates = append(candidates, name)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceMimeTypes(ctx context.Context, prefix string) []any {
	resources, err := s.listResourcesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(resources))
	for _, resource := range resources {
		mimeType, _ := resource["mimeType"].(string)
		mimeType = strings.TrimSpace(mimeType)
		if mimeType != "" {
			candidates = append(candidates, mimeType)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) suggestResourceDescriptions(ctx context.Context, prefix string) []any {
	resources, err := s.listResourcesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	candidates := make([]string, 0, len(resources))
	for _, resource := range resources {
		description, _ := resource["description"].(string)
		description = strings.TrimSpace(description)
		if description != "" {
			candidates = append(candidates, description)
		}
	}
	return uniquePrefixMatches(candidates, prefix)
}

func (s *Server) handlePromptsList(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest) {
	prompts := append([]map[string]any{}, defaultPrompts()...)
	providerPrompts, listErr := s.listPromptsFromProviders(ctx, false)
	if listErr != nil {
		s.writeRPCError(w, req.ID, RPCInternalError, "prompts/list failed: "+listErr.Error())
		return
	}
	prompts = append(prompts, providerPrompts...)
	prompts = dedupePromptEntriesByName(prompts)
	result, err := paginatedListResult(req.Params, "prompts", prompts)
	if err != nil {
		writeJSONRPCError(w, req.ID, err)
		return
	}
	writeRPCResult(w, req.ID, result)
}

// handleNotification processes notifications (which have no ID and should not be replied to).
func (s *Server) handleNotification(req jsonRPCRequest) {
	switch req.Method {
	case "notifications/cancelled":
		var params struct {
			RequestID any    `json:"requestId"`
			Reason    string `json:"reason"`
		}
		if len(req.Params) == 0 {
			return
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return
		}
		requestIDKey, err := requestIDKey(params.RequestID)
		if err != nil {
			return
		}
		s.cancelInFlight(requestIDKey, strings.TrimSpace(params.Reason))
	case "notifications/progress":
		// Progress notifications are fire-and-forget. We accept and ignore them.
	case "notifications/resources/updated":
		// Resource-updated notifications are accepted and ignored.
	case "notifications/resources/list_changed":
		// Resource-list change notifications are accepted and ignored.
	case "notifications/prompts/list_changed":
		// Prompt-list change notifications are accepted and ignored.
	case "notifications/tools/list_changed":
		// Tool-list change notifications are accepted and ignored.
	case "notifications/message":
		// Log message from client - just informational
	}
}

// handleToolsList handles tools/list request.
func (s *Server) handleToolsList(w http.ResponseWriter, req jsonRPCRequest) {
	s.mu.RLock()
	tools := make([]Tool, len(s.tools))
	copy(tools, s.tools)
	s.mu.RUnlock()

	result, err := paginatedListResult(req.Params, "tools", tools)
	if err != nil {
		writeJSONRPCError(w, req.ID, err)
		return
	}
	writeRPCResult(w, req.ID, result)
}

func (s *Server) handleToolsCall(ctx context.Context, reqIDKey string, rawProgressToken any, w http.ResponseWriter, req jsonRPCRequest) {
	type callParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	call, paramsErr := decodeOptionalParams[callParams](req.Params)
	if paramsErr != nil {
		writeJSONRPCError(w, req.ID, paramsErr)
		return
	}
	if call.Name == "" {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "tool name required")
		return
	}
	s.mu.RLock()
	handler, ok := s.handlers[call.Name]
	s.mu.RUnlock()
	if !ok {
		s.writeRPCError(w, req.ID, RPCMethodNotFound, "unknown tool: "+call.Name)
		return
	}
	// The stream this call is being answered on, if the client asked for one.
	// Everything below reports to it and to nowhere else.
	stream := requestStreamFrom(ctx)
	if rawProgressToken != nil {
		s.emitProgress(stream, rawProgressToken, 0, 1)
	}
	result, err := handler(ctx, call.Arguments)
	if s.isInFlightCancelled(reqIDKey) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if rawProgressToken != nil {
		s.emitProgress(stream, rawProgressToken, 1, 1)
	}
	if err != nil {
		s.emitLogMessage(stream, "error", err.Error())
		writeRPCResult(w, req.ID, normalizeToolResult(nil, err))
		return
	}
	if tool, ok := s.lookupTool(call.Name); ok && toolMutationAffectsRuntimeResources(tool) {
		s.emitResourceListChanged()
	}
	writeRPCResult(w, req.ID, normalizeToolResult(result, nil))
}

func (s *Server) handleResourcesList(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest) {
	resources, listErr := s.listResourcesFromProviders(ctx, false)
	if listErr != nil {
		s.writeRPCError(w, req.ID, RPCInternalError, "resources/list failed: "+listErr.Error())
		return
	}
	result, err := paginatedListResult(req.Params, "resources", resources)
	if err != nil {
		writeJSONRPCError(w, req.ID, err)
		return
	}
	writeRPCResult(w, req.ID, result)
}

func (s *Server) handleResourceTemplatesList(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest) {
	templates, listErr := s.listResourceTemplatesFromProviders(ctx, false)
	if listErr != nil {
		s.writeRPCError(w, req.ID, RPCInternalError, "resources/templates/list failed: "+listErr.Error())
		return
	}
	result, pageErr := paginatedListResult(req.Params, "resourceTemplates", templates)
	if pageErr != nil {
		writeJSONRPCError(w, req.ID, pageErr)
		return
	}
	writeRPCResult(w, req.ID, result)
}

func (s *Server) handleResourcesRead(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest) {
	uri, uriErr := decodeRequiredURIParam(req.Params, "resources/read")
	if uriErr != nil {
		writeJSONRPCError(w, req.ID, uriErr)
		return
	}
	providers := s.snapshotResourceProviders()
	if len(providers) == 0 {
		writeRPCResult(w, req.ID, markCacheable(map[string]any{"contents": []any{}}))
		return
	}
	contents, readErr := s.readResourceFromProviders(ctx, uri, true)
	if readErr != nil {
		s.writeRPCError(w, req.ID, RPCInternalError, "resources/read failed: "+readErr.Error())
		return
	}
	if len(contents) == 0 {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "resource not found")
		return
	}
	writeRPCResult(w, req.ID, markCacheable(map[string]any{"contents": contents}))
}

func (s *Server) handlePromptsGet(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest) {
	type promptGetParams struct {
		Name string `json:"name"`
	}
	params, paramsErr := decodeRequiredParams[promptGetParams](req.Params, "prompts/get")
	if paramsErr != nil {
		writeJSONRPCError(w, req.ID, paramsErr)
		return
	}
	if strings.TrimSpace(params.Name) == "" {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "prompts/get name required")
		return
	}
	messages, ok := promptMessages(params.Name)
	if !ok {
		providerMessages, found, providerErr := s.promptMessagesFromProviders(ctx, params.Name, false)
		if providerErr != nil {
			s.writeRPCError(w, req.ID, RPCInternalError, "prompts/get failed: "+providerErr.Error())
			return
		}
		if !found {
			s.writeRPCError(w, req.ID, RPCInvalidParams, "prompt not found")
			return
		}
		messages = providerMessages
	}
	if len(messages) == 0 {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "prompt not found")
		return
	}
	writeRPCResult(w, req.ID, map[string]any{"messages": messages})
}

func (s *Server) handleCompletionComplete(ctx context.Context, w http.ResponseWriter, req jsonRPCRequest) {
	type completionParams struct {
		Ref      map[string]any `json:"ref"`
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
	}
	params, paramsErr := decodeOptionalParams[completionParams](req.Params)
	if paramsErr != nil {
		writeJSONRPCError(w, req.ID, paramsErr)
		return
	}
	var values []any
	refType, _ := params.Ref["type"].(string)
	argumentName := strings.ToLower(strings.TrimSpace(params.Argument.Name))
	prefix := strings.TrimSpace(params.Argument.Value)
	switch refType {
	case "ref/prompt":
		if argumentName == "title" {
			values = s.suggestPromptTitleValues(ctx, prefix)
		} else if argumentName == "description" {
			values = s.suggestPromptDescriptionValues(ctx, prefix)
		} else if argumentName == "name" {
			values = s.suggestPromptNameValues(ctx, prefix)
		} else {
			values = s.suggestPromptFieldValues(ctx, prefix, argumentName)
			if len(values) == 0 {
				values = s.suggestPromptNameValues(ctx, prefix)
			}
		}
	case "ref/resource":
		if argumentName == "name" {
			values = s.suggestResourceNames(ctx, prefix)
		} else if argumentName == "description" {
			values = s.suggestResourceDescriptions(ctx, prefix)
		} else if argumentName == "mimetype" || argumentName == "mime_type" {
			values = s.suggestResourceMimeTypes(ctx, prefix)
		} else if argumentName == "uri" {
			values = s.suggestResourceValues(ctx, prefix)
		} else {
			values = s.suggestResourceFieldValues(ctx, prefix, argumentName)
			if len(values) == 0 {
				values = s.suggestResourceValues(ctx, prefix)
			}
		}
	case "ref/resourceTemplate":
		if argumentName == "name" {
			values = s.suggestResourceTemplateNames(ctx, prefix)
		} else if argumentName == "description" {
			values = s.suggestResourceTemplateDescriptions(ctx, prefix)
		} else if argumentName == "mimetype" || argumentName == "mime_type" {
			values = s.suggestResourceTemplateMimeTypes(ctx, prefix)
		} else if argumentName == "uri" || argumentName == "uri_template" {
			values = s.suggestResourceTemplateValues(ctx, prefix)
		} else {
			values = s.suggestResourceTemplateFieldValues(ctx, prefix, argumentName)
			if len(values) == 0 {
				values = s.suggestResourceTemplateValues(ctx, prefix)
			}
		}
	default:
		values = s.suggestPromptNameValues(ctx, prefix)
	}
	writeRPCResult(w, req.ID, map[string]any{"completion": map[string]any{"values": values, "hasMore": false}})
}

func (s *Server) writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeRPCResult(w, id, jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func writeRPCResult(w http.ResponseWriter, id any, payload any) {
	w.Header().Set("Content-Type", "application/json")
	switch value := payload.(type) {
	case jsonRPCResponse:
		_ = json.NewEncoder(w).Encode(value)
	default:
		// stampResult adds the resultType and identity 2026-07-28 requires. Done
		// here so a handler cannot forget it; see its comment for why only
		// map-shaped results are touched.
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: stampResult(payload)})
	}
}

func mustMarshalString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

// TextContent creates a standard MCP text content block for tools/call results.
func TextContent(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text}}
}

// emitLogMessage reports something worth logging, on the response stream of the
// request that produced it, if that request asked to hear about it.
//
// The threshold belongs to the request rather than to the server, which is what
// `logging/setLevel` used to set and 2026-07-28 replaces: a level is named in
// `_meta` per request, so two clients cannot change what each other hears. The
// check comes before the params are built so that the overwhelmingly common
// case — a request that named no level — costs nothing. See
// requestStream.wantsLog.
func (s *Server) emitLogMessage(stream *requestStream, level string, data any) {
	if !stream.wantsLog(level) {
		return
	}
	stream.notify("notifications/message", logMessageParams{Level: level, Logger: "overcast", Data: data})
}

// emitCancelled tells a request it has been cancelled, on that request's own
// response stream.
//
// The spec requires the notification — "the server MUST send
// notifications/cancelled" — and it is about one request, so it travels with
// that request rather than being broadcast. Unlike a log message this is not
// filtered: the client asked for the cancellation, and the notification is the
// answer to that request rather than something reported in passing.
func (s *Server) emitCancelled(stream *requestStream, requestID any, reason string) {
	params := map[string]any{"requestId": requestID}
	if reason != "" {
		params["reason"] = reason
	}
	stream.notify("notifications/cancelled", params)
}

// emitProgress reports how far a request has got, on that request's own
// response stream.
func (s *Server) emitProgress(stream *requestStream, rawToken any, progress, total float64) {
	stream.notify("notifications/progress", map[string]any{
		"progressToken": rawToken,
		"progress":      progress,
		"total":         total,
	})
}

// emitResourceUpdated sends notifications/resources/updated for a URI if at least one
// client has subscribed to it via resources/subscribe.
func (s *Server) emitResourceUpdated(uri string) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return
	}

	// The audience is checked before the params map is built, and that ordering
	// is the point. Providers hold this function through
	// SetResourceUpdatedEmitter and call it whenever a resource changes, so it
	// runs on the emulator's paths, not MCP's. Building a notification body
	// first and discovering there is no audience second would charge the
	// emulator for every update whether or not anyone is using MCP.
	if !s.hasSubscriberFor("notifications/resources/updated", uri) {
		return
	}
	s.emitToSubscriptions("notifications/resources/updated", map[string]any{"uri": uri}, uri)
}

// emitResourceListChanged notifies clients that the resource list has changed.
func (s *Server) emitResourceListChanged() {
	s.emitToSubscriptions("notifications/resources/list_changed", map[string]any{}, "")
}

// emitPromptsListChanged notifies clients that the prompts list has changed.
func (s *Server) emitPromptsListChanged() {
	s.emitToSubscriptions("notifications/prompts/list_changed", map[string]any{}, "")
}

// emitToolsListChanged notifies clients that the tools list has changed.
func (s *Server) emitToolsListChanged() {
	s.emitToSubscriptions("notifications/tools/list_changed", map[string]any{}, "")
}

func toolResultText(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case json.RawMessage:
		return string(value)
	default:
		return mustMarshalString(v)
	}
}

type captureResponseWriter struct {
	headers    http.Header
	body       strings.Builder
	statusCode int
}

func (w *captureResponseWriter) Header() http.Header {
	return w.headers
}

func (w *captureResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *captureResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

// ServeStdio runs the MCP server over a newline-delimited JSON stdio transport.
// This is compatible with the stdio transport defined in MCP spec §1.3.1.
// It exits cleanly when the input stream closes (EOF) or ctx is cancelled.
//
// # Notifications reach a stdio client the same way they reach an HTTP one
//
// This loop used to subscribe to a server-wide broadcast and copy every
// notification onto stdout. 2026-07-28 has no such broadcast: a notification
// belongs either to a `subscriptions/listen` stream or to one request's
// response. Both are streams, and a stdio client opens them with an ordinary
// request — so they are served by the same handlers, writing through the
// ndjsonWriter below instead of an SSE response. See message_writer.go.
//
// # What decides concurrency now
//
// Notifications (no "id") run inline; requests are dispatched concurrently so
// tool calls can overlap. `initialize` used to run inline too, so that the
// lifecycle state it set was visible to requests behind it on the stream. There
// is no such state any more — every request carries its own version and
// capabilities — so that carve-out is gone. Notifications stay inline for a
// reason of their own: they produce no response, so there is nothing to
// parallelise and nothing to wait for.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	var writeMu sync.Mutex
	var workers sync.WaitGroup
	handler := s.RootHandler()

	// Requests run under a context this loop can cancel, and closing stdin
	// cancels it. A `subscriptions/listen` request stays open until its context
	// ends, so without this the first one a client opened would keep the loop
	// alive after EOF and ServeStdio would never return — the deferred wait
	// below would block on a stream with nothing left to read it. Cancelling
	// first and waiting second is what makes EOF actually end the session.
	loopCtx, cancelLoop := context.WithCancel(ctx)
	defer workers.Wait()
	defer cancelLoop()

	// Every stream this loop serves writes through here, under the same mutex
	// as the responses, because they share one descriptor.
	stdioOut := ndjsonWriter{out: out, mu: &writeMu}

	dispatch := func(msg []byte) {
		// Marked as stdio so the rules that belong to the HTTP binding do not
		// apply. This loop has no dispatcher of its own — it pushes a
		// synthesised request through the same handler — so without the marker
		// a header requirement written for HTTP would reject every stdio
		// request. See standard_headers.go.
		// The stdio client gets a response stream too, so the notifications that
		// belong to a request reach it the same way they reach an HTTP client.
		// Already started, because there is no header to write first.
		reqCtx := withStdioWriter(withStdioTransport(loopCtx), stdioOut)
		reqCtx = withRequestStream(reqCtx, &requestStream{out: stdioOut, started: true})
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "/", bytes.NewReader(msg))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		capture := &captureResponseWriter{headers: make(http.Header)}
		handler.ServeHTTP(capture, req)
		if capture.statusCode == http.StatusNoContent {
			return
		}
		body := strings.TrimSpace(capture.body.String())
		if body == "" {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = fmt.Fprintln(out, body)
	}

	for {
		payload, err := readStdioLine(reader)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			errResp, _ := json.Marshal(jsonRPCResponse{JSONRPC: "2.0", Error: &rpcError{Code: RPCParseError, Message: err.Error()}})
			writeMu.Lock()
			_, _ = fmt.Fprintln(out, string(errResp))
			writeMu.Unlock()
			continue
		}
		msg := append([]byte(nil), payload...)

		// Notifications run inline; see the concurrency note above.
		var peek struct {
			ID any `json:"id"`
		}
		inline := false
		if jsonErr := json.Unmarshal(msg, &peek); jsonErr == nil {
			inline = peek.ID == nil
		}
		if inline {
			dispatch(msg)
			continue
		}

		workers.Add(1)
		go func() {
			defer workers.Done()
			dispatch(msg)
		}()
	}
}

// readStdioLine reads the next non-empty line from reader, stripping surrounding whitespace.
// Returns io.EOF if the stream is cleanly closed, io.ErrUnexpectedEOF if closed mid-message.
func readStdioLine(reader *bufio.Reader) ([]byte, error) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			trimmed := bytes.TrimSpace(line)
			if err == io.EOF && len(trimmed) == 0 {
				return nil, io.EOF
			}
			if err == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		payload := bytes.TrimSpace(line)
		if len(payload) == 0 {
			continue
		}
		return payload, nil
	}
}

func isOriginAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	return false
}

// SetBearerAuthToken enables bearer-token HTTP auth checks when token is
// non-empty. Empty token disables auth checks.
func (s *Server) SetBearerAuthToken(token string) {
	s.mu.Lock()
	s.authBearerToken = strings.TrimSpace(token)
	s.mu.Unlock()
}

// SetShutdownSignal hands the server the channel its host closes when the
// process is going down. Closing it releases every SSE stream this server is
// serving.
//
// Without one, the only thing that ends a stream is the client disconnecting,
// which a graceful shutdown cannot wait for: http.Server.Shutdown and
// httptest.Server.Close both block on in-flight handlers, so one attached
// client is enough to hold either open indefinitely. The router hands over the
// same channel it gives its own long-lived handlers — see eventsHandler and
// domainsWatchHandler.
func (s *Server) SetShutdownSignal(ch <-chan struct{}) {
	s.mu.Lock()
	s.shutdown = ch
	s.mu.Unlock()
}

// ShutdownSignal reports the channel given to SetShutdownSignal, or nil if the
// server has no host shutdown to observe.
//
// It reads under the lock, so a stream that is starting while the host is still
// wiring up cannot race the setter. A nil channel blocks forever in a select,
// which is what a server with no host shutdown wants.
//
// Exported because the shutdown signal is the one piece of a server's wiring
// that nothing else reveals: a host that forgets to hand its channel over
// builds a server that looks identical and holds the process open at shutdown.
// A host can assert it wired its own channel without opening a stream to infer
// it — see internal/router.
func (s *Server) ShutdownSignal() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shutdown
}

func (s *Server) validateAuthorizationHeader(header string) error {
	s.mu.RLock()
	token := strings.TrimSpace(s.authBearerToken)
	s.mu.RUnlock()
	if token == "" {
		return nil
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return fmt.Errorf("missing bearer token")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return fmt.Errorf("invalid authorization scheme")
	}
	presented := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if presented == "" || presented != token {
		return fmt.Errorf("invalid bearer token")
	}
	return nil
}

func humanizeToolName(name string) string {
	parts := strings.Split(name, "_")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func (s *Server) lookupTool(name string) (Tool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tool := range s.tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

func toolMutationAffectsRuntimeResources(tool Tool) bool {
	if !strings.HasPrefix(tool.Name, "runtime_") {
		return false
	}
	readOnly, _ := tool.Execution["readOnlyHint"].(bool)
	openWorld, _ := tool.Execution["openWorldHint"].(bool)
	return !readOnly && !openWorld
}

func defaultToolIcons(tool Tool) []map[string]any {
	symbol := "OC"
	color := "%230f766e"
	if strings.HasPrefix(tool.Name, "runtime_") {
		symbol = "RT"
		color = "%231d4ed8"
	}
	if destructive, _ := tool.Execution["destructiveHint"].(bool); destructive {
		symbol = "MT"
		color = "%23b91c1c"
	} else if readOnly, _ := tool.Annotations["readOnlyHint"].(bool); !readOnly {
		symbol = "WR"
		color = "%23b45309"
	}
	svg := fmt.Sprintf("<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'><rect width='64' height='64' rx='12' fill='%s'/><text x='32' y='40' text-anchor='middle' font-size='18' font-family='Arial, sans-serif' font-weight='700' fill='white'>%s</text></svg>", color, symbol)
	return []map[string]any{{
		"src":      "data:image/svg+xml;utf8," + url.QueryEscape(svg),
		"mimeType": "image/svg+xml",
	}}
}

func enrichToolExecutionMetadata(tool *Tool) {
	if tool == nil || tool.Execution == nil {
		return
	}
	readOnly, _ := tool.Execution["readOnlyHint"].(bool)
	destructive, _ := tool.Execution["destructiveHint"].(bool)
	openWorld, _ := tool.Execution["openWorldHint"].(bool)

	if _, exists := tool.Execution["mutationClass"]; !exists {
		if readOnly {
			tool.Execution["mutationClass"] = "read"
		} else {
			tool.Execution["mutationClass"] = "write"
		}
	}
	if _, exists := tool.Execution["effectScope"]; !exists {
		if openWorld {
			tool.Execution["effectScope"] = "external"
		} else {
			tool.Execution["effectScope"] = "local_runtime"
		}
	}
	if _, exists := tool.Execution["reversibility"]; !exists {
		if destructive {
			tool.Execution["reversibility"] = "destructive"
		} else if readOnly {
			tool.Execution["reversibility"] = "not_applicable"
		} else {
			tool.Execution["reversibility"] = "non_destructive"
		}
	}
}

func collectStringFieldValues(items []map[string]any, field string) []string {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" {
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		for key, raw := range item {
			if strings.ToLower(strings.TrimSpace(key)) != field {
				continue
			}
			if str, ok := raw.(string); ok {
				str = strings.TrimSpace(str)
				if str != "" {
					values = append(values, str)
				}
			}
		}
	}
	return values
}

func (s *Server) suggestPromptFieldValues(ctx context.Context, prefix string, field string) []any {
	items := append([]map[string]any{}, defaultPrompts()...)
	providerPrompts, err := s.listPromptsFromProviders(ctx, true)
	if err == nil {
		items = append(items, providerPrompts...)
	}
	return uniquePrefixMatches(collectStringFieldValues(items, field), prefix)
}

func (s *Server) suggestResourceFieldValues(ctx context.Context, prefix string, field string) []any {
	resources, err := s.listResourcesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	return uniquePrefixMatches(collectStringFieldValues(resources, field), prefix)
}

func (s *Server) suggestResourceTemplateFieldValues(ctx context.Context, prefix string, field string) []any {
	templates, err := s.listResourceTemplatesFromProviders(ctx, true)
	if err != nil {
		return []any{}
	}
	return uniquePrefixMatches(collectStringFieldValues(templates, field), prefix)
}

func requestIDKey(id any) (string, error) {
	if id == nil {
		return "", fmt.Errorf("request id is required")
	}
	b, err := json.Marshal(id)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if key == "" || key == "null" {
		return "", fmt.Errorf("request id is invalid")
	}
	return key, nil
}

// extractProgressToken extracts the progress token from request params.
// Returns (internalKey, rawValue, hasToken, error).
// internalKey is used for dedup tracking; rawValue is the original token for notifications.
func extractProgressToken(params json.RawMessage) (string, any, bool, error) {
	if len(params) == 0 {
		return "", nil, false, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(params, &raw); err != nil {
		return "", nil, false, fmt.Errorf("invalid params: %w", err)
	}
	metaAny, ok := raw["_meta"]
	if !ok {
		return "", nil, false, nil
	}
	meta, ok := metaAny.(map[string]any)
	if !ok {
		return "", nil, false, fmt.Errorf("params._meta must be an object")
	}
	token, ok := meta["progressToken"]
	if !ok {
		return "", nil, false, nil
	}
	switch v := token.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", nil, false, fmt.Errorf("progressToken must not be empty")
		}
		return "str:" + v, v, true, nil
	case float64:
		return fmt.Sprintf("num:%v", v), v, true, nil
	default:
		return "", nil, false, fmt.Errorf("progressToken must be a string or number")
	}
}

func (s *Server) registerProgressToken(requestIDKey, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		return true
	}
	if existingReq, exists := s.activeProgress[token]; exists && existingReq != requestIDKey {
		return false
	}
	s.activeProgress[token] = requestIDKey
	return true
}

func (s *Server) unregisterProgressToken(requestIDKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, reqID := range s.activeProgress {
		if reqID == requestIDKey {
			delete(s.activeProgress, token)
		}
	}
}

func (s *Server) registerInFlight(requestIDKey string, rawRequestID any, progressToken string, cancel context.CancelFunc, stream *requestStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight[requestIDKey] = &inFlightRequest{rawRequestID: rawRequestID, cancel: cancel, progressToken: progressToken, stream: stream}
}

func (s *Server) unregisterInFlight(requestIDKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, requestIDKey)
}

func (s *Server) cancelInFlight(requestIDKey, reason string) {
	var requestID any
	s.mu.Lock()
	request, ok := s.inFlight[requestIDKey]
	if !ok {
		s.mu.Unlock()
		return
	}
	if request.cancelled {
		s.mu.Unlock()
		return
	}
	request.cancelled = true
	requestID = request.rawRequestID
	stream := request.stream
	request.cancel()
	s.mu.Unlock()
	s.emitCancelled(stream, requestID, reason)
}

func (s *Server) isInFlightCancelled(requestIDKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	request, ok := s.inFlight[requestIDKey]
	if !ok {
		return false
	}
	return request.cancelled
}
