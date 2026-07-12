package appsync

// handler.go — implemented GraphQL API management handlers.
//
// Implemented:
//   - CreateGraphqlApi   POST   /v1/apis
//   - GetGraphqlApi      GET    /v1/apis/{apiId}
//   - ListGraphqlApis    GET    /v1/apis
//   - UpdateGraphqlApi   POST   /v1/apis/{apiId}
//   - DeleteGraphqlApi   DELETE /v1/apis/{apiId}
//   - TagResource        POST   /v1/tags/{resourceArn}
//   - UntagResource      DELETE /v1/tags/{resourceArn}
//   - ListTagsForResource GET   /v1/tags/{resourceArn}

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Handler holds AppSync handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *Store
	log   *serviceutil.ServiceLogger
	bus   *events.Bus
	clk   clock.Clock

	typedOp map[string]op.Operation

	// schemaParser validates and parses uploaded SDL schemas.
	sp *schemaParser

	// invoker invokes Lambda functions synchronously (for AWS_LAMBDA data sources).
	invoker events.FunctionSyncInvoker

	// dynamoInvoker invokes DynamoDB operations (for AMAZON_DYNAMODB data sources).
	dynamoInvoker events.DynamoDBInvoker

	// Execution engine — optional, nil until implementations are wired.

	// vtlEvaluator evaluates VTL mapping templates.
	vtlEvaluator MappingTemplateEvaluator

	// jsEvaluator evaluates APPSYNC_JS resolver code.
	jsEvaluator CodeEvaluator

	// subscriptions manages active WebSocket subscriptions.
	subscriptions *subscriptionManager
}

func newHandler(cfg *config.Config, store *Store, log *serviceutil.ServiceLogger, clk clock.Clock, sp *schemaParser) *Handler {
	h := &Handler{
		cfg:           cfg,
		store:         store,
		log:           log,
		clk:           clk,
		sp:            sp,
		jsEvaluator:   newJSEvaluator(clk),
		vtlEvaluator:  newVTLEvaluator(clk),
		subscriptions: newSubscriptionManager(clk, log),
	}
	h.typedOp = h.typedOps()
	return h
}

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Source: serviceName, Payload: payload})
	}
}

// region returns the request's region (from SigV4 / X-Overcast-Region header),
// falling back to the configured default. Used for ARN minting so that ARNs
// returned to clients carry the deployment region they actually called.
func (h *Handler) region(r *http.Request) string {
	return middleware.RegionFromContext(r.Context(), h.cfg.Region)
}

// regionCtx is the context-only variant of region for code paths that don't
// have an *http.Request in scope (helpers called from handlers).
func (h *Handler) regionCtx(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, h.cfg.Region)
}

// writeJSON writes a JSON response with the correct AppSync content type.
// AppSync uses application/json (not application/x-amz-json-1.0).
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	reqID := protocol.RequestIDFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-amzn-requestid", reqID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func validAppSyncName(name string) bool {
	if name == "" || len(name) > 65536 {
		return false
	}
	first := name[0]
	if first != '_' && (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func paginateList[T any](r *http.Request, items []T, maxDefault int) ([]T, string, *protocol.AWSError) {
	q := r.URL.Query()
	start := 0
	if token := q.Get("nextToken"); token != "" {
		parsed, err := strconv.Atoi(token)
		if err != nil || parsed < 0 || parsed > len(items) {
			return nil, "", badRequestError("nextToken is invalid.")
		}
		start = parsed
	}

	max := maxDefault
	if raw := q.Get("maxResults"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 25 {
			return nil, "", badRequestError("maxResults must be between 0 and 25.")
		}
		max = parsed
	}
	if max < 0 || max > len(items) {
		max = len(items)
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + max
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[start:end], next, nil
}

func writeListJSON[T any](w http.ResponseWriter, r *http.Request, field string, items []T) {
	page, next, aerr := paginateList(r, items, len(items))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	body := map[string]any{field: page}
	if next != "" {
		body["nextToken"] = next
	}
	writeJSON(w, r, http.StatusOK, body)
}

// requireAPI loads an API by ID from the URL, writing 404 on miss.
func (h *Handler) requireAPI(w http.ResponseWriter, r *http.Request) (*GraphqlAPI, bool) {
	apiID := chi.URLParam(r, "apiId")
	api, err := h.store.GetAPI(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return nil, false
	}
	if api == nil {
		protocol.WriteJSONError(w, r, notFoundError("GraphQL API "+apiID+" not found."))
		return nil, false
	}
	return api, true
}

// ─── CreateGraphqlApi ────────────────────────────────────────────────────────

// CreateGraphqlApi handles POST /v1/apis.
func (h *Handler) CreateGraphqlApi(w http.ResponseWriter, r *http.Request) {
	var api GraphqlAPI
	if !serviceutil.DecodeJSON(w, r, &api) {
		return
	}
	if api.Name == "" {
		protocol.WriteJSONError(w, r, badRequestError("name is required."))
		return
	}
	if !containsString([]string{"API_KEY", "AWS_IAM", "AMAZON_COGNITO_USER_POOLS", "OPENID_CONNECT", "AWS_LAMBDA"}, api.AuthenticationType) {
		protocol.WriteJSONError(w, r, badRequestError("authenticationType is invalid or missing."))
		return
	}
	if api.ApiType != "" && !containsString([]string{"GRAPHQL", "MERGED"}, api.ApiType) {
		protocol.WriteJSONError(w, r, badRequestError("apiType is invalid."))
		return
	}
	if api.Visibility != "" && !containsString([]string{"GLOBAL", "PRIVATE"}, api.Visibility) {
		protocol.WriteJSONError(w, r, badRequestError("visibility is invalid."))
		return
	}
	if api.IntrospectionConfig != "" && !containsString([]string{"ENABLED", "DISABLED"}, api.IntrospectionConfig) {
		protocol.WriteJSONError(w, r, badRequestError("introspectionConfig is invalid."))
		return
	}
	if api.QueryDepthLimit < 0 || api.QueryDepthLimit > 75 {
		protocol.WriteJSONError(w, r, badRequestError("queryDepthLimit must be between 0 and 75."))
		return
	}
	if api.ResolverCountLimit < 0 || api.ResolverCountLimit > 10000 {
		protocol.WriteJSONError(w, r, badRequestError("resolverCountLimit must be between 0 and 10000."))
		return
	}
	if len(api.OwnerContact) > 256 {
		protocol.WriteJSONError(w, r, badRequestError("ownerContact must be 256 characters or fewer."))
		return
	}
	if len(api.Tags) > 50 {
		protocol.WriteJSONError(w, r, badRequestError("tags cannot exceed 50 entries."))
		return
	}
	for key, value := range api.Tags {
		if len(key) < 1 || len(key) > 128 {
			protocol.WriteJSONError(w, r, badRequestError("tag keys must be between 1 and 128 characters."))
			return
		}
		if strings.HasPrefix(key, "aws:") {
			protocol.WriteJSONError(w, r, badRequestError("tag keys must not start with aws:."))
			return
		}
		if len(value) > 256 {
			protocol.WriteJSONError(w, r, badRequestError("tag values must be 256 characters or fewer."))
			return
		}
	}

	apiID := uuid.NewString()
	api.ApiId = apiID
	api.ARN = protocol.ARN(h.region(r), h.cfg.AccountID, "appsync", "apis/"+apiID)
	api.Owner = h.cfg.AccountID

	if api.ApiType == "" {
		api.ApiType = "GRAPHQL"
	}
	if api.Visibility == "" {
		api.Visibility = "GLOBAL"
	}
	if api.IntrospectionConfig == "" {
		api.IntrospectionConfig = "ENABLED"
	}

	// Generate synthetic URIs matching the real AWS format.
	api.Uris = map[string]string{
		"GRAPHQL":  fmt.Sprintf("https://%s.appsync-api.%s.amazonaws.com/graphql", apiID, h.region(r)),
		"REALTIME": fmt.Sprintf("wss://%s.appsync-realtime-api.%s.amazonaws.com/graphql", apiID, h.region(r)),
	}
	api.Dns = map[string]string{
		"GRAPHQL":  fmt.Sprintf("%s.appsync-api.%s.amazonaws.com", apiID, h.region(r)),
		"REALTIME": fmt.Sprintf("%s.appsync-realtime-api.%s.amazonaws.com", apiID, h.region(r)),
	}

	if err := h.store.PutAPI(r.Context(), &api); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	// Real AWS auto-creates a default API key when auth type is API_KEY.
	if api.AuthenticationType == "API_KEY" {
		now := h.clk.Now()
		key := &ApiKey{
			Id:      generateAPIKeyID(),
			Expires: now.Add(7 * 24 * time.Hour).Unix(),
			Deletes: now.Add(67 * 24 * time.Hour).Unix(),
		}
		if err := h.store.PutApiKey(r.Context(), apiID, key); err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
	}

	h.publish(r, events.AppSyncAPICreated, events.ResourcePayload{Name: api.Name})

	writeJSON(w, r, http.StatusOK, map[string]any{"graphqlApi": &api})
}

// ─── GetGraphqlApi ───────────────────────────────────────────────────────────

// GetGraphqlApi handles GET /v1/apis/{apiId}.
func (h *Handler) GetGraphqlApi(w http.ResponseWriter, r *http.Request) {
	api, ok := h.requireAPI(w, r)
	if !ok {
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"graphqlApi": api})
}

// ─── ListGraphqlApis ─────────────────────────────────────────────────────────

// ListGraphqlApis handles GET /v1/apis.
func (h *Handler) ListGraphqlApis(w http.ResponseWriter, r *http.Request) {
	apis, err := h.store.ListAPIs(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	apiType := r.URL.Query().Get("apiType")
	if apiType != "" && !containsString([]string{"GRAPHQL", "MERGED"}, apiType) {
		protocol.WriteJSONError(w, r, badRequestError("apiType is invalid."))
		return
	}
	owner := r.URL.Query().Get("owner")
	if owner != "" && !containsString([]string{"CURRENT_ACCOUNT", "OTHER_ACCOUNTS"}, owner) {
		protocol.WriteJSONError(w, r, badRequestError("owner is invalid."))
		return
	}
	filtered := make([]*GraphqlAPI, 0, len(apis))
	for _, api := range apis {
		if apiType != "" && api.ApiType != apiType {
			continue
		}
		if owner == "CURRENT_ACCOUNT" && api.Owner != h.cfg.AccountID {
			continue
		}
		if owner == "OTHER_ACCOUNTS" && api.Owner == h.cfg.AccountID {
			continue
		}
		filtered = append(filtered, api)
	}
	writeListJSON(w, r, "graphqlApis", filtered)
}

// ─── UpdateGraphqlApi ────────────────────────────────────────────────────────

// UpdateGraphqlApi handles POST /v1/apis/{apiId}.
func (h *Handler) UpdateGraphqlApi(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.requireAPI(w, r)
	if !ok {
		return
	}

	var update GraphqlAPI
	if !serviceutil.DecodeJSON(w, r, &update) {
		return
	}

	// Preserve server-generated fields.
	update.ApiId = existing.ApiId
	update.ARN = existing.ARN
	update.Owner = existing.Owner
	update.Uris = existing.Uris
	update.Dns = existing.Dns

	// Preserve defaults when not provided.
	if update.ApiType == "" {
		update.ApiType = existing.ApiType
	}
	if update.Visibility == "" {
		update.Visibility = existing.Visibility
	}
	if update.IntrospectionConfig == "" {
		update.IntrospectionConfig = existing.IntrospectionConfig
	}
	// Preserve tags if not provided in update.
	if update.Tags == nil {
		update.Tags = existing.Tags
	}

	if err := h.store.PutAPI(r.Context(), &update); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.AppSyncAPIUpdated, events.ResourcePayload{Name: update.Name, ARN: update.ARN})

	writeJSON(w, r, http.StatusOK, map[string]any{"graphqlApi": &update})
}

// ─── DeleteGraphqlApi ────────────────────────────────────────────────────────

// DeleteGraphqlApi handles DELETE /v1/apis/{apiId}.
func (h *Handler) DeleteGraphqlApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if err := h.store.DeleteAPIAndChildren(r.Context(), apiID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.AppSyncAPIDeleted, events.ResourcePayload{Name: apiID})

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── Tags ────────────────────────────────────────────────────────────────────

// TagResource handles POST /v1/tags/{resourceArn}.
func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	api, err := h.apiForARN(r, arn)
	if err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if api.Tags == nil {
		api.Tags = make(map[string]string, len(req.Tags))
	}
	for k, v := range req.Tags {
		api.Tags[k] = v
	}

	if storeErr := h.store.PutAPI(r.Context(), api); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// UntagResource handles DELETE /v1/tags/{resourceArn}.
func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	api, err := h.apiForARN(r, arn)
	if err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	tagKeys := r.URL.Query()["tagKeys"]
	for _, k := range tagKeys {
		delete(api.Tags, k)
	}

	if storeErr := h.store.PutAPI(r.Context(), api); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ListTagsForResource handles GET /v1/tags/{resourceArn}.
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	api, err := h.apiForARN(r, arn)
	if err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	tags := api.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"tags": tags})
}

// apiForARN extracts the API ID from a resource ARN and loads the API.
func (h *Handler) apiForARN(r *http.Request, arn string) (*GraphqlAPI, *protocol.AWSError) {
	// The SDK URL-encodes the ARN in the path; decode so lookup works.
	if decoded, err := url.PathUnescape(arn); err == nil {
		arn = decoded
	}
	// ARN format: arn:aws:appsync:<region>:<account>:apis/<apiId>
	parts := strings.SplitN(arn, "/", 2)
	if len(parts) < 2 {
		return nil, notFoundError("Resource not found: " + arn)
	}
	// parts might be ["arn:aws:appsync:...:apis", "<apiId>"] or just the tail
	apiID := parts[len(parts)-1]
	// Handle the full ARN: extract the last segment after "apis/"
	if idx := strings.LastIndex(arn, "apis/"); idx >= 0 {
		apiID = arn[idx+len("apis/"):]
	}

	api, err := h.store.GetAPI(r.Context(), apiID)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundError("Resource not found: " + arn)
	}
	return api, nil
}
