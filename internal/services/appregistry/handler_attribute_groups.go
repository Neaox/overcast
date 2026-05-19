package appregistry

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// AttributeGroup emulation is "inert tier": attributes are stored and
// returned verbatim, but the emulator does not interpret them or enforce any
// cross-resource constraints. This gives SDK clients a valid success path for
// CRUD without pretending Overcast implements the full AppRegistry semantics.

type attributeGroupResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Arn            string            `json:"arn"`
	Description    string            `json:"description,omitempty"`
	Attributes     string            `json:"attributes,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	CreationTime   float64           `json:"creationTime"`
	LastUpdateTime float64           `json:"lastUpdateTime"`
}

func toAttributeGroupResponse(ag *AttributeGroup) attributeGroupResponse {
	return attributeGroupResponse{
		ID:             ag.ID,
		Name:           ag.Name,
		Arn:            ag.ARN,
		Description:    ag.Description,
		Attributes:     ag.Attributes,
		Tags:           ag.Tags,
		CreationTime:   ag.CreationTime,
		LastUpdateTime: ag.LastUpdateTime,
	}
}

// decodeAttributes accepts either a JSON string or a JSON object for the
// `attributes` field — the SDK may send either depending on client version.
func decodeAttributes(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func (h *Handler) CreateAttributeGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Attributes  json.RawMessage   `json:"attributes"`
		Tags        map[string]string `json:"tags"`
		ClientToken string            `json:"clientToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("name is required"))
		return
	}

	ctx := r.Context()
	if existing, _ := h.store.resolveAttributeGroup(ctx, req.Name); existing != nil {
		protocol.WriteJSONError(w, r, errConflict(req.Name))
		return
	}

	now := h.store.now()
	id := uuid.New().String()
	region := middleware.RegionFromContext(ctx, h.cfg.Region)
	arn := protocol.ARN(region, h.cfg.AccountID, "servicecatalog", fmt.Sprintf("/attribute-groups/%s", id))

	ag := &AttributeGroup{
		ID:             id,
		Name:           req.Name,
		ARN:            arn,
		Description:    req.Description,
		Attributes:     decodeAttributes(req.Attributes),
		Tags:           req.Tags,
		CreationTime:   float64(now.Unix()),
		LastUpdateTime: float64(now.Unix()),
	}
	if aerr := h.store.putAttributeGroup(ctx, ag); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		AttributeGroup attributeGroupResponse `json:"attributeGroup"`
	}{AttributeGroup: toAttributeGroupResponse(ag)})
}

func (h *Handler) GetAttributeGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "attributeGroup")
	ag, aerr := h.store.resolveAttributeGroup(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, toAttributeGroupResponse(ag))
}

func (h *Handler) ListAttributeGroups(w http.ResponseWriter, r *http.Request) {
	ags, aerr := h.store.listAttributeGroups(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	out := make([]attributeGroupResponse, 0, len(ags))
	for i := range ags {
		out = append(out, toAttributeGroupResponse(&ags[i]))
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		AttributeGroups []attributeGroupResponse `json:"attributeGroups"`
	}{AttributeGroups: out})
}

func (h *Handler) UpdateAttributeGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "attributeGroup")
	var req struct {
		Name        *string         `json:"name"`
		Description *string         `json:"description"`
		Attributes  json.RawMessage `json:"attributes"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	ag, aerr := h.store.resolveAttributeGroup(ctx, id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if req.Name != nil && *req.Name != "" && *req.Name != ag.Name {
		if existing, _ := h.store.resolveAttributeGroup(ctx, *req.Name); existing != nil && existing.ID != ag.ID {
			protocol.WriteJSONError(w, r, errConflict(*req.Name))
			return
		}
		ag.Name = *req.Name
	}
	if req.Description != nil {
		ag.Description = *req.Description
	}
	if len(req.Attributes) > 0 {
		ag.Attributes = decodeAttributes(req.Attributes)
	}
	ag.LastUpdateTime = float64(h.store.now().Unix())

	if aerr := h.store.putAttributeGroup(ctx, ag); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		AttributeGroup attributeGroupResponse `json:"attributeGroup"`
	}{AttributeGroup: toAttributeGroupResponse(ag)})
}

func (h *Handler) DeleteAttributeGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "attributeGroup")
	ctx := r.Context()
	ag, aerr := h.store.resolveAttributeGroup(ctx, id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteAttributeGroup(ctx, ag.ID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		AttributeGroup attributeGroupResponse `json:"attributeGroup"`
	}{AttributeGroup: toAttributeGroupResponse(ag)})
}

func (h *Handler) AssociateAttributeGroup(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	agID := chi.URLParam(r, "attributeGroup")
	ctx := r.Context()

	app, aerr := h.store.resolveApplication(ctx, appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ag, aerr := h.store.resolveAttributeGroup(ctx, agID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.putAttrGroupAssoc(ctx, app.ID, ag.ID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		ApplicationArn    string `json:"applicationArn"`
		AttributeGroupArn string `json:"attributeGroupArn"`
	}{ApplicationArn: app.ARN, AttributeGroupArn: ag.ARN})
}

func (h *Handler) DisassociateAttributeGroup(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	agID := chi.URLParam(r, "attributeGroup")
	ctx := r.Context()

	app, aerr := h.store.resolveApplication(ctx, appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ag, aerr := h.store.resolveAttributeGroup(ctx, agID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteAttrGroupAssoc(ctx, app.ID, ag.ID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		ApplicationArn    string `json:"applicationArn"`
		AttributeGroupArn string `json:"attributeGroupArn"`
	}{ApplicationArn: app.ARN, AttributeGroupArn: ag.ARN})
}

func (h *Handler) ListAssociatedAttributeGroups(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	ctx := r.Context()
	app, aerr := h.store.resolveApplication(ctx, appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ids, aerr := h.store.listAttrGroupAssocs(ctx, app.ID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		AttributeGroups []string `json:"attributeGroups"`
	}{AttributeGroups: ids})
}
