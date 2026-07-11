package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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

type SSESource interface {
	RegisterSSEClient(ch chan []byte)
	UnregisterSSEClient(ch chan []byte)
}

type Server struct {
	sseSource SSESource
	logger    *slog.Logger
	mu        sync.RWMutex
	tools     []Tool
	handlers  map[string]HandlerFunc
	initDone  bool
	ready     bool
	// Capability advertisement - what the server supports
	capabilities ServerCapabilities
	// State tracking for lifecycle
	clientCapabilities      map[string]any
	negotiatedVersion       string
	sessions                map[string]time.Time
	logLevel                string
	inFlight                map[string]*inFlightRequest
	activeProgress          map[string]string
	notificationSubscribers map[chan []byte]struct{}
	sseSubscribers          map[chan notificationEvent]struct{}
	notificationReplay      []notificationEvent
	nextNotificationID      uint64
	notificationReplayLimit int
	resourceSubscriptions   map[string]int // URI -> active subscriber count
	resourceProviders       []ResourceProvider
	promptProviders         []PromptProvider
	requestTimeout          time.Duration
	authBearerToken         string
}

type notificationEvent struct {
	id      string
	payload []byte
}

type inFlightRequest struct {
	rawRequestID  any
	method        string
	cancel        context.CancelFunc
	progressToken string
	cancelled     bool
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
}

type promptDefinition struct {
	Name        string
	Title       string
	Description string
	Messages    []map[string]any
}

// InitializeResult represents the server's response to initialize.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      map[string]string  `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
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
	RPCParseError         = -32700
	RPCInvalidRequest     = -32600
	RPCMethodNotFound     = -32601
	RPCInvalidParams      = -32602
	RPCInternalError      = -32603
	ProtocolVersion       = "2025-11-25"
	DefaultRequestTimeout = 30 * time.Second
	// DefaultReplayLimit bounds in-memory SSE notification replay history per
	// process. Replay state is intentionally ephemeral and not shared across
	// restarts.
	// TODO(mcp): make replay retention configurable (and optionally durable)
	// once runtime auth and multi-process reconnect expectations are finalized.
	DefaultReplayLimit = 256
)

var (
	errInvalidLastEventID = errors.New("invalid Last-Event-ID")
	errUnknownLastEventID = errors.New("Last-Event-ID is no longer available")
)

func NewServer(sseSource SSESource, logger *slog.Logger, providers ...ToolProvider) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		sseSource:               sseSource,
		logger:                  logger,
		handlers:                make(map[string]HandlerFunc),
		capabilities:            defaultServerCapabilities(),
		negotiatedVersion:       ProtocolVersion,
		sessions:                make(map[string]time.Time),
		logLevel:                "info",
		inFlight:                make(map[string]*inFlightRequest),
		activeProgress:          make(map[string]string),
		notificationSubscribers: make(map[chan []byte]struct{}),
		sseSubscribers:          make(map[chan notificationEvent]struct{}),
		notificationReplayLimit: DefaultReplayLimit,
		resourceSubscriptions:   make(map[string]int),
		requestTimeout:          DefaultRequestTimeout,
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
	shouldEmit := s.initDone && s.ready
	s.mu.Unlock()

	if !shouldEmit {
		return
	}
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
	mux.HandleFunc("GET /", s.handleStreamableGet)
	mux.HandleFunc("POST /", s.handleRPC)
	mux.HandleFunc("DELETE /", s.handleSessionDelete)
	mux.HandleFunc("GET /sse", s.handleSSE)
	return mux
}

func (s *Server) handleStreamableGet(w http.ResponseWriter, r *http.Request) {
	if err := s.validateAuthorizationHeader(r.Header.Get("Authorization")); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="overcast-mcp"`)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !isOriginAllowed(r.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if err := s.validateOptionalSessionHeader(r.Header.Get("MCP-Session-Id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		http.Error(w, "accept must include text/event-stream", http.StatusNotAcceptable)
		return
	}
	s.handleSSE(w, r)
}

func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.validateAuthorizationHeader(r.Header.Get("Authorization")); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="overcast-mcp"`)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !isOriginAllowed(r.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	sid := strings.TrimSpace(r.Header.Get("MCP-Session-Id"))
	if sid == "" {
		http.Error(w, "missing MCP-Session-Id", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	delete(s.sessions, sid)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
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
		capture := &captureResponseWriter{headers: make(http.Header), statusCode: http.StatusOK}
		s.handleRPCInternal(capture, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for k, vals := range capture.headers {
			if strings.EqualFold(k, "Content-Type") {
				continue
			}
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		if capture.statusCode != http.StatusOK && capture.statusCode != http.StatusNoContent {
			w.WriteHeader(capture.statusCode)
		}
		if capture.body.Len() == 0 {
			_, _ = fmt.Fprint(w, ": no response\n\n")
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", strings.TrimSpace(capture.body.String()))
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

	if req.Method == "ping" {
		writeRPCResult(w, req.ID, map[string]any{})
		return
	}

	if req.Method == "initialize" {
		s.handleInitialize(w, req)
		return
	}

	reqIDKey, err := requestIDKey(req.ID)
	if err != nil {
		s.writeRPCError(w, req.ID, RPCInvalidRequest, "invalid request id")
		return
	}

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
	s.registerInFlight(reqIDKey, req.ID, req.Method, progressToken, cancel)
	defer s.unregisterInFlight(reqIDKey)

	s.mu.RLock()
	lifecycleErr := validateLifecycle(s.initDone, s.ready)
	s.mu.RUnlock()
	if lifecycleErr != nil {
		writeJSONRPCError(w, req.ID, lifecycleErr)
		return
	}
	if err := s.validateOptionalSessionHeader(r.Header.Get("MCP-Session-Id")); err != nil {
		writeJSONRPCError(w, req.ID, &rpcError{Code: RPCInvalidParams, Message: err.Error()})
		return
	}
	if versionErr := validateProtocolVersionHeader(r.Header.Get("MCP-Protocol-Version")); versionErr != nil {
		writeJSONRPCError(w, req.ID, versionErr)
		return
	}

	switch req.Method {
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
	case "resources/subscribe":
		s.handleResourcesSubscribe(w, req)
	case "resources/unsubscribe":
		s.handleResourcesUnsubscribe(w, req)
	case "prompts/list":
		s.handlePromptsList(requestCtx, w, req)
	case "prompts/get":
		s.handlePromptsGet(requestCtx, w, req)
	case "completion/complete":
		s.handleCompletionComplete(requestCtx, w, req)
	case "logging/setLevel":
		s.handleLoggingSetLevel(w, req)
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
	case "notifications/initialized":
		// Client indicates it's ready for operations after initialize
		s.mu.Lock()
		s.ready = s.initDone
		s.mu.Unlock()
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

// handleInitialize processes the initialize request per spec §1.2.
func (s *Server) handleInitialize(w http.ResponseWriter, req jsonRPCRequest) {
	var params struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ClientInfo      map[string]any `json:"clientInfo"`
	}
	if len(req.Params) == 0 {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "initialize params required")
		return
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "invalid initialize params: "+err.Error())
		return
	}
	if params.ProtocolVersion == "" {
		s.writeRPCError(w, req.ID, RPCInvalidParams, "initialize.protocolVersion required")
		return
	}

	s.mu.Lock()
	s.initDone = true
	s.ready = false
	s.clientCapabilities = params.Capabilities
	s.negotiatedVersion = ProtocolVersion // We only support our version
	sid := newSessionID()
	s.sessions[sid] = time.Now()
	caps := s.capabilities
	s.mu.Unlock()
	w.Header().Set("MCP-Session-Id", sid)

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    caps,
		ServerInfo: map[string]string{
			"name":       "overcast-mcp",
			"version":    "1.0.0",
			"serverRole": "workspace",
		},
	}

	writeRPCResult(w, req.ID, result)
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
	if rawProgressToken != nil {
		s.emitProgress(rawProgressToken, 0, 1)
	}
	result, err := handler(ctx, call.Arguments)
	if s.isInFlightCancelled(reqIDKey) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if rawProgressToken != nil {
		s.emitProgress(rawProgressToken, 1, 1)
	}
	if err != nil {
		s.emitLogMessage("error", err.Error())
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
		writeRPCResult(w, req.ID, map[string]any{"contents": []any{}})
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
	writeRPCResult(w, req.ID, map[string]any{"contents": contents})
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

func (s *Server) handleResourcesSubscribe(w http.ResponseWriter, req jsonRPCRequest) {
	normalizedURI, uriErr := decodeRequiredURIParam(req.Params, "resources/subscribe")
	if uriErr != nil {
		writeJSONRPCError(w, req.ID, uriErr)
		return
	}
	s.mu.Lock()
	s.resourceSubscriptions[normalizedURI]++
	s.mu.Unlock()
	writeRPCResult(w, req.ID, map[string]any{})
}

func (s *Server) handleResourcesUnsubscribe(w http.ResponseWriter, req jsonRPCRequest) {
	normalizedURI, uriErr := decodeRequiredURIParam(req.Params, "resources/unsubscribe")
	if uriErr != nil {
		writeJSONRPCError(w, req.ID, uriErr)
		return
	}
	s.mu.Lock()
	if s.resourceSubscriptions[normalizedURI] > 0 {
		s.resourceSubscriptions[normalizedURI]--
		if s.resourceSubscriptions[normalizedURI] == 0 {
			delete(s.resourceSubscriptions, normalizedURI)
		}
	}
	s.mu.Unlock()
	writeRPCResult(w, req.ID, map[string]any{})
}

func (s *Server) handleLoggingSetLevel(w http.ResponseWriter, req jsonRPCRequest) {
	type setLevelParams struct {
		Level string `json:"level"`
	}
	params, paramsErr := decodeRequiredParams[setLevelParams](req.Params, "logging/setLevel")
	if paramsErr != nil {
		writeJSONRPCError(w, req.ID, paramsErr)
		return
	}
	level, levelErr := normalizeLoggingLevel(params.Level)
	if levelErr != nil {
		writeJSONRPCError(w, req.ID, levelErr)
		return
	}
	s.mu.Lock()
	s.logLevel = level
	s.mu.Unlock()
	s.emitLogMessage(level, fmt.Sprintf("logging level set to %s", level))
	writeRPCResult(w, req.ID, map[string]any{})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if err := s.validateAuthorizationHeader(r.Header.Get("Authorization")); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="overcast-mcp"`)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !isOriginAllowed(r.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if err := s.validateOptionalSessionHeader(r.Header.Get("MCP-Session-Id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	replay, err := s.replayAfter(lastEventID)
	if err != nil {
		switch {
		case errors.Is(err, errInvalidLastEventID):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, errUnknownLastEventID):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if strings.TrimSpace(r.URL.Path) == "/sse" {
		// Legacy SSE endpoint remains available for compatibility, but advertise
		// the primary Streamable HTTP endpoint for migration.
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", "</mcp/>; rel=\"successor-version\"")
		w.Header().Set("Warning", `299 - "deprecated endpoint; use GET/POST /mcp/"`)
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan notificationEvent, 256)
	s.subscribeSSE(ch)
	defer s.unsubscribeSSE(ch)
	extCh := make(chan []byte, 256)
	if s.sseSource != nil {
		s.sseSource.RegisterSSEClient(extCh)
		defer s.sseSource.UnregisterSSEClient(extCh)
	}
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()
	for _, ev := range replay {
		_, _ = fmt.Fprintf(w, "id: %s\n", ev.id)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", ev.payload)
	}
	if len(replay) > 0 {
		flusher.Flush()
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %s\n", ev.id)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev.payload)
			flusher.Flush()
		case ev, ok := <-extCh:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
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
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: payload})
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

func (s *Server) subscribeNotifications(ch chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notificationSubscribers[ch] = struct{}{}
}

func (s *Server) unsubscribeNotifications(ch chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.notificationSubscribers, ch)
}

func (s *Server) subscribeSSE(ch chan notificationEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sseSubscribers[ch] = struct{}{}
}

func (s *Server) unsubscribeSSE(ch chan notificationEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sseSubscribers, ch)
}

func (s *Server) emitNotification(method string, params any) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return
	}
	s.mu.Lock()
	s.nextNotificationID++
	ev := notificationEvent{
		id:      strconv.FormatUint(s.nextNotificationID, 10),
		payload: append([]byte(nil), payload...),
	}
	if s.notificationReplayLimit > 0 {
		s.notificationReplay = append(s.notificationReplay, ev)
		if len(s.notificationReplay) > s.notificationReplayLimit {
			s.notificationReplay = s.notificationReplay[len(s.notificationReplay)-s.notificationReplayLimit:]
		}
	}
	subs := make([]chan []byte, 0, len(s.notificationSubscribers))
	for ch := range s.notificationSubscribers {
		subs = append(subs, ch)
	}
	sseSubs := make([]chan notificationEvent, 0, len(s.sseSubscribers))
	for ch := range s.sseSubscribers {
		sseSubs = append(sseSubs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- payload:
		default:
		}
	}
	for _, ch := range sseSubs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Server) replayAfter(lastEventID string) ([]notificationEvent, error) {
	if lastEventID == "" {
		return nil, nil
	}
	if _, err := strconv.ParseUint(lastEventID, 10, 64); err != nil {
		return nil, errInvalidLastEventID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.notificationReplay) == 0 {
		return nil, errUnknownLastEventID
	}
	idx := -1
	for i := range s.notificationReplay {
		if s.notificationReplay[i].id == lastEventID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, errUnknownLastEventID
	}
	replay := make([]notificationEvent, len(s.notificationReplay[idx+1:]))
	copy(replay, s.notificationReplay[idx+1:])
	return replay, nil
}

func (s *Server) emitLogMessage(level string, data any) {
	if !s.shouldEmitLog(level) {
		return
	}
	s.emitNotification("notifications/message", logMessageParams{Level: level, Logger: "overcast", Data: data})
}

func (s *Server) emitCancelled(requestID any, reason string) {
	params := map[string]any{"requestId": requestID}
	if reason != "" {
		params["reason"] = reason
	}
	s.emitNotification("notifications/cancelled", params)
}

func (s *Server) shouldEmitLog(level string) bool {
	s.mu.RLock()
	threshold := s.logLevel
	s.mu.RUnlock()
	return loggingLevelRank(level) <= loggingLevelRank(threshold)
}

// emitProgress sends a notifications/progress notification for the given raw progress token.
func (s *Server) emitProgress(rawToken any, progress, total float64) {
	s.emitNotification("notifications/progress", map[string]any{
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
	s.mu.RLock()
	_, subscribed := s.resourceSubscriptions[uri]
	s.mu.RUnlock()
	if !subscribed {
		return
	}
	s.emitNotification("notifications/resources/updated", map[string]any{"uri": uri})
}

// emitResourceListChanged notifies clients that the resource list has changed.
func (s *Server) emitResourceListChanged() {
	s.emitNotification("notifications/resources/list_changed", map[string]any{})
}

// emitPromptsListChanged notifies clients that the prompts list has changed.
func (s *Server) emitPromptsListChanged() {
	s.emitNotification("notifications/prompts/list_changed", map[string]any{})
}

// emitToolsListChanged notifies clients that the tools list has changed.
func (s *Server) emitToolsListChanged() {
	s.emitNotification("notifications/tools/list_changed", map[string]any{})
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

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	return "mcp-" + hex.EncodeToString(b[:])
}

// ServeStdio runs the MCP server over a newline-delimited JSON stdio transport.
// This is compatible with the stdio transport defined in MCP spec §1.3.1.
// It exits cleanly when the input stream closes (EOF) or ctx is cancelled.
// Notifications (id-less messages) are processed synchronously so that lifecycle
// state (e.g. notifications/initialized → ready) is visible to subsequent requests.
// Requests are dispatched concurrently to allow parallel tool calls.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	var writeMu sync.Mutex
	var workers sync.WaitGroup
	var notificationWorkers sync.WaitGroup
	handler := s.RootHandler()
	notificationCh := make(chan []byte, 256)
	s.subscribeNotifications(notificationCh)
	defer func() {
		workers.Wait()
		s.unsubscribeNotifications(notificationCh)
		close(notificationCh)
		notificationWorkers.Wait()
	}()
	notificationWorkers.Add(1)
	go func() {
		defer notificationWorkers.Done()
		for {
			select {
			case payload, ok := <-notificationCh:
				if !ok {
					return
				}
				writeMu.Lock()
				_, _ = fmt.Fprintln(out, string(payload))
				writeMu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	dispatch := func(msg []byte) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(msg))
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

		// Detect messages that must run synchronously to preserve lifecycle ordering.
		// Per spec §1.2, the client sends initialize, waits for the result, then
		// sends notifications/initialized before any operations.  Any notification
		// (no "id") and the initialize request are processed inline so that
		// initDone and ready are visible to concurrently-dispatched requests that
		// follow on the stream.
		var peek struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		isSync := false
		if jsonErr := json.Unmarshal(msg, &peek); jsonErr == nil {
			isSync = peek.ID == nil || peek.Method == "initialize"
		}
		if isSync {
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

// SetNotificationReplayLimit sets the in-memory notification replay history
// size. A value of 0 disables replay retention entirely.
func (s *Server) SetNotificationReplayLimit(limit int) {
	if limit < 0 {
		limit = 0
	}
	s.mu.Lock()
	s.notificationReplayLimit = limit
	if limit == 0 {
		s.notificationReplay = nil
	} else if len(s.notificationReplay) > limit {
		s.notificationReplay = s.notificationReplay[len(s.notificationReplay)-limit:]
	}
	s.mu.Unlock()
}

// SetBearerAuthToken enables bearer-token HTTP auth checks when token is
// non-empty. Empty token disables auth checks.
func (s *Server) SetBearerAuthToken(token string) {
	s.mu.Lock()
	s.authBearerToken = strings.TrimSpace(token)
	s.mu.Unlock()
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

func (s *Server) registerInFlight(requestIDKey string, rawRequestID any, method, progressToken string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight[requestIDKey] = &inFlightRequest{rawRequestID: rawRequestID, method: method, cancel: cancel, progressToken: progressToken}
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
	if request.method == "initialize" {
		s.mu.Unlock()
		return
	}
	if request.cancelled {
		s.mu.Unlock()
		return
	}
	request.cancelled = true
	requestID = request.rawRequestID
	request.cancel()
	s.mu.Unlock()
	s.emitCancelled(requestID, reason)
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
