package dynamodbstreams

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"ListStreams": op.NewTyped[listStreamsRequest, listStreamsResponse](
			"ListStreams", h.listStreamsTyped,
		),
		"DescribeStream": op.NewTyped[describeStreamRequest, describeStreamResponse](
			"DescribeStream", h.describeStreamTyped,
		),
		"GetShardIterator": op.NewTyped[getShardIteratorRequest, getShardIteratorResponse](
			"GetShardIterator", h.getShardIteratorTyped,
		),
		"GetRecords": op.NewTyped[getRecordsRequest, getRecordsResponse](
			"GetRecords", h.getRecordsTyped,
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
