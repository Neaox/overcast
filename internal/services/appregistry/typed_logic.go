package appregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

// ─── Application typed request/response types ──────────────────────────────────

type createApplicationRequest struct {
	Name        string            `json:"Name" cbor:"Name"`
	Description string            `json:"Description" cbor:"Description"`
	Tags        map[string]string `json:"Tags" cbor:"Tags"`
	ClientToken string            `json:"ClientToken" cbor:"ClientToken"`
}

type createApplicationResponse struct {
	Application applicationResponse `json:"Application" cbor:"Application"`
}

func (h *Handler) createApplicationTyped(ctx context.Context, req *createApplicationRequest) (*createApplicationResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, errInvalidParameter("name is required")
	}
	if existing, _ := h.store.resolveApplication(ctx, req.Name); existing != nil {
		return nil, errConflict(req.Name)
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
		return nil, aerr
	}
	return &createApplicationResponse{Application: toResponse(app)}, nil
}

type getApplicationRequest struct {
	Application string `json:"Application" cbor:"Application"`
}

type getApplicationResponse struct {
	applicationResponse
	AssociatedResourceCount int `json:"AssociatedResourceCount" cbor:"AssociatedResourceCount"`
}

func (h *Handler) getApplicationTyped(ctx context.Context, req *getApplicationRequest) (*getApplicationResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	assocs, aerr := h.store.listAssociations(ctx, app.ID)
	if aerr != nil {
		return nil, aerr
	}
	return &getApplicationResponse{
		applicationResponse:     toResponse(app),
		AssociatedResourceCount: len(assocs),
	}, nil
}

type listApplicationsRequest struct{}

type listApplicationsResponse struct {
	Applications []applicationResponse `json:"Applications" cbor:"Applications"`
}

func (h *Handler) listApplicationsTyped(ctx context.Context, _ *listApplicationsRequest) (*listApplicationsResponse, *protocol.AWSError) {
	apps, aerr := h.store.listApplications(ctx)
	if aerr != nil {
		return nil, aerr
	}
	summaries := make([]applicationResponse, 0, len(apps))
	for i := range apps {
		summaries = append(summaries, toResponse(&apps[i]))
	}
	return &listApplicationsResponse{Applications: summaries}, nil
}

type deleteApplicationRequest struct {
	Application string `json:"Application" cbor:"Application"`
}

type deleteApplicationResponse struct {
	Application applicationResponse `json:"Application" cbor:"Application"`
}

func (h *Handler) deleteApplicationTyped(ctx context.Context, req *deleteApplicationRequest) (*deleteApplicationResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	assocs, aerr := h.store.listAssociations(ctx, app.ID)
	if aerr != nil {
		return nil, aerr
	}
	for _, a := range assocs {
		if aerr := h.store.deleteAssociation(ctx, a.ApplicationID, a.ResourceType, a.ResourceARN); aerr != nil {
			return nil, aerr
		}
	}
	if aerr := h.store.deleteApplication(ctx, app.ID); aerr != nil {
		return nil, aerr
	}
	return &deleteApplicationResponse{Application: toResponse(app)}, nil
}

type updateApplicationRequest struct {
	Application string  `json:"Application" cbor:"Application"`
	Name        *string `json:"Name" cbor:"Name"`
	Description *string `json:"Description" cbor:"Description"`
}

type updateApplicationResponse struct {
	Application applicationResponse `json:"Application" cbor:"Application"`
}

func (h *Handler) updateApplicationTyped(ctx context.Context, req *updateApplicationRequest) (*updateApplicationResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	if req.Name != nil && *req.Name != "" && *req.Name != app.Name {
		if existing, _ := h.store.resolveApplication(ctx, *req.Name); existing != nil && existing.ID != app.ID {
			return nil, errConflict(*req.Name)
		}
		app.Name = *req.Name
	}
	if req.Description != nil {
		app.Description = *req.Description
	}
	app.LastUpdateTime = float64(h.store.now().Unix())
	if aerr := h.store.putApplication(ctx, app); aerr != nil {
		return nil, aerr
	}
	return &updateApplicationResponse{Application: toResponse(app)}, nil
}

// ─── Resource association typed request/response types ─────────────────────────

type associateResourceRequest struct {
	Application  string `json:"Application" cbor:"Application"`
	ResourceType string `json:"ResourceType" cbor:"ResourceType"`
	Resource     string `json:"Resource" cbor:"Resource"`
}

type associateResourceResponse struct {
	ApplicationArn string `json:"ApplicationArn" cbor:"ApplicationArn"`
	ResourceArn    string `json:"ResourceArn" cbor:"ResourceArn"`
}

func (h *Handler) associateResourceTyped(ctx context.Context, req *associateResourceRequest) (*associateResourceResponse, *protocol.AWSError) {
	resourceType := strings.ToUpper(req.ResourceType)
	resource, _ := url.PathUnescape(req.Resource)

	if resourceType != "CFN_STACK" && resourceType != "RESOURCE_TAG_VALUE" {
		return nil, errInvalidParameter("unsupported resourceType: " + resourceType)
	}
	if resource == "" {
		return nil, errInvalidParameter("resource is required")
	}

	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}

	assoc := &ResourceAssociation{
		ApplicationID: app.ID,
		ResourceType:  resourceType,
		ResourceARN:   resource,
		CreationTime:  float64(h.store.now().Unix()),
	}
	if aerr := h.store.putAssociation(ctx, assoc); aerr != nil {
		return nil, aerr
	}
	return &associateResourceResponse{ApplicationArn: app.ARN, ResourceArn: resource}, nil
}

type disassociateResourceRequest struct {
	Application  string `json:"Application" cbor:"Application"`
	ResourceType string `json:"ResourceType" cbor:"ResourceType"`
	Resource     string `json:"Resource" cbor:"Resource"`
}

type disassociateResourceResponse struct {
	ApplicationArn string `json:"ApplicationArn" cbor:"ApplicationArn"`
	ResourceArn    string `json:"ResourceArn" cbor:"ResourceArn"`
}

func (h *Handler) disassociateResourceTyped(ctx context.Context, req *disassociateResourceRequest) (*disassociateResourceResponse, *protocol.AWSError) {
	resourceType := strings.ToUpper(req.ResourceType)
	resource, _ := url.PathUnescape(req.Resource)

	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := h.store.getAssociation(ctx, app.ID, resourceType, resource); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteAssociation(ctx, app.ID, resourceType, resource); aerr != nil {
		return nil, aerr
	}
	return &disassociateResourceResponse{ApplicationArn: app.ARN, ResourceArn: resource}, nil
}

type listAssociatedResourcesRequest struct {
	Application string `json:"Application" cbor:"Application"`
}

type listAssociatedResourcesResponse struct {
	Resources []resourceInfo `json:"Resources" cbor:"Resources"`
}

func (h *Handler) listAssociatedResourcesTyped(ctx context.Context, req *listAssociatedResourcesRequest) (*listAssociatedResourcesResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	assocs, aerr := h.store.listAssociations(ctx, app.ID)
	if aerr != nil {
		return nil, aerr
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
	return &listAssociatedResourcesResponse{Resources: resources}, nil
}

type getAssociatedResourceRequest struct {
	Application  string `json:"Application" cbor:"Application"`
	ResourceType string `json:"ResourceType" cbor:"ResourceType"`
	Resource     string `json:"Resource" cbor:"Resource"`
}

type getAssociatedResourceResponse struct {
	Resource resourceInfo `json:"Resource" cbor:"Resource"`
}

func (h *Handler) getAssociatedResourceTyped(ctx context.Context, req *getAssociatedResourceRequest) (*getAssociatedResourceResponse, *protocol.AWSError) {
	resourceType := strings.ToUpper(req.ResourceType)
	resource, _ := url.PathUnescape(req.Resource)

	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	assoc, aerr := h.store.getAssociation(ctx, app.ID, resourceType, resource)
	if aerr != nil {
		return nil, aerr
	}
	return &getAssociatedResourceResponse{Resource: resourceInfo{
		Name:         assoc.ResourceARN,
		ARN:          assoc.ResourceARN,
		ResourceType: assoc.ResourceType,
		CreationTime: assoc.CreationTime,
	}}, nil
}

// ─── Attribute group association typed request/response types ──────────────────

type associateAttributeGroupRequest struct {
	Application    string `json:"Application" cbor:"Application"`
	AttributeGroup string `json:"AttributeGroup" cbor:"AttributeGroup"`
}

type associateAttributeGroupResponse struct {
	ApplicationArn    string `json:"ApplicationArn" cbor:"ApplicationArn"`
	AttributeGroupArn string `json:"AttributeGroupArn" cbor:"AttributeGroupArn"`
}

func (h *Handler) associateAttributeGroupTyped(ctx context.Context, req *associateAttributeGroupRequest) (*associateAttributeGroupResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	ag, aerr := h.store.resolveAttributeGroup(ctx, req.AttributeGroup)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putAttrGroupAssoc(ctx, app.ID, ag.ID); aerr != nil {
		return nil, aerr
	}
	return &associateAttributeGroupResponse{ApplicationArn: app.ARN, AttributeGroupArn: ag.ARN}, nil
}

type disassociateAttributeGroupRequest struct {
	Application    string `json:"Application" cbor:"Application"`
	AttributeGroup string `json:"AttributeGroup" cbor:"AttributeGroup"`
}

type disassociateAttributeGroupResponse struct {
	ApplicationArn    string `json:"ApplicationArn" cbor:"ApplicationArn"`
	AttributeGroupArn string `json:"AttributeGroupArn" cbor:"AttributeGroupArn"`
}

func (h *Handler) disassociateAttributeGroupTyped(ctx context.Context, req *disassociateAttributeGroupRequest) (*disassociateAttributeGroupResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	ag, aerr := h.store.resolveAttributeGroup(ctx, req.AttributeGroup)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteAttrGroupAssoc(ctx, app.ID, ag.ID); aerr != nil {
		return nil, aerr
	}
	return &disassociateAttributeGroupResponse{ApplicationArn: app.ARN, AttributeGroupArn: ag.ARN}, nil
}

type listAssociatedAttributeGroupsRequest struct {
	Application string `json:"Application" cbor:"Application"`
}

type listAssociatedAttributeGroupsResponse struct {
	AttributeGroups []string `json:"AttributeGroups" cbor:"AttributeGroups"`
}

func (h *Handler) listAssociatedAttributeGroupsTyped(ctx context.Context, req *listAssociatedAttributeGroupsRequest) (*listAssociatedAttributeGroupsResponse, *protocol.AWSError) {
	app, aerr := h.store.resolveApplication(ctx, req.Application)
	if aerr != nil {
		return nil, aerr
	}
	ids, aerr := h.store.listAttrGroupAssocs(ctx, app.ID)
	if aerr != nil {
		return nil, aerr
	}
	return &listAssociatedAttributeGroupsResponse{AttributeGroups: ids}, nil
}

// ─── Attribute group typed request/response types ──────────────────────────────

// decodeAttributes accepts either a JSON string or a JSON object for the
// `attributes` field — the SDK may send either depending on client version.
func decodeAttributesTyped(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

type createAttributeGroupRequest struct {
	Name        string            `json:"Name" cbor:"Name"`
	Description string            `json:"Description" cbor:"Description"`
	Attributes  json.RawMessage   `json:"Attributes" cbor:"Attributes"`
	Tags        map[string]string `json:"Tags" cbor:"Tags"`
	ClientToken string            `json:"ClientToken" cbor:"ClientToken"`
}

type createAttributeGroupResponse struct {
	AttributeGroup attributeGroupResponse `json:"AttributeGroup" cbor:"AttributeGroup"`
}

func (h *Handler) createAttributeGroupTyped(ctx context.Context, req *createAttributeGroupRequest) (*createAttributeGroupResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, errInvalidParameter("name is required")
	}
	if existing, _ := h.store.resolveAttributeGroup(ctx, req.Name); existing != nil {
		return nil, errConflict(req.Name)
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
		return nil, aerr
	}
	return &createAttributeGroupResponse{AttributeGroup: toAttributeGroupResponse(ag)}, nil
}

type getAttributeGroupRequest struct {
	AttributeGroup string `json:"AttributeGroup" cbor:"AttributeGroup"`
}

func (h *Handler) getAttributeGroupTyped(ctx context.Context, req *getAttributeGroupRequest) (*attributeGroupResponse, *protocol.AWSError) {
	ag, aerr := h.store.resolveAttributeGroup(ctx, req.AttributeGroup)
	if aerr != nil {
		return nil, aerr
	}
	resp := toAttributeGroupResponse(ag)
	return &resp, nil
}

type listAttributeGroupsRequest struct{}

type listAttributeGroupsResponse struct {
	AttributeGroups []attributeGroupResponse `json:"AttributeGroups" cbor:"AttributeGroups"`
}

func (h *Handler) listAttributeGroupsTyped(ctx context.Context, _ *listAttributeGroupsRequest) (*listAttributeGroupsResponse, *protocol.AWSError) {
	ags, aerr := h.store.listAttributeGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}
	out := make([]attributeGroupResponse, 0, len(ags))
	for i := range ags {
		out = append(out, toAttributeGroupResponse(&ags[i]))
	}
	return &listAttributeGroupsResponse{AttributeGroups: out}, nil
}

type updateAttributeGroupRequest struct {
	AttributeGroup string          `json:"AttributeGroup" cbor:"AttributeGroup"`
	Name           *string         `json:"Name" cbor:"Name"`
	Description    *string         `json:"Description" cbor:"Description"`
	Attributes     json.RawMessage `json:"Attributes" cbor:"Attributes"`
}

type updateAttributeGroupResponse struct {
	AttributeGroup attributeGroupResponse `json:"AttributeGroup" cbor:"AttributeGroup"`
}

func (h *Handler) updateAttributeGroupTyped(ctx context.Context, req *updateAttributeGroupRequest) (*updateAttributeGroupResponse, *protocol.AWSError) {
	ag, aerr := h.store.resolveAttributeGroup(ctx, req.AttributeGroup)
	if aerr != nil {
		return nil, aerr
	}
	if req.Name != nil && *req.Name != "" && *req.Name != ag.Name {
		if existing, _ := h.store.resolveAttributeGroup(ctx, *req.Name); existing != nil && existing.ID != ag.ID {
			return nil, errConflict(*req.Name)
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
		return nil, aerr
	}
	return &updateAttributeGroupResponse{AttributeGroup: toAttributeGroupResponse(ag)}, nil
}

type deleteAttributeGroupRequest struct {
	AttributeGroup string `json:"AttributeGroup" cbor:"AttributeGroup"`
}

type deleteAttributeGroupResponse struct {
	AttributeGroup attributeGroupResponse `json:"AttributeGroup" cbor:"AttributeGroup"`
}

func (h *Handler) deleteAttributeGroupTyped(ctx context.Context, req *deleteAttributeGroupRequest) (*deleteAttributeGroupResponse, *protocol.AWSError) {
	ag, aerr := h.store.resolveAttributeGroup(ctx, req.AttributeGroup)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteAttributeGroup(ctx, ag.ID); aerr != nil {
		return nil, aerr
	}
	return &deleteAttributeGroupResponse{AttributeGroup: toAttributeGroupResponse(ag)}, nil
}
