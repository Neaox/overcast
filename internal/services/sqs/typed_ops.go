package sqs

// typed_ops.go contains the Smithy-aligned typed operation registry for SQS.
// Operations migrated to the typed dispatcher live here; legacy http.HandlerFunc
// entries remain in handler.go's ops map until they are migrated. See
// docs/plans/smithy.md §5 Phase 2 for the per-op migration checklist.

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

// typedOps returns the typed operation registry for SQS, keyed by AWS
// operation name. Built once at handler construction.
func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateQueue": op.NewTyped[createQueueRequest, createQueueResponse](
			"CreateQueue", h.createQueueTyped,
		),
		"GetQueueUrl": op.NewTyped[getQueueURLRequest, getQueueURLResponse](
			"GetQueueUrl", h.getQueueURLTyped,
		),
		"GetQueueAttributes": op.NewTyped[getQueueAttributesRequest, getQueueAttributesResponse](
			"GetQueueAttributes", h.getQueueAttributesTyped,
		),
		"SetQueueAttributes": op.NewTyped[setQueueAttributesRequest, struct{}](
			"SetQueueAttributes", h.setQueueAttributesTyped,
		),
		"DeleteQueue": op.NewTyped[deleteQueueRequest, struct{}](
			"DeleteQueue", h.deleteQueueTyped,
		),
		"ListQueues": op.NewTyped[listQueuesRequest, listQueuesResponse](
			"ListQueues", h.listQueuesTyped,
		),
		"PurgeQueue": op.NewTyped[purgeQueueRequest, struct{}](
			"PurgeQueue", h.purgeQueueTyped,
		),
		"SendMessage": op.NewTyped[sendMessageRequest, sendMessageResponse](
			"SendMessage", h.sendMessageTyped,
		),
		"ReceiveMessage": op.NewTyped[receiveMessageRequest, receiveMessageResponse](
			"ReceiveMessage", h.receiveMessageTyped,
		),
		"DeleteMessage": op.NewTyped[deleteMessageRequest, struct{}](
			"DeleteMessage", h.deleteMessageTyped,
		),
		"SendMessageBatch": op.NewTyped[sendMessageBatchRequest, sendMessageBatchResponse](
			"SendMessageBatch", h.sendMessageBatchTyped,
		),
		"DeleteMessageBatch": op.NewTyped[deleteMessageBatchRequest, deleteMessageBatchResponse](
			"DeleteMessageBatch", h.deleteMessageBatchTyped,
		),
		"ChangeMessageVisibility": op.NewTyped[changeMessageVisibilityRequest, struct{}](
			"ChangeMessageVisibility", h.changeMessageVisibilityTyped,
		),
		"ChangeMessageVisibilityBatch": op.NewTyped[changeMessageVisibilityBatchRequest, changeMessageVisibilityBatchResponse](
			"ChangeMessageVisibilityBatch", h.changeMessageVisibilityBatchTyped,
		),
		"ListDeadLetterSourceQueues": op.NewTyped[listDeadLetterSourceQueuesRequest, listDeadLetterSourceQueuesResponse](
			"ListDeadLetterSourceQueues", h.listDeadLetterSourceQueuesTyped,
		),
		"StartMessageMoveTask": op.NewTyped[startMessageMoveTaskRequest, startMessageMoveTaskResponse](
			"StartMessageMoveTask", h.startMessageMoveTaskTyped,
		),
		"AddPermission": op.NewTyped[struct{}, struct{}](
			"AddPermission", h.addPermissionTyped,
		),
		"RemovePermission": op.NewTyped[struct{}, struct{}](
			"RemovePermission", h.removePermissionTyped,
		),
		"ListQueueTags": op.NewTyped[listQueueTagsRequest, listQueueTagsResponse](
			"ListQueueTags", h.listQueueTagsTyped,
		),
		"TagQueue": op.NewTyped[tagQueueRequest, struct{}](
			"TagQueue", h.tagQueueTyped,
		),
		"UntagQueue": op.NewTyped[untagQueueRequest, struct{}](
			"UntagQueue", h.untagQueueTyped,
		),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOps()
	out := make([]op.Operation, 0, len(ops))
	for _, o := range ops {
		out = append(out, o)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService. SQS speaks JSON 1.0
// (modern SDKs) and AWS Query (legacy SDKs / form-encoded). The query path
// still goes through the legacy form decoder until Phase 6.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR, codec.QueryXML}
}
