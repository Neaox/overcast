package appsync

// handler_events.go — Events API management handlers.
//
// The Events API is a separate AppSync feature for pub/sub messaging
// over WebSockets using channel namespaces (not GraphQL).
//
// Implemented:
//   - CreateApi              POST   /v2/apis
//   - GetApi                 GET    /v2/apis/{apiId}
//   - ListApis               GET    /v2/apis
//   - UpdateApi              POST   /v2/apis/{apiId}
//   - DeleteApi              DELETE /v2/apis/{apiId}
//   - CreateChannelNamespace POST   /v2/apis/{apiId}/channelNamespaces
//   - GetChannelNamespace    GET    /v2/apis/{apiId}/channelNamespaces/{name}
//   - ListChannelNamespaces  GET    /v2/apis/{apiId}/channelNamespaces
//   - UpdateChannelNamespace POST   /v2/apis/{apiId}/channelNamespaces/{name}
//   - DeleteChannelNamespace DELETE /v2/apis/{apiId}/channelNamespaces/{name}

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── CreateApi ───────────────────────────────────────────────────────────────

// CreateApi handles POST /v2/apis.
func (h *Handler) CreateApi(w http.ResponseWriter, r *http.Request) {
	var api EventApi
	if !serviceutil.DecodeJSON(w, r, &api) {
		return
	}

	if api.Name == "" {
		protocol.WriteJSONError(w, r, badRequestError("name is required"))
		return
	}

	apiID := uuid.NewString()
	api.ApiId = apiID
	api.ApiArn = protocol.ARN(h.region(r), h.cfg.AccountID, "appsync", "apis/"+apiID)
	api.Created = h.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	// Generate synthetic DNS entries.
	api.Dns = map[string]string{
		"HTTP":     fmt.Sprintf("%s.appsync-api.%s.amazonaws.com", apiID, h.region(r)),
		"REALTIME": fmt.Sprintf("%s.appsync-realtime-api.%s.amazonaws.com", apiID, h.region(r)),
	}

	if err := h.store.PutEventApi(r.Context(), &api); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"api": &api})
}

// ─── GetApi ──────────────────────────────────────────────────────────────────

// GetApi handles GET /v2/apis/{apiId}.
func (h *Handler) GetApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	api, err := h.store.GetEventApi(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if api == nil {
		protocol.WriteJSONError(w, r, notFoundError("Api "+apiID+" not found."))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"api": api})
}

// ─── ListApis ────────────────────────────────────────────────────────────────

// ListApis handles GET /v2/apis.
func (h *Handler) ListApis(w http.ResponseWriter, r *http.Request) {
	apis, err := h.store.ListEventApis(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeListJSON(w, r, "apis", apis)
}

// ─── UpdateApi ───────────────────────────────────────────────────────────────

// UpdateApi handles POST /v2/apis/{apiId}.
func (h *Handler) UpdateApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	existing, err := h.store.GetEventApi(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError("Api "+apiID+" not found."))
		return
	}

	var update EventApi
	if !serviceutil.DecodeJSON(w, r, &update) {
		return
	}

	// Preserve server-generated fields.
	update.ApiId = existing.ApiId
	update.ApiArn = existing.ApiArn
	update.Created = existing.Created
	update.Dns = existing.Dns
	if update.Tags == nil {
		update.Tags = existing.Tags
	}
	if update.Name == "" {
		update.Name = existing.Name
	}
	if update.EventConfig == nil {
		update.EventConfig = existing.EventConfig
	}

	if storeErr := h.store.PutEventApi(r.Context(), &update); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"api": &update})
}

// ─── DeleteApi ───────────────────────────────────────────────────────────────

// DeleteApi handles DELETE /v2/apis/{apiId}.
func (h *Handler) DeleteApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	existing, err := h.store.GetEventApi(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError("Api "+apiID+" not found."))
		return
	}

	if err := h.store.DeleteEventApi(r.Context(), apiID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── CreateChannelNamespace ──────────────────────────────────────────────────

// CreateChannelNamespace handles POST /v2/apis/{apiId}/channelNamespaces.
func (h *Handler) CreateChannelNamespace(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	// Verify the Event API exists.
	api, err := h.store.GetEventApi(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if api == nil {
		protocol.WriteJSONError(w, r, notFoundError("Api "+apiID+" not found."))
		return
	}

	var ns ChannelNamespace
	if !serviceutil.DecodeJSON(w, r, &ns) {
		return
	}

	if ns.Name == "" {
		protocol.WriteJSONError(w, r, badRequestError("name is required"))
		return
	}

	// Check for duplicates.
	existing, _ := h.store.GetChannelNamespace(r.Context(), apiID, ns.Name)
	if existing != nil {
		protocol.WriteJSONError(w, r, conflictError("Channel namespace "+ns.Name+" already exists."))
		return
	}

	now := h.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	ns.ApiId = apiID
	ns.ChannelNamespaceArn = protocol.ARN(h.region(r), h.cfg.AccountID, "appsync", "apis/"+apiID+"/channelNamespace/"+ns.Name)
	ns.Created = now
	ns.LastModified = now

	if storeErr := h.store.PutChannelNamespace(r.Context(), apiID, &ns); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"channelNamespace": &ns})
}

// ─── GetChannelNamespace ─────────────────────────────────────────────────────

// GetChannelNamespace handles GET /v2/apis/{apiId}/channelNamespaces/{name}.
func (h *Handler) GetChannelNamespace(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	name := chi.URLParam(r, "name")

	ns, err := h.store.GetChannelNamespace(r.Context(), apiID, name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if ns == nil {
		protocol.WriteJSONError(w, r, notFoundError("Channel namespace "+name+" not found."))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"channelNamespace": ns})
}

// ─── ListChannelNamespaces ───────────────────────────────────────────────────

// ListChannelNamespaces handles GET /v2/apis/{apiId}/channelNamespaces.
func (h *Handler) ListChannelNamespaces(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	namespaces, err := h.store.ListChannelNamespaces(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeListJSON(w, r, "channelNamespaces", namespaces)
}

// ─── UpdateChannelNamespace ──────────────────────────────────────────────────

// UpdateChannelNamespace handles POST /v2/apis/{apiId}/channelNamespaces/{name}.
func (h *Handler) UpdateChannelNamespace(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	name := chi.URLParam(r, "name")

	existing, err := h.store.GetChannelNamespace(r.Context(), apiID, name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError("Channel namespace "+name+" not found."))
		return
	}

	var update ChannelNamespace
	if !serviceutil.DecodeJSON(w, r, &update) {
		return
	}

	// Preserve server-generated fields.
	update.ApiId = existing.ApiId
	update.Name = existing.Name
	update.ChannelNamespaceArn = existing.ChannelNamespaceArn
	update.Created = existing.Created
	update.LastModified = h.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if update.Tags == nil {
		update.Tags = existing.Tags
	}
	if update.CodeHandlers == "" {
		update.CodeHandlers = existing.CodeHandlers
	}
	if update.PublishAuthModes == nil {
		update.PublishAuthModes = existing.PublishAuthModes
	}
	if update.SubscribeAuthModes == nil {
		update.SubscribeAuthModes = existing.SubscribeAuthModes
	}
	if update.HandlerConfigs == nil {
		update.HandlerConfigs = existing.HandlerConfigs
	}

	if storeErr := h.store.PutChannelNamespace(r.Context(), apiID, &update); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"channelNamespace": &update})
}

// ─── DeleteChannelNamespace ──────────────────────────────────────────────────

// DeleteChannelNamespace handles DELETE /v2/apis/{apiId}/channelNamespaces/{name}.
func (h *Handler) DeleteChannelNamespace(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	name := chi.URLParam(r, "name")

	existing, err := h.store.GetChannelNamespace(r.Context(), apiID, name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError("Channel namespace "+name+" not found."))
		return
	}

	if err := h.store.DeleteChannelNamespace(r.Context(), apiID, name); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}
