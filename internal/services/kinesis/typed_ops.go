package kinesis

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateStream": op.NewTyped[createStreamRequest, struct{}](
			"CreateStream", h.createStreamTyped,
		),
		"DeleteStream": op.NewTyped[deleteStreamRequest, struct{}](
			"DeleteStream", h.deleteStreamTyped,
		),
		"DescribeStream": op.NewTyped[describeStreamRequest, describeStreamResponse](
			"DescribeStream", h.describeStreamTyped,
		),
		"DescribeStreamSummary": op.NewTyped[describeStreamSummaryRequest, describeStreamSummaryResponse](
			"DescribeStreamSummary", h.describeStreamSummaryTyped,
		),
		"ListStreams": op.NewTyped[listStreamsRequest, listStreamsResponse](
			"ListStreams", h.listStreamsTyped,
		),
		"PutRecord": op.NewTyped[putRecordRequest, putRecordResponse](
			"PutRecord", h.putRecordTyped,
		),
		"PutRecords": op.NewTyped[putRecordsRequest, putRecordsResponse](
			"PutRecords", h.putRecordsTyped,
		),
		"GetShardIterator": op.NewTyped[getShardIteratorRequest, getShardIteratorResponse](
			"GetShardIterator", h.getShardIteratorTyped,
		),
		"GetRecords": op.NewTyped[getRecordsRequest, getRecordsResponse](
			"GetRecords", h.getRecordsTyped,
		),
		"ListShards": op.NewTyped[listShardsRequest, listShardsResponse](
			"ListShards", h.listShardsTyped,
		),
		"SplitShard": op.NewTyped[splitShardRequest, struct{}](
			"SplitShard", h.splitShardTyped,
		),
		"MergeShards": op.NewTyped[mergeShardsRequest, struct{}](
			"MergeShards", h.mergeShardsTyped,
		),
		"AddTagsToStream": op.NewTyped[addTagsToStreamRequest, struct{}](
			"AddTagsToStream", h.addTagsToStreamTyped,
		),
		"ListTagsForStream": op.NewTyped[listTagsForStreamRequest, listTagsForStreamResponse](
			"ListTagsForStream", h.listTagsForStreamTyped,
		),
		"RemoveTagsFromStream": op.NewTyped[removeTagsFromStreamRequest, struct{}](
			"RemoveTagsFromStream", h.removeTagsFromStreamTyped,
		),
		"IncreaseStreamRetentionPeriod": op.NewTyped[retentionPeriodRequest, struct{}](
			"IncreaseStreamRetentionPeriod", h.increaseStreamRetentionPeriodTyped,
		),
		"DecreaseStreamRetentionPeriod": op.NewTyped[retentionPeriodRequest, struct{}](
			"DecreaseStreamRetentionPeriod", h.decreaseStreamRetentionPeriodTyped,
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
