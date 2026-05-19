package waf

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateWebACL": op.NewTyped[createWebACLRequest, createWebACLResponse](
			"CreateWebACL", h.createWebACLTyped,
		),
		"GetWebACL": op.NewTyped[getWebACLRequest, getWebACLResponse](
			"GetWebACL", h.getWebACLTyped,
		),
		"ListWebACLs": op.NewTyped[listWebACLsRequest, listWebACLsResponse](
			"ListWebACLs", h.listWebACLsTyped,
		),
		"DeleteWebACL": op.NewTyped[deleteWebACLRequest, struct{}](
			"DeleteWebACL", h.deleteWebACLTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
