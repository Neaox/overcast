package logs

import (
	"context"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateLogGroup": op.NewTyped[createLogGroupRequest, struct{}](
			"CreateLogGroup", h.createLogGroupTyped,
		),
		"DescribeLogGroups": op.NewTyped[describeLogGroupsRequest, describeLogGroupsResponse](
			"DescribeLogGroups", h.describeLogGroupsTyped,
		),
		"DeleteLogGroup": op.NewTyped[deleteLogGroupRequest, struct{}](
			"DeleteLogGroup", h.deleteLogGroupTyped,
		),
		"CreateLogStream": op.NewTyped[createLogStreamRequest, struct{}](
			"CreateLogStream", h.createLogStreamTyped,
		),
		"DescribeLogStreams": op.NewTyped[describeLogStreamsRequest, describeLogStreamsResponse](
			"DescribeLogStreams", h.describeLogStreamsTyped,
		),
		"DeleteLogStream": op.NewTyped[deleteLogStreamRequest, struct{}](
			"DeleteLogStream", h.deleteLogStreamTyped,
		),
		"PutLogEvents": op.NewTyped[putLogEventsRequest, putLogEventsResponse](
			"PutLogEvents", h.putLogEventsTyped,
		),
		"GetLogEvents": op.NewTyped[getLogEventsRequest, getLogEventsResponse](
			"GetLogEvents", h.getLogEventsTyped,
		),
		"FilterLogEvents": op.NewTyped[filterLogEventsRequest, filterLogEventsResponse](
			"FilterLogEvents", h.filterLogEventsTyped,
		),
		"PutRetentionPolicy": op.NewTyped[putRetentionPolicyRequest, struct{}](
			"PutRetentionPolicy", h.putRetentionPolicyTyped,
		),
		"DeleteRetentionPolicy": op.NewTyped[deleteRetentionPolicyRequest, struct{}](
			"DeleteRetentionPolicy", h.deleteRetentionPolicyTyped,
		),
		"TagLogGroup": op.NewTyped[tagLogGroupRequest, struct{}](
			"TagLogGroup", h.tagLogGroupTyped,
		),
		"UntagLogGroup": op.NewTyped[untagLogGroupRequest, struct{}](
			"UntagLogGroup", h.untagLogGroupTyped,
		),
		"ListTagsLogGroup": op.NewTyped[listTagsLogGroupRequest, listTagsLogGroupResponse](
			"ListTagsLogGroup", h.listTagsLogGroupTyped,
		),
		"PutSubscriptionFilter": unsupportedOperation("PutSubscriptionFilter"),
		"StartQuery":            unsupportedOperation("StartQuery"),
		"GetQueryResults":       unsupportedOperation("GetQueryResults"),
		"PutMetricFilter":       unsupportedOperation("PutMetricFilter"),
	}
}

func unsupportedOperation(name string) op.Operation {
	return op.NewTyped[struct{}, struct{}](
		name,
		func(_ context.Context, _ *struct{}) (*struct{}, *protocol.AWSError) {
			return nil, protocol.ErrNotImplemented
		},
	)
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
