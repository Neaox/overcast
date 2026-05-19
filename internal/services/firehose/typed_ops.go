package firehose

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateDeliveryStream":   op.NewTyped[createDeliveryStreamReq, createDeliveryStreamResp]("CreateDeliveryStream", s.createDeliveryStreamTyped),
		"DescribeDeliveryStream": op.NewTyped[describeDeliveryStreamReq, describeDeliveryStreamResp]("DescribeDeliveryStream", s.describeDeliveryStreamTyped),
		"ListDeliveryStreams":    op.NewTyped[struct{}, listDeliveryStreamsResp]("ListDeliveryStreams", s.listDeliveryStreamsTyped),
		"DeleteDeliveryStream":   op.NewTyped[deleteDeliveryStreamReq, struct{}]("DeleteDeliveryStream", s.deleteDeliveryStreamTyped),
		"PutRecord":              op.NewTyped[putRecordReq, putRecordResp]("PutRecord", s.putRecordTyped),
		"PutRecordBatch":         op.NewTyped[putRecordBatchReq, putRecordBatchResp]("PutRecordBatch", s.putRecordBatchTyped),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
