package appsync

// handler_merged.go — Merged API management handlers.
//
// Implemented:
//   - AssociateSourceGraphqlApi    POST   /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations
//   - GetSourceApiAssociation      GET    /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}
//   - ListSourceApiAssociations    GET    /v1/apis/{apiId}/sourceApiAssociations
//   - DisassociateSourceGraphqlApi DELETE /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}
//   - StartSchemaMerge             POST   /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}/merge
//   - AssociateMergedGraphqlApi    POST   /v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations
//   - DisassociateMergedGraphqlApi DELETE /v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations/{associationId}

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── AssociateSourceGraphqlApi ───────────────────────────────────────────────

// AssociateSourceGraphqlApi handles POST /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations.
// Associates a source API with a merged API (called from the merged API side).
func (h *Handler) AssociateSourceGraphqlApi(w http.ResponseWriter, r *http.Request) {
	mergedApiID := chi.URLParam(r, "mergedApiIdentifier")

	var req struct {
		SourceApiIdentifier        string          `json:"sourceApiIdentifier"`
		Description                string          `json:"description,omitempty"`
		SourceApiAssociationConfig json.RawMessage `json:"sourceApiAssociationConfig,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.SourceApiIdentifier == "" {
		protocol.WriteJSONError(w, r, badRequestError("sourceApiIdentifier is required"))
		return
	}

	assoc, err := h.createSourceAssociation(r, mergedApiID, req.SourceApiIdentifier, req.Description, req.SourceApiAssociationConfig)
	if err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"sourceApiAssociation": assoc})
}

// ─── AssociateMergedGraphqlApi ───────────────────────────────────────────────

// AssociateMergedGraphqlApi handles POST /v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations.
// Associates a merged API with a source API (called from the source API side).
func (h *Handler) AssociateMergedGraphqlApi(w http.ResponseWriter, r *http.Request) {
	sourceApiID := chi.URLParam(r, "sourceApiIdentifier")

	var req struct {
		MergedApiIdentifier        string          `json:"mergedApiIdentifier"`
		Description                string          `json:"description,omitempty"`
		SourceApiAssociationConfig json.RawMessage `json:"sourceApiAssociationConfig,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.MergedApiIdentifier == "" {
		protocol.WriteJSONError(w, r, badRequestError("mergedApiIdentifier is required"))
		return
	}

	assoc, err := h.createSourceAssociation(r, req.MergedApiIdentifier, sourceApiID, req.Description, req.SourceApiAssociationConfig)
	if err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"sourceApiAssociation": assoc})
}

// createSourceAssociation is the shared logic for both Associate operations.
func (h *Handler) createSourceAssociation(r *http.Request, mergedApiID, sourceApiID, description string, config json.RawMessage) (*SourceApiAssociation, *protocol.AWSError) {
	ctx := r.Context()

	// Validate merged API exists.
	mergedAPI, storeErr := h.store.GetAPI(ctx, mergedApiID)
	if storeErr != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, storeErr)
	}
	if mergedAPI == nil {
		return nil, notFoundError("Merged API " + mergedApiID + " not found.")
	}

	// Validate source API exists.
	sourceAPI, storeErr := h.store.GetAPI(ctx, sourceApiID)
	if storeErr != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, storeErr)
	}
	if sourceAPI == nil {
		return nil, notFoundError("Source API " + sourceApiID + " not found.")
	}

	assocID := uuid.NewString()
	assoc := &SourceApiAssociation{
		AssociationId:                    assocID,
		AssociationArn:                   protocol.ARN(h.region(r), h.cfg.AccountID, "appsync", "apis/"+mergedApiID+"/sourceApiAssociations/"+assocID),
		Description:                      description,
		SourceApiId:                      sourceApiID,
		SourceApiArn:                     sourceAPI.ARN,
		MergedApiId:                      mergedApiID,
		MergedApiArn:                     mergedAPI.ARN,
		SourceApiAssociationConfig:       config,
		SourceApiAssociationStatus:       "MERGE_SCHEDULED",
		SourceApiAssociationStatusDetail: "",
	}

	if storeErr := h.store.PutSourceApiAssociation(ctx, mergedApiID, assoc); storeErr != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, storeErr)
	}

	// Perform synchronous schema merge.
	h.performMerge(ctx, mergedApiID, assoc)

	return assoc, nil
}

// ─── GetSourceApiAssociation ─────────────────────────────────────────────────

// GetSourceApiAssociation handles GET /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}.
func (h *Handler) GetSourceApiAssociation(w http.ResponseWriter, r *http.Request) {
	mergedApiID := chi.URLParam(r, "mergedApiIdentifier")
	assocID := chi.URLParam(r, "associationId")

	assoc, err := h.store.GetSourceApiAssociation(r.Context(), mergedApiID, assocID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if assoc == nil {
		protocol.WriteJSONError(w, r, notFoundError("Source API association "+assocID+" not found."))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"sourceApiAssociation": assoc})
}

// ─── ListSourceApiAssociations ───────────────────────────────────────────────

// ListSourceApiAssociations handles GET /v1/apis/{apiId}/sourceApiAssociations.
func (h *Handler) ListSourceApiAssociations(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	assocs, err := h.store.ListSourceApiAssociations(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeListJSON(w, r, "sourceApiAssociationSummaries", assocs)
}

// ─── DisassociateSourceGraphqlApi ────────────────────────────────────────────

// DisassociateSourceGraphqlApi handles DELETE /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}.
func (h *Handler) DisassociateSourceGraphqlApi(w http.ResponseWriter, r *http.Request) {
	mergedApiID := chi.URLParam(r, "mergedApiIdentifier")
	assocID := chi.URLParam(r, "associationId")

	assoc, err := h.store.GetSourceApiAssociation(r.Context(), mergedApiID, assocID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if assoc == nil {
		protocol.WriteJSONError(w, r, notFoundError("Source API association "+assocID+" not found."))
		return
	}

	if err := h.store.DeleteSourceApiAssociation(r.Context(), mergedApiID, assocID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	// Re-merge remaining sources.
	h.reMergeAllSources(r.Context(), mergedApiID)

	writeJSON(w, r, http.StatusOK, map[string]any{"sourceApiAssociation": assoc})
}

// ─── DisassociateMergedGraphqlApi ────────────────────────────────────────────

// DisassociateMergedGraphqlApi handles DELETE /v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations/{associationId}.
func (h *Handler) DisassociateMergedGraphqlApi(w http.ResponseWriter, r *http.Request) {
	// The association ID identifies the association globally.
	// We need to find which merged API this belongs to.
	assocID := chi.URLParam(r, "associationId")

	// List all APIs to find the merged one containing this association.
	apis, err := h.store.ListAPIs(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	for _, api := range apis {
		assoc, storeErr := h.store.GetSourceApiAssociation(r.Context(), api.ApiId, assocID)
		if storeErr != nil {
			continue
		}
		if assoc != nil {
			if delErr := h.store.DeleteSourceApiAssociation(r.Context(), api.ApiId, assocID); delErr != nil {
				protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, delErr))
				return
			}
			h.reMergeAllSources(r.Context(), api.ApiId)
			writeJSON(w, r, http.StatusOK, map[string]any{"sourceApiAssociation": assoc})
			return
		}
	}

	protocol.WriteJSONError(w, r, notFoundError("Source API association "+assocID+" not found."))
}

// ─── StartSchemaMerge ────────────────────────────────────────────────────────

// StartSchemaMerge handles POST /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}/merge.
func (h *Handler) StartSchemaMerge(w http.ResponseWriter, r *http.Request) {
	mergedApiID := chi.URLParam(r, "mergedApiIdentifier")
	assocID := chi.URLParam(r, "associationId")

	assoc, err := h.store.GetSourceApiAssociation(r.Context(), mergedApiID, assocID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if assoc == nil {
		protocol.WriteJSONError(w, r, notFoundError("Source API association "+assocID+" not found."))
		return
	}

	h.performMerge(r.Context(), mergedApiID, assoc)

	writeJSON(w, r, http.StatusOK, map[string]any{
		"sourceApiAssociationStatus": assoc.SourceApiAssociationStatus,
	})
}

// ─── Merge helpers ───────────────────────────────────────────────────────────

// performMerge merges all source API schemas for the given merged API.
// Updates the association status and stores the merged schema.
func (h *Handler) performMerge(ctx context.Context, mergedApiID string, assoc *SourceApiAssociation) {
	assocs, err := h.store.ListSourceApiAssociations(ctx, mergedApiID)
	if err != nil {
		assoc.SourceApiAssociationStatus = "MERGE_FAILED"
		assoc.SourceApiAssociationStatusDetail = err.Error()
		_ = h.store.PutSourceApiAssociation(ctx, mergedApiID, assoc)
		return
	}

	var schemas [][]byte
	for _, a := range assocs {
		schema, sErr := h.store.GetSchema(ctx, a.SourceApiId)
		if sErr != nil || schema == nil {
			continue
		}
		schemas = append(schemas, schema.Definition)
	}

	if len(schemas) == 0 {
		assoc.SourceApiAssociationStatus = "MERGE_SUCCESS"
		assoc.SourceApiAssociationStatusDetail = "No source schemas to merge."
		assoc.LastSuccessfulMergeDate = h.clk.Now().Unix()
		_ = h.store.PutSourceApiAssociation(ctx, mergedApiID, assoc)
		return
	}

	parsed, mergeErr := h.sp.Merge(schemas)
	if mergeErr != nil {
		assoc.SourceApiAssociationStatus = "MERGE_FAILED"
		assoc.SourceApiAssociationStatusDetail = mergeErr.Error()
		_ = h.store.PutSourceApiAssociation(ctx, mergedApiID, assoc)
		return
	}

	// Store merged schema on the merged API.
	mergedSchema := &Schema{
		ApiId:      mergedApiID,
		Definition: parsed.Raw,
		Status:     "ACTIVE",
	}
	if sErr := h.store.PutSchema(ctx, mergedSchema); sErr != nil {
		assoc.SourceApiAssociationStatus = "MERGE_FAILED"
		assoc.SourceApiAssociationStatusDetail = sErr.Error()
		_ = h.store.PutSourceApiAssociation(ctx, mergedApiID, assoc)
		return
	}

	// Cache the parsed schema.
	h.sp.Put(mergedApiID, parsed)

	assoc.SourceApiAssociationStatus = "MERGE_SUCCESS"
	assoc.SourceApiAssociationStatusDetail = ""
	assoc.LastSuccessfulMergeDate = h.clk.Now().Unix()
	_ = h.store.PutSourceApiAssociation(ctx, mergedApiID, assoc)
}

// reMergeAllSources rebuilds the merged schema from all remaining source associations.
func (h *Handler) reMergeAllSources(ctx context.Context, mergedApiID string) {
	assocs, err := h.store.ListSourceApiAssociations(ctx, mergedApiID)
	if err != nil || len(assocs) == 0 {
		// No remaining sources — clear the merged schema.
		h.sp.Evict(mergedApiID)
		return
	}

	var schemas [][]byte
	for _, a := range assocs {
		schema, sErr := h.store.GetSchema(ctx, a.SourceApiId)
		if sErr != nil || schema == nil {
			continue
		}
		schemas = append(schemas, schema.Definition)
	}

	if len(schemas) == 0 {
		h.sp.Evict(mergedApiID)
		return
	}

	parsed, mergeErr := h.sp.Merge(schemas)
	if mergeErr != nil {
		return
	}

	mergedSchema := &Schema{
		ApiId:      mergedApiID,
		Definition: parsed.Raw,
		Status:     "ACTIVE",
	}
	_ = h.store.PutSchema(ctx, mergedSchema)
	h.sp.Put(mergedApiID, parsed)
}
