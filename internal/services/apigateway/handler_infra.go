package apigateway

// handler_infra.go — Inert infrastructure metadata handlers.
//
// These handlers store and return metadata but have no effect on request
// routing or execution. All operations are purely CRUD.
//
// Implemented:
//   REST v1: CreateDomainName, GetDomainNames, DeleteDomainName
//            CreateBasePathMapping, GetBasePathMappings
//            CreateVpcLink, GetVpcLinks, DeleteVpcLink
//            TagResource, UntagResource, GetTags
//   HTTP v2: CreateV2DomainName, GetV2DomainNames, DeleteV2DomainName
//            CreateV2VpcLink, GetV2VpcLinks, DeleteV2VpcLink
//            CreateV2ApiMapping, GetV2ApiMappings
//            TagV2Resource, UntagV2Resource, GetV2Tags

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/domainregistry"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- REST API v1: Domain Names --------------------------------------------

type createDomainNameRequest struct {
	DomainName             string            `json:"domainName"`
	CertificateArn         string            `json:"certificateArn,omitempty"`
	CertificateName        string            `json:"certificateName,omitempty"`
	RegionalCertificateArn string            `json:"regionalCertificateArn,omitempty"`
	EndpointConfiguration  map[string]any    `json:"endpointConfiguration,omitempty"`
	Tags                   map[string]string `json:"tags,omitempty"`
}

// CreateDomainName handles POST /domainnames.
func (h *Handler) CreateDomainName(w http.ResponseWriter, r *http.Request) {
	h.ensureRegistryHydrated()
	var req createDomainNameRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DomainName == "" {
		protocol.WriteJSONError(w, r, errBadRequest("domainName is required"))
		return
	}

	dn := &DomainName{
		DomainName:             req.DomainName,
		CertificateArn:         req.CertificateArn,
		CertificateName:        req.CertificateName,
		RegionalCertificateArn: req.RegionalCertificateArn,
		Tags:                   req.Tags,
	}

	if aerr := h.store.putDomainName(r.Context(), dn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.domainRegistry != nil {
		h.domainRegistry.Put(domainregistry.Record{Name: dn.DomainName, Source: "apigateway.v1"})
	}

	h.log.Info("domain name created", zap.String("domainName", dn.DomainName))
	protocol.WriteJSON(w, r, http.StatusCreated, dn)
}

// GetDomainNames handles GET /domainnames.
func (h *Handler) GetDomainNames(w http.ResponseWriter, r *http.Request) {
	h.ensureRegistryHydrated()
	items, aerr := h.store.listDomainNames(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"item": items,
	})
}

// DeleteDomainName handles DELETE /domainnames/{domainName}.
func (h *Handler) DeleteDomainName(w http.ResponseWriter, r *http.Request) {
	h.ensureRegistryHydrated()
	domainName := chi.URLParam(r, "domainName")

	if _, aerr := h.store.getDomainName(r.Context(), domainName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteDomainName(r.Context(), domainName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.domainRegistry != nil {
		h.domainRegistry.Delete(domainName)
	}

	h.log.Info("domain name deleted", zap.String("domainName", domainName))
	w.WriteHeader(http.StatusAccepted)
}

// ---- REST API v1: Base Path Mappings --------------------------------------

type createBasePathMappingRequest struct {
	BasePath  string `json:"basePath"`
	RestApiID string `json:"restApiId"`
	Stage     string `json:"stage,omitempty"`
}

// CreateBasePathMapping handles POST /domainnames/{domainName}/basepathmappings.
func (h *Handler) CreateBasePathMapping(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	// Verify the domain name exists.
	if _, aerr := h.store.getDomainName(r.Context(), domainName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createBasePathMappingRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.RestApiID == "" {
		protocol.WriteJSONError(w, r, errBadRequest("restApiId is required"))
		return
	}

	bpm := &BasePathMapping{
		BasePath:  req.BasePath,
		RestApiID: req.RestApiID,
		Stage:     req.Stage,
	}

	if aerr := h.store.putBasePathMapping(r.Context(), domainName, bpm); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, bpm)
}

// GetBasePathMappings handles GET /domainnames/{domainName}/basepathmappings.
func (h *Handler) GetBasePathMappings(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	items, aerr := h.store.listBasePathMappings(r.Context(), domainName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"item": items,
	})
}

// ---- REST API v1: VPC Links -----------------------------------------------

type createVpcLinkRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	TargetArns  []string `json:"targetArns"`
}

// CreateVpcLink handles POST /vpclinks.
func (h *Handler) CreateVpcLink(w http.ResponseWriter, r *http.Request) {
	var req createVpcLinkRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("name is required"))
		return
	}

	vl := &VpcLink{
		ID:          generateShortID(),
		Name:        req.Name,
		Description: req.Description,
		TargetArns:  req.TargetArns,
		Status:      "AVAILABLE",
	}

	if aerr := h.store.putVpcLink(r.Context(), vl); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("VPC link created", zap.String("id", vl.ID), zap.String("name", vl.Name))
	protocol.WriteJSON(w, r, http.StatusCreated, vl)
}

// GetVpcLinks handles GET /vpclinks.
func (h *Handler) GetVpcLinks(w http.ResponseWriter, r *http.Request) {
	items, aerr := h.store.listVpcLinks(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"item": items,
	})
}

// DeleteVpcLink handles DELETE /vpclinks/{vpcLinkId}.
func (h *Handler) DeleteVpcLink(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vpcLinkId")

	if _, aerr := h.store.getVpcLink(r.Context(), id); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteVpcLink(r.Context(), id); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("VPC link deleted", zap.String("id", id))
	w.WriteHeader(http.StatusAccepted)
}

// ---- REST API v1: Tags ----------------------------------------------------

type tagResourceRequest struct {
	Tags map[string]string `json:"tags"`
}

// TagResource handles PUT /tags/{resourceArn}.
func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")

	var req tagResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Merge with any existing tags.
	existing, aerr := h.store.getResourceTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}

	if aerr := h.store.putResourceTags(r.Context(), arn, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UntagResource handles DELETE /tags/{resourceArn}?tagKeys=k1,k2.
func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	keysParam := r.URL.Query().Get("tagKeys")

	existing, aerr := h.store.getResourceTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}

	if keysParam != "" {
		for _, k := range strings.Split(keysParam, ",") {
			delete(existing, strings.TrimSpace(k))
		}
	}

	if aerr := h.store.putResourceTags(r.Context(), arn, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetTags handles GET /tags/{resourceArn}.
func (h *Handler) GetTags(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")

	tags, aerr := h.store.getResourceTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if tags == nil {
		tags = make(map[string]string)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"tags": tags,
	})
}

// ---- HTTP API v2: Domain Names -------------------------------------------

type createV2DomainNameRequest struct {
	DomainName               string             `json:"domainName"`
	DomainNameConfigurations []DomainNameConfig `json:"domainNameConfigurations,omitempty"`
	Tags                     map[string]string  `json:"tags,omitempty"`
}

// CreateV2DomainName handles POST /v2/domainnames.
func (h *Handler) CreateV2DomainName(w http.ResponseWriter, r *http.Request) {
	h.ensureRegistryHydrated()
	var req createV2DomainNameRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DomainName == "" {
		protocol.WriteJSONError(w, r, errBadRequest("domainName is required"))
		return
	}

	dn := &DomainNameV2{
		DomainName:               req.DomainName,
		DomainNameConfigurations: req.DomainNameConfigurations,
		Tags:                     req.Tags,
	}

	if aerr := h.store.putV2DomainName(r.Context(), dn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.domainRegistry != nil {
		h.domainRegistry.Put(domainregistry.Record{Name: dn.DomainName, Source: "apigateway.v2"})
	}

	h.log.Info("v2 domain name created", zap.String("domainName", dn.DomainName))
	protocol.WriteJSON(w, r, http.StatusCreated, dn)
}

// GetV2DomainNames handles GET /v2/domainnames.
func (h *Handler) GetV2DomainNames(w http.ResponseWriter, r *http.Request) {
	h.ensureRegistryHydrated()
	items, aerr := h.store.listV2DomainNames(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"items": items,
	})
}

// DeleteV2DomainName handles DELETE /v2/domainnames/{domainName}.
func (h *Handler) DeleteV2DomainName(w http.ResponseWriter, r *http.Request) {
	h.ensureRegistryHydrated()
	domainName := chi.URLParam(r, "domainName")

	if _, aerr := h.store.getV2DomainName(r.Context(), domainName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteV2DomainName(r.Context(), domainName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.domainRegistry != nil {
		h.domainRegistry.Delete(domainName)
	}

	h.log.Info("v2 domain name deleted", zap.String("domainName", domainName))
	w.WriteHeader(http.StatusNoContent)
}

// ---- HTTP API v2: VPC Links -----------------------------------------------

type createV2VpcLinkRequest struct {
	Name             string            `json:"name"`
	SubnetIDs        []string          `json:"subnetIds"`
	SecurityGroupIDs []string          `json:"securityGroupIds,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

// CreateV2VpcLink handles POST /v2/vpclinks.
func (h *Handler) CreateV2VpcLink(w http.ResponseWriter, r *http.Request) {
	var req createV2VpcLinkRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("name is required"))
		return
	}

	vl := &VpcLinkV2{
		VpcLinkID:        generateShortID(),
		Name:             req.Name,
		SubnetIDs:        req.SubnetIDs,
		SecurityGroupIDs: req.SecurityGroupIDs,
		Tags:             req.Tags,
		Status:           "AVAILABLE",
	}

	if aerr := h.store.putV2VpcLink(r.Context(), vl); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("v2 VPC link created", zap.String("id", vl.VpcLinkID), zap.String("name", vl.Name))
	protocol.WriteJSON(w, r, http.StatusCreated, vl)
}

// GetV2VpcLinks handles GET /v2/vpclinks.
func (h *Handler) GetV2VpcLinks(w http.ResponseWriter, r *http.Request) {
	items, aerr := h.store.listV2VpcLinks(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"items": items,
	})
}

// DeleteV2VpcLink handles DELETE /v2/vpclinks/{vpcLinkId}.
func (h *Handler) DeleteV2VpcLink(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vpcLinkId")

	if _, aerr := h.store.getV2VpcLink(r.Context(), id); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteV2VpcLink(r.Context(), id); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("v2 VPC link deleted", zap.String("id", id))
	w.WriteHeader(http.StatusNoContent)
}

// ---- HTTP API v2: API Mappings -------------------------------------------

type createV2ApiMappingRequest struct {
	ApiID         string `json:"apiId"`
	Stage         string `json:"stage"`
	ApiMappingKey string `json:"apiMappingKey,omitempty"`
}

// CreateV2ApiMapping handles POST /v2/domainnames/{domainName}/apimappings.
func (h *Handler) CreateV2ApiMapping(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	// Verify the domain name exists.
	if _, aerr := h.store.getV2DomainName(r.Context(), domainName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2ApiMappingRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ApiID == "" {
		protocol.WriteJSONError(w, r, errBadRequest("apiId is required"))
		return
	}

	m := &ApiMapping{
		ApiMappingID:  generateShortID(),
		ApiID:         req.ApiID,
		Stage:         req.Stage,
		ApiMappingKey: req.ApiMappingKey,
	}

	if aerr := h.store.putV2ApiMapping(r.Context(), domainName, m); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, m)
}

// GetV2ApiMappings handles GET /v2/domainnames/{domainName}/apimappings.
func (h *Handler) GetV2ApiMappings(w http.ResponseWriter, r *http.Request) {
	domainName := chi.URLParam(r, "domainName")

	items, aerr := h.store.listV2ApiMappings(r.Context(), domainName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"items": items,
	})
}

// ---- HTTP API v2: Tags ---------------------------------------------------

type tagV2ResourceRequest struct {
	Tags map[string]string `json:"Tags"`
}

// TagV2Resource handles POST /v2/tags/{resourceArn}.
func (h *Handler) TagV2Resource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")

	var req tagV2ResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Merge with existing tags.
	existing, aerr := h.store.getV2Tags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}

	if aerr := h.store.putV2Tags(r.Context(), arn, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// UntagV2Resource handles DELETE /v2/tags/{resourceArn}?tagKeys=k1,k2.
func (h *Handler) UntagV2Resource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	keysParam := r.URL.Query().Get("tagKeys")

	existing, aerr := h.store.getV2Tags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}

	if keysParam != "" {
		for _, k := range strings.Split(keysParam, ",") {
			delete(existing, strings.TrimSpace(k))
		}
	}

	if aerr := h.store.putV2Tags(r.Context(), arn, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetV2Tags handles GET /v2/tags/{resourceArn}.
func (h *Handler) GetV2Tags(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")

	tags, aerr := h.store.getV2Tags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if tags == nil {
		tags = make(map[string]string)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"tags": tags,
	})
}
