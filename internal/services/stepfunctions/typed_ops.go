package stepfunctions

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateStateMachine": op.NewTyped[createStateMachineRequest, createStateMachineResponse](
			"CreateStateMachine", h.createStateMachineTyped,
		),
		"DescribeStateMachine": op.NewTyped[describeStateMachineRequest, describeStateMachineResponse](
			"DescribeStateMachine", h.describeStateMachineTyped,
		),
		"ListStateMachines": op.NewTyped[listStateMachinesRequest, listStateMachinesResponse](
			"ListStateMachines", h.listStateMachinesTyped,
		),
		"StartExecution": op.NewTyped[startExecutionRequest, startExecutionResponse](
			"StartExecution", h.startExecutionTyped,
		),
		"StartSyncExecution": op.NewTyped[startSyncExecutionRequest, startSyncExecutionResponse](
			"StartSyncExecution", h.startSyncExecutionTyped,
		),
		"DescribeExecution": op.NewTyped[describeExecutionRequest, describeExecutionResponse](
			"DescribeExecution", h.describeExecutionTyped,
		),
		"GetExecutionHistory": op.NewTyped[getExecutionHistoryRequest, getExecutionHistoryResponse](
			"GetExecutionHistory", h.getExecutionHistoryTyped,
		),
		"ListExecutions": op.NewTyped[listExecutionsRequest, listExecutionsResponse](
			"ListExecutions", h.listExecutionsTyped,
		),
		"StopExecution": op.NewTyped[stopExecutionRequest, stopExecutionResponse](
			"StopExecution", h.stopExecutionTyped,
		),
		"DescribeStateMachineForExecution": op.NewTyped[describeStateMachineForExecutionRequest, describeStateMachineForExecutionResponse](
			"DescribeStateMachineForExecution", h.describeStateMachineForExecutionTyped,
		),
		"DeleteStateMachine": op.NewTyped[deleteStateMachineRequest, struct{}](
			"DeleteStateMachine", h.deleteStateMachineTyped,
		),
		"TagResource": op.NewTyped[tagResourceRequest, struct{}](
			"TagResource", h.tagResourceTyped,
		),
		"UntagResource": op.NewTyped[untagResourceRequest, struct{}](
			"UntagResource", h.untagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceTypedResponse](
			"ListTagsForResource", h.listTagsForResourceTyped,
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
