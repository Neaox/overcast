package appregistry

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds AppRegistry handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *arStore
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	bus   *events.Bus
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	return &Handler{
		cfg:   cfg,
		store: newStore(store, clk, cfg.Region),
		log:   log,
		clk:   clk,
	}
}

// applicationResponse mirrors the AWS wire shape for a single application.
type applicationResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Arn            string            `json:"arn"`
	Description    string            `json:"description,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	ApplicationTag map[string]string `json:"applicationTag,omitempty"`
	CreationTime   float64           `json:"creationTime"`
	LastUpdateTime float64           `json:"lastUpdateTime"`
}

func toResponse(app *Application) applicationResponse {
	return applicationResponse{
		ID:             app.ID,
		Name:           app.Name,
		Arn:            app.ARN,
		Description:    app.Description,
		Tags:           app.Tags,
		ApplicationTag: app.ApplicationTag,
		CreationTime:   app.CreationTime,
		LastUpdateTime: app.LastUpdateTime,
	}
}

// ─── CreateApplication ────────────────────────────────────────────────────

func (h *Handler) CreateApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
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
	if existing, _ := h.store.resolveApplication(ctx, req.Name); existing != nil {
		protocol.WriteJSONError(w, r, errConflict(req.Name))
		return
	}

	now := h.store.now()
	id := uuid.New().String()
	region := middleware.RegionFromContext(ctx, h.cfg.Region)
	arn := protocol.ARN(region, h.cfg.AccountID, "servicecatalog", fmt.Sprintf("/applications/%s", id))

	app := &Application{
		ID:             id,
		Name:           req.Name,
		ARN:            arn,
		Description:    req.Description,
		Tags:           req.Tags,
		ApplicationTag: map[string]string{"awsApplication": arn},
		CreationTime:   float64(now.Unix()),
		LastUpdateTime: float64(now.Unix()),
	}

	if aerr := h.store.putApplication(ctx, app); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Application applicationResponse `json:"application"`
	}{Application: toResponse(app)})
}

// ─── GetApplication ───────────────────────────────────────────────────────

func (h *Handler) GetApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "application")
	app, aerr := h.store.resolveApplication(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Compute associated resource count so the UI can badge it without a second call.
	assocs, aerr := h.store.listAssociations(r.Context(), app.ID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		applicationResponse
		AssociatedResourceCount int `json:"associatedResourceCount"`
	}{
		applicationResponse:     toResponse(app),
		AssociatedResourceCount: len(assocs),
	})
}

// ─── ListApplications ─────────────────────────────────────────────────────

func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) {
	apps, aerr := h.store.listApplications(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	summaries := make([]applicationResponse, 0, len(apps))
	for i := range apps {
		summaries = append(summaries, toResponse(&apps[i]))
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Applications []applicationResponse `json:"applications"`
	}{Applications: summaries})
}

// ─── UpdateApplication ────────────────────────────────────────────────────

func (h *Handler) UpdateApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "application")
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	app, aerr := h.store.resolveApplication(ctx, id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if req.Name != nil && *req.Name != "" && *req.Name != app.Name {
		if existing, _ := h.store.resolveApplication(ctx, *req.Name); existing != nil && existing.ID != app.ID {
			protocol.WriteJSONError(w, r, errConflict(*req.Name))
			return
		}
		app.Name = *req.Name
	}
	if req.Description != nil {
		app.Description = *req.Description
	}
	app.LastUpdateTime = float64(h.store.now().Unix())

	if aerr := h.store.putApplication(ctx, app); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Application applicationResponse `json:"application"`
	}{Application: toResponse(app)})
}

// ─── DeleteApplication ────────────────────────────────────────────────────

func (h *Handler) DeleteApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "application")
	app, aerr := h.store.resolveApplication(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	assocs, aerr := h.store.listAssociations(r.Context(), app.ID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for _, a := range assocs {
		if aerr := h.store.deleteAssociation(r.Context(), a.ApplicationID, a.ResourceType, a.ResourceARN); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}

	if aerr := h.store.deleteApplication(r.Context(), app.ID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Application applicationResponse `json:"application"`
	}{Application: toResponse(app)})
}

// ─── AssociateResource ────────────────────────────────────────────────────

func (h *Handler) AssociateResource(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	resourceType := strings.ToUpper(chi.URLParam(r, "resourceType"))
	resource, _ := url.PathUnescape(chi.URLParam(r, "resource"))

	if resourceType != "CFN_STACK" && resourceType != "RESOURCE_TAG_VALUE" {
		protocol.WriteJSONError(w, r, errInvalidParameter("unsupported resourceType: "+resourceType))
		return
	}
	if resource == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("resource is required"))
		return
	}

	app, aerr := h.store.resolveApplication(r.Context(), appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	assoc := &ResourceAssociation{
		ApplicationID: app.ID,
		ResourceType:  resourceType,
		ResourceARN:   resource,
		CreationTime:  float64(h.store.now().Unix()),
	}
	if aerr := h.store.putAssociation(r.Context(), assoc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		ApplicationArn string `json:"applicationArn"`
		ResourceArn    string `json:"resourceArn"`
	}{ApplicationArn: app.ARN, ResourceArn: resource})
}

// ─── DisassociateResource ─────────────────────────────────────────────────

func (h *Handler) DisassociateResource(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	resourceType := strings.ToUpper(chi.URLParam(r, "resourceType"))
	resource, _ := url.PathUnescape(chi.URLParam(r, "resource"))

	app, aerr := h.store.resolveApplication(r.Context(), appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if _, aerr := h.store.getAssociation(r.Context(), app.ID, resourceType, resource); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteAssociation(r.Context(), app.ID, resourceType, resource); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		ApplicationArn string `json:"applicationArn"`
		ResourceArn    string `json:"resourceArn"`
	}{ApplicationArn: app.ARN, ResourceArn: resource})
}

// ─── ListAssociatedResources ──────────────────────────────────────────────

type resourceInfo struct {
	Name         string  `json:"name"`
	ARN          string  `json:"arn"`
	ResourceType string  `json:"resourceType"`
	CreationTime float64 `json:"creationTime,omitempty"`
}

func (h *Handler) ListAssociatedResources(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	app, aerr := h.store.resolveApplication(r.Context(), appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	assocs, aerr := h.store.listAssociations(r.Context(), app.ID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	resources := make([]resourceInfo, 0, len(assocs))
	for _, a := range assocs {
		resources = append(resources, resourceInfo{
			Name:         a.ResourceARN,
			ARN:          a.ResourceARN,
			ResourceType: a.ResourceType,
			CreationTime: a.CreationTime,
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Resources []resourceInfo `json:"resources"`
	}{Resources: resources})
}

// ─── GetAssociatedResource ────────────────────────────────────────────────

func (h *Handler) GetAssociatedResource(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "application")
	resourceType := strings.ToUpper(chi.URLParam(r, "resourceType"))
	resource, _ := url.PathUnescape(chi.URLParam(r, "resource"))

	app, aerr := h.store.resolveApplication(r.Context(), appID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	assoc, aerr := h.store.getAssociation(r.Context(), app.ID, resourceType, resource)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Resource resourceInfo `json:"resource"`
	}{Resource: resourceInfo{
		Name:         assoc.ResourceARN,
		ARN:          assoc.ResourceARN,
		ResourceType: assoc.ResourceType,
		CreationTime: assoc.CreationTime,
	}})
}
