package cloudformation

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateStack":            op.NewTyped[createStackReq, createStackResp]("CreateStack", h.createStackTyped),
		"UpdateStack":            op.NewTyped[updateStackReq, updateStackResp]("UpdateStack", h.updateStackTyped),
		"DeleteStack":            op.NewTyped[deleteStackReq, struct{}]("DeleteStack", h.deleteStackTyped),
		"DescribeStacks":         op.NewTyped[describeStacksReq, describeStacksResp]("DescribeStacks", h.describeStacksTyped),
		"ListStacks":             op.NewTyped[listStacksReq, listStacksResp]("ListStacks", h.listStacksTyped),
		"GetTemplate":            op.NewTyped[getTemplateReq, getTemplateResp]("GetTemplate", h.getTemplateTyped),
		"CreateChangeSet":        op.NewTyped[createChangeSetReq, createChangeSetResp]("CreateChangeSet", h.createChangeSetTyped),
		"DescribeChangeSet":      op.NewTyped[describeChangeSetReq, describeChangeSetResp]("DescribeChangeSet", h.describeChangeSetTyped),
		"ExecuteChangeSet":       op.NewTyped[executeChangeSetReq, struct{}]("ExecuteChangeSet", h.executeChangeSetTyped),
		"DeleteChangeSet":        op.NewTyped[deleteChangeSetReq, struct{}]("DeleteChangeSet", h.deleteChangeSetTyped),
		"ListChangeSets":         op.NewTyped[listChangeSetsReq, listChangeSetsResp]("ListChangeSets", h.listChangeSetsTyped),
		"DescribeStackResources": op.NewTyped[describeStackResourcesReq, describeStackResourcesResp]("DescribeStackResources", h.describeStackResourcesTyped),
		"ListStackResources":     op.NewTyped[listStackResourcesReq, listStackResourcesResp]("ListStackResources", h.listStackResourcesTyped),
		"DescribeStackEvents":    op.NewTyped[describeStackEventsReq, describeStackEventsResp]("DescribeStackEvents", h.describeStackEventsTyped),
		"GetTemplateSummary":     op.NewTyped[getTemplateSummaryReq, getTemplateSummaryResp]("GetTemplateSummary", h.getTemplateSummaryTyped),
		"ValidateTemplate":       op.NewTyped[validateTemplateReq, validateTemplateResp]("ValidateTemplate", h.validateTemplateTyped),
		"ListExports":            op.NewTyped[struct{}, listExportsResp]("ListExports", h.listExportsTyped),
		"ListImports":            op.NewTyped[listImportsReq, listImportsResp]("ListImports", h.listImportsTyped),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}
