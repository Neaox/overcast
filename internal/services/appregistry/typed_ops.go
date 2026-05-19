package appregistry

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	h := s.handler
	return map[string]op.Operation{
		"CreateApplication": op.NewTyped[createApplicationRequest, createApplicationResponse](
			"CreateApplication", h.createApplicationTyped,
		),
		"GetApplication": op.NewTyped[getApplicationRequest, getApplicationResponse](
			"GetApplication", h.getApplicationTyped,
		),
		"ListApplications": op.NewTyped[listApplicationsRequest, listApplicationsResponse](
			"ListApplications", h.listApplicationsTyped,
		),
		"DeleteApplication": op.NewTyped[deleteApplicationRequest, deleteApplicationResponse](
			"DeleteApplication", h.deleteApplicationTyped,
		),
		"UpdateApplication": op.NewTyped[updateApplicationRequest, updateApplicationResponse](
			"UpdateApplication", h.updateApplicationTyped,
		),
		"AssociateResource": op.NewTyped[associateResourceRequest, associateResourceResponse](
			"AssociateResource", h.associateResourceTyped,
		),
		"DisassociateResource": op.NewTyped[disassociateResourceRequest, disassociateResourceResponse](
			"DisassociateResource", h.disassociateResourceTyped,
		),
		"ListAssociatedResources": op.NewTyped[listAssociatedResourcesRequest, listAssociatedResourcesResponse](
			"ListAssociatedResources", h.listAssociatedResourcesTyped,
		),
		"GetAssociatedResource": op.NewTyped[getAssociatedResourceRequest, getAssociatedResourceResponse](
			"GetAssociatedResource", h.getAssociatedResourceTyped,
		),
		"AssociateAttributeGroup": op.NewTyped[associateAttributeGroupRequest, associateAttributeGroupResponse](
			"AssociateAttributeGroup", h.associateAttributeGroupTyped,
		),
		"DisassociateAttributeGroup": op.NewTyped[disassociateAttributeGroupRequest, disassociateAttributeGroupResponse](
			"DisassociateAttributeGroup", h.disassociateAttributeGroupTyped,
		),
		"ListAssociatedAttributeGroups": op.NewTyped[listAssociatedAttributeGroupsRequest, listAssociatedAttributeGroupsResponse](
			"ListAssociatedAttributeGroups", h.listAssociatedAttributeGroupsTyped,
		),
		"CreateAttributeGroup": op.NewTyped[createAttributeGroupRequest, createAttributeGroupResponse](
			"CreateAttributeGroup", h.createAttributeGroupTyped,
		),
		"GetAttributeGroup": op.NewTyped[getAttributeGroupRequest, attributeGroupResponse](
			"GetAttributeGroup", h.getAttributeGroupTyped,
		),
		"ListAttributeGroups": op.NewTyped[listAttributeGroupsRequest, listAttributeGroupsResponse](
			"ListAttributeGroups", h.listAttributeGroupsTyped,
		),
		"UpdateAttributeGroup": op.NewTyped[updateAttributeGroupRequest, updateAttributeGroupResponse](
			"UpdateAttributeGroup", h.updateAttributeGroupTyped,
		),
		"DeleteAttributeGroup": op.NewTyped[deleteAttributeGroupRequest, deleteAttributeGroupResponse](
			"DeleteAttributeGroup", h.deleteAttributeGroupTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
