package appsync

// handler_config.go — implemented config-level CRUD handlers.
//
// Implemented:
//   - PutGraphqlApiEnvironmentVariables  PUT    /v1/apis/{apiId}/environmentVariables
//   - GetGraphqlApiEnvironmentVariables  GET    /v1/apis/{apiId}/environmentVariables
//   - CreateDomainName                   POST   /v1/domainnames
//   - GetDomainName                      GET    /v1/domainnames/{domainName}
//   - ListDomainNames                    GET    /v1/domainnames
//   - UpdateDomainName                   POST   /v1/domainnames/{domainName}
//   - DeleteDomainName                   DELETE /v1/domainnames/{domainName}
//   - AssociateApi                       POST   /v1/domainnames/{domainName}/apiassociation
//   - GetApiAssociation                  GET    /v1/domainnames/{domainName}/apiassociation
//   - DisassociateApi                    DELETE /v1/domainnames/{domainName}/apiassociation
//   - CreateApiCache                     POST   /v1/apis/{apiId}/ApiCaches
//   - GetApiCache                        GET    /v1/apis/{apiId}/ApiCaches
//   - UpdateApiCache                     POST   /v1/apis/{apiId}/ApiCaches/update
//   - DeleteApiCache                     DELETE /v1/apis/{apiId}/ApiCaches
//   - FlushApiCache                      DELETE /v1/apis/{apiId}/ApiCaches/flush

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── Environment Variables ───────────────────────────────────────────────────

// PutGraphqlApiEnvironmentVariables handles PUT /v1/apis/{apiId}/environmentVariables.
func (h *Handler) PutGraphqlApiEnvironmentVariables(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	var req struct {
		EnvironmentVariables map[string]string `json:"environmentVariables"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.EnvironmentVariables == nil {
		req.EnvironmentVariables = make(map[string]string)
	}

	// Validate constraints: max 50 vars, key 2-64 chars, value ≤512 chars.
	if len(req.EnvironmentVariables) > 50 {
		protocol.WriteJSONError(w, r, badRequestError("Environment variables cannot exceed 50 entries."))
		return
	}
	for k, v := range req.EnvironmentVariables {
		if len(k) < 2 || len(k) > 64 {
			protocol.WriteJSONError(w, r, badRequestError(
				fmt.Sprintf("Environment variable key %q must be between 2 and 64 characters.", k)))
			return
		}
		if len(v) > 512 {
			protocol.WriteJSONError(w, r, badRequestError(
				fmt.Sprintf("Environment variable value for key %q exceeds 512 characters.", k)))
			return
		}
	}

	ev := &EnvironmentVariables{
		ApiId:                apiID,
		EnvironmentVariables: req.EnvironmentVariables,
	}
	if err := h.store.PutEnvironmentVariables(r.Context(), apiID, ev); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"environmentVariables": ev.EnvironmentVariables,
	})
}

// GetGraphqlApiEnvironmentVariables handles GET /v1/apis/{apiId}/environmentVariables.
func (h *Handler) GetGraphqlApiEnvironmentVariables(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	ev, err := h.store.GetEnvironmentVariables(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	vars := make(map[string]string)
	if ev != nil {
		vars = ev.EnvironmentVariables
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"environmentVariables": vars,
	})
}

// ─── Domain Names ────────────────────────────────────────────────────────────

// generateDomainHex creates a short random hex string for synthetic domain names.
func generateDomainHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateDomainName handles POST /v1/domainnames.
func (h *Handler) CreateDomainName(w http.ResponseWriter, r *http.Request) {
	var req DomainNameConfig
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.DomainName == "" {
		protocol.WriteJSONError(w, r, badRequestError("domainName is required."))
		return
	}

	// Check for duplicate.
	existing, err := h.store.GetDomainName(r.Context(), req.DomainName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, conflictError(
			fmt.Sprintf("Domain name %q already exists.", req.DomainName)))
		return
	}

	// Generate synthetic appsyncDomainName and hostedZoneId.
	req.AppsyncDomainName = fmt.Sprintf("d-%s.appsync-api.%s.amazonaws.com",
		generateDomainHex(7), h.cfg.Region)
	req.HostedZoneId = "Z" + generateDomainHex(10)

	if storeErr := h.store.PutDomainName(r.Context(), &req); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"domainNameConfig": &req})
}

// GetDomainName handles GET /v1/domainnames/{domainName}.
func (h *Handler) GetDomainName(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	dn, err := h.store.GetDomainName(r.Context(), domainName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if dn == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("Domain name %q not found.", domainName)))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"domainNameConfig": dn})
}

// ListDomainNames handles GET /v1/domainnames.
func (h *Handler) ListDomainNames(w http.ResponseWriter, r *http.Request) {
	domains, err := h.store.ListDomainNames(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeListJSON(w, r, "domainNameConfigs", domains)
}

// UpdateDomainName handles POST /v1/domainnames/{domainName}.
func (h *Handler) UpdateDomainName(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	existing, err := h.store.GetDomainName(r.Context(), domainName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("Domain name %q not found.", domainName)))
		return
	}

	var req struct {
		Description string `json:"description"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Description != "" {
		existing.Description = req.Description
	}

	if storeErr := h.store.PutDomainName(r.Context(), existing); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"domainNameConfig": existing})
}

// DeleteDomainName handles DELETE /v1/domainnames/{domainName}.
func (h *Handler) DeleteDomainName(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	// Also delete any associated API association.
	_ = h.store.DeleteApiAssociation(r.Context(), domainName)

	if err := h.store.DeleteDomainName(r.Context(), domainName); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── API Associations ────────────────────────────────────────────────────────

// AssociateApi handles POST /v1/domainnames/{domainName}/apiassociation.
func (h *Handler) AssociateApi(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	// Validate domain exists.
	dn, err := h.store.GetDomainName(r.Context(), domainName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if dn == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("Domain name %q not found.", domainName)))
		return
	}

	var req struct {
		ApiId string `json:"apiId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Validate API exists.
	api, apiErr := h.store.GetAPI(r.Context(), req.ApiId)
	if apiErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, apiErr))
		return
	}
	if api == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("GraphQL API %q not found.", req.ApiId)))
		return
	}

	assoc := &ApiAssociation{
		DomainName:        domainName,
		ApiId:             req.ApiId,
		AssociationStatus: "SUCCESS",
	}

	if storeErr := h.store.PutApiAssociation(r.Context(), assoc); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiAssociation": assoc})
}

// GetApiAssociation handles GET /v1/domainnames/{domainName}/apiassociation.
func (h *Handler) GetApiAssociation(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	assoc, err := h.store.GetApiAssociation(r.Context(), domainName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if assoc == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("API association for domain %q not found.", domainName)))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiAssociation": assoc})
}

// DisassociateApi handles DELETE /v1/domainnames/{domainName}/apiassociation.
func (h *Handler) DisassociateApi(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	if err := h.store.DeleteApiAssociation(r.Context(), domainName); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── API Cache ───────────────────────────────────────────────────────────────

// CreateApiCache handles POST /v1/apis/{apiId}/ApiCaches.
func (h *Handler) CreateApiCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	// Check for existing cache (one per API).
	existing, err := h.store.GetApiCache(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, conflictError(
			fmt.Sprintf("API cache already exists for API %s.", apiID)))
		return
	}

	var cache ApiCacheConfig
	if !serviceutil.DecodeJSON(w, r, &cache) {
		return
	}

	cache.ApiId = apiID
	cache.Status = "AVAILABLE"

	if storeErr := h.store.PutApiCache(r.Context(), apiID, &cache); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiCache": &cache})
}

// GetApiCache handles GET /v1/apis/{apiId}/ApiCaches.
func (h *Handler) GetApiCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	cache, err := h.store.GetApiCache(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if cache == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("API cache not found for API %s.", apiID)))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiCache": cache})
}

// UpdateApiCache handles POST /v1/apis/{apiId}/ApiCaches/update.
func (h *Handler) UpdateApiCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	existing, err := h.store.GetApiCache(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("API cache not found for API %s.", apiID)))
		return
	}

	var update ApiCacheConfig
	if !serviceutil.DecodeJSON(w, r, &update) {
		return
	}

	// Merge fields.
	if update.Type != "" {
		existing.Type = update.Type
	}
	if update.ApiCachingBehavior != "" {
		existing.ApiCachingBehavior = update.ApiCachingBehavior
	}
	if update.Ttl != 0 {
		existing.Ttl = update.Ttl
	}
	if update.HealthMetricsConfig != "" {
		existing.HealthMetricsConfig = update.HealthMetricsConfig
	}
	// Boolean fields always apply from update.
	existing.TransitEncryptionEnabled = update.TransitEncryptionEnabled
	existing.AtRestEncryptionEnabled = update.AtRestEncryptionEnabled

	if storeErr := h.store.PutApiCache(r.Context(), apiID, existing); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"apiCache": existing})
}

// DeleteApiCache handles DELETE /v1/apis/{apiId}/ApiCaches.
func (h *Handler) DeleteApiCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	if err := h.store.DeleteApiCache(r.Context(), apiID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// FlushApiCache handles DELETE /v1/apis/{apiId}/ApiCaches/flush.
// This is a no-op for the emulator — we don't actually cache resolver results.
func (h *Handler) FlushApiCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}
