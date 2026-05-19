package cloudtrail

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateTrail": op.NewTyped[createTrailInput, createTrailOutput](
			"CreateTrail", h.createTrailTyped,
		),
		"DescribeTrails": op.NewTyped[describeTrailsRequest, describeTrailsResponse](
			"DescribeTrails", h.describeTrailsTyped,
		),
		"UpdateTrail": op.NewTyped[updateTrailInput, createTrailOutput](
			"UpdateTrail", h.updateTrailTyped,
		),
		"DeleteTrail": op.NewTyped[deleteTrailRequest, struct{}](
			"DeleteTrail", h.deleteTrailTyped,
		),
		"ListTrails": op.NewTyped[struct{}, listTrailsResponse](
			"ListTrails", h.listTrailsTyped,
		),
		"GetTrailStatus": op.NewTyped[getTrailStatusRequest, getTrailStatusResponse](
			"GetTrailStatus", h.getTrailStatusTyped,
		),
		"StartLogging": op.NewTyped[loggingRequest, struct{}](
			"StartLogging", h.startLoggingTyped,
		),
		"StopLogging": op.NewTyped[loggingRequest, struct{}](
			"StopLogging", h.stopLoggingTyped,
		),
		"LookupEvents": op.NewTyped[struct{}, lookupEventsResponse](
			"LookupEvents", h.lookupEventsTyped,
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
