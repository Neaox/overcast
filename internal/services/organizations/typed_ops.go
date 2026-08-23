package organizations

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

// typedOps is the hand-written operation table — the one §4.5 gives absolute
// precedence to. Keep it and handWrittenOps in agreement;
// TestHandWrittenOps_MatchTheTypedTable holds that.
func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"DescribeOrganization": op.NewTyped[describeOrganizationRequest, describeOrganizationResponse](
			"DescribeOrganization", s.describeOrganizationTyped,
		),
	}
}

// Operations reports every operation this service implements, hand-written
// and Tier 1 alike.
//
// Both halves are load-bearing: the router's Smithy RPC v2 entry dispatches
// only an operation this list names (see smithyRPCService.supports), so
// omitting the inert bindings would leave them reachable over JSON but 501
// over CBOR — one of the "implementation mounted where no client will send a
// request" shapes docs/plans/route-reachability-audit.md exists to catch.
func (s *Service) Operations() []op.Operation {
	out := make([]op.Operation, 0, len(s.typedOp)+len(s.bindings))
	for _, operation := range s.typedOp {
		out = append(out, operation)
	}
	for _, binding := range s.bindings {
		out = append(out, binding.Invoke)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
