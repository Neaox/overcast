package ssm

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"PutParameter": op.NewTyped[putParameterRequest, putParameterResponse](
			"PutParameter", h.putParameterTyped,
		),
		"GetParameter": op.NewTyped[getParameterRequest, getParameterResponse](
			"GetParameter", h.getParameterTyped,
		),
		"GetParameters": op.NewTyped[getParametersRequest, getParametersResponse](
			"GetParameters", h.getParametersTyped,
		),
		"GetParametersByPath": op.NewTyped[getParametersByPathRequest, parametersPageResponse](
			"GetParametersByPath", h.getParametersByPathTyped,
		),
		"DescribeParameters": op.NewTyped[describeParametersRequest, describeParametersResponse](
			"DescribeParameters", h.describeParametersTyped,
		),
		"GetParameterHistory": op.NewTyped[getParameterHistoryRequest, parameterHistoryResponse](
			"GetParameterHistory", h.getParameterHistoryTyped,
		),
		"AddTagsToResource": op.NewTyped[addTagsToResourceRequest, struct{}](
			"AddTagsToResource", h.addTagsToResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[resourceIDRequest, listTagsForResourceResponse](
			"ListTagsForResource", h.listTagsForResourceTyped,
		),
		"DeleteParameter": op.NewTyped[deleteParameterRequest, struct{}](
			"DeleteParameter", h.deleteParameterTyped,
		),
		"DeleteParameters": op.NewTyped[deleteParametersRequest, deleteParametersResponse](
			"DeleteParameters", h.deleteParametersTyped,
		),
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
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
