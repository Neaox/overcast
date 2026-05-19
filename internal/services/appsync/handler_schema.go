package appsync

// handler_schema.go — schema management and API key handlers.
//
// Implemented:
//   - StartSchemaCreation      POST /v1/apis/{apiId}/schemacreation
//   - GetSchemaCreationStatus  GET  /v1/apis/{apiId}/schemacreation
//   - GetIntrospectionSchema   GET  /v1/apis/{apiId}/schema
//   - CreateApiKey             POST /v1/apis/{apiId}/apikeys
//   - ListApiKeys              GET  /v1/apis/{apiId}/apikeys
//   - UpdateApiKey             POST /v1/apis/{apiId}/apikeys/{keyId}
//   - DeleteApiKey             DELETE /v1/apis/{apiId}/apikeys/{keyId}

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	gqlast "github.com/vektah/gqlparser/v2/ast"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── Schema ──────────────────────────────────────────────────────────────────

// StartSchemaCreation handles POST /v1/apis/{apiId}/schemacreation.
//
// In real AWS this is async — the schema is validated and compiled in the
// background. Our emulator validates, parses, and stores the SDL immediately.
func (h *Handler) StartSchemaCreation(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	var req struct {
		Definition string `json:"definition"` // base64-encoded SDL
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	sdl, err := base64.StdEncoding.DecodeString(req.Definition)
	if err != nil {
		protocol.WriteJSONError(w, r, badRequestError("Invalid base64 in definition"))
		return
	}

	// Validate and parse the SDL.
	parsed, parseErr := h.sp.Parse(sdl)
	if parseErr != nil {
		protocol.WriteJSONError(w, r, badRequestError(parseErr.Error()))
		return
	}

	// Cache the parsed schema for execution.
	h.sp.Put(apiID, parsed)

	schema := &Schema{
		ApiId:      apiID,
		Definition: sdl,
		Status:     "ACTIVE",
	}
	if storeErr := h.store.PutSchema(r.Context(), schema); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	h.publish(r, events.AppSyncSchemaUpdated, events.ResourcePayload{Name: apiID})

	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ACTIVE"})
}

// GetSchemaCreationStatus handles GET /v1/apis/{apiId}/schemacreation.
func (h *Handler) GetSchemaCreationStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	schema, err := h.store.GetSchema(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	status := "NOT_APPLICABLE"
	details := ""
	if schema != nil {
		status = schema.Status
		details = "Schema created successfully."
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"status":  status,
		"details": details,
	})
}

// GetIntrospectionSchema handles GET /v1/apis/{apiId}/schema.
// Returns the schema in SDL or JSON introspection format.
// The ?format query parameter selects the output format (SDL | JSON).
// Both formats are returned as base64-encoded bytes in the "schema" key.
func (h *Handler) GetIntrospectionSchema(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	schema, err := h.store.GetSchema(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if schema == nil {
		protocol.WriteJSONError(w, r, notFoundError("Schema not found for API "+apiID))
		return
	}

	format := r.URL.Query().Get("format")

	var payload []byte
	if format == "JSON" {
		// Return the standard GraphQL introspection JSON result.
		parsed := h.sp.Get(apiID)
		if parsed == nil {
			reparsed, parseErr := h.sp.Parse(schema.Definition)
			if parseErr != nil {
				protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, parseErr))
				return
			}
			h.sp.Put(apiID, reparsed)
			parsed = reparsed
		}
		astSchema, castOK := parsed.Opaque.(*gqlast.Schema)
		if !castOK {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
		introspJSON, introspErr := buildFullSchemaIntrospection(astSchema)
		if introspErr != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, introspErr))
			return
		}
		payload = introspJSON
	} else {
		// SDL format (default) — return the raw schema definition.
		payload = schema.Definition
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"schema": base64.StdEncoding.EncodeToString(payload),
	})
}

// ─── API Keys ────────────────────────────────────────────────────────────────

// generateAPIKeyID creates a key ID in the AWS da2-xxxx format.
func generateAPIKeyID() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	// AWS API key IDs are "da2-" followed by 26 alphanumeric chars (lowercase).
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	id := make([]byte, 26)
	for i := range id {
		id[i] = alphabet[int(b[i%len(b)])%len(alphabet)]
	}
	return "da2-" + string(id)
}

// CreateApiKey handles POST /v1/apis/{apiId}/apikeys.
func (h *Handler) CreateApiKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	var req struct {
		Description string `json:"description"`
		Expires     int64  `json:"expires"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Default expiry: 7 days from now.
	if req.Expires == 0 {
		req.Expires = h.clk.Now().Add(7 * 24 * time.Hour).Unix()
	}

	key := &ApiKey{
		Id:          generateAPIKeyID(),
		Description: req.Description,
		Expires:     req.Expires,
		Deletes:     req.Expires + 60*24*3600, // 60 days after expiry
	}

	if err := h.store.PutApiKey(r.Context(), apiID, key); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusCreated, map[string]any{"apiKey": key})
}

// ListApiKeys handles GET /v1/apis/{apiId}/apikeys.
func (h *Handler) ListApiKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	keys, err := h.store.ListApiKeys(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiKeys": keys})
}

// UpdateApiKey handles POST /v1/apis/{apiId}/apikeys/{keyId}.
func (h *Handler) UpdateApiKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	keyID := chi.URLParam(r, "keyId")

	existing, err := h.store.GetApiKey(r.Context(), apiID, keyID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError(fmt.Sprintf("API key %s not found.", keyID)))
		return
	}

	var req struct {
		Description string `json:"description"`
		Expires     int64  `json:"expires"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Expires != 0 {
		existing.Expires = req.Expires
		existing.Deletes = req.Expires + 60*24*3600
	}

	if storeErr := h.store.PutApiKey(r.Context(), apiID, existing); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiKey": existing})
}

// DeleteApiKey handles DELETE /v1/apis/{apiId}/apikeys/{keyId}.
func (h *Handler) DeleteApiKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	keyID := chi.URLParam(r, "keyId")

	if err := h.store.DeleteApiKey(r.Context(), apiID, keyID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}
