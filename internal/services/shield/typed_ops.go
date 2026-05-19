package shield

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"DescribeSubscription": op.NewTyped[struct{}, describeSubscriptionResponse](
			"DescribeSubscription", h.describeSubscriptionTyped,
		),
		"CreateProtection": op.NewTyped[createProtectionRequest, createProtectionResponse](
			"CreateProtection", h.createProtectionTyped,
		),
		"ListProtections": op.NewTyped[listProtectionsRequest, listProtectionsResponse](
			"ListProtections", h.listProtectionsTyped,
		),
		"DeleteProtection": op.NewTyped[deleteProtectionRequest, struct{}](
			"DeleteProtection", h.deleteProtectionTyped,
		),
		"DescribeProtection": op.NewTyped[describeProtectionRequest, describeProtectionResponse](
			"DescribeProtection", h.describeProtectionTyped,
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
