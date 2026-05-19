package sns

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateTopic":               op.NewTyped[createTopicReq, createTopicResp]("CreateTopic", h.createTopicTyped),
		"DeleteTopic":               op.NewTyped[deleteTopicReq, deleteTopicResp]("DeleteTopic", h.deleteTopicTyped),
		"ListTopics":                op.NewTyped[struct{}, listTopicsResp]("ListTopics", h.listTopicsTyped),
		"GetTopicAttributes":        op.NewTyped[getTopicAttributesReq, getTopicAttributesResp]("GetTopicAttributes", h.getTopicAttributesTyped),
		"SetTopicAttributes":        op.NewTyped[setTopicAttributesReq, setTopicAttributesResp]("SetTopicAttributes", h.setTopicAttributesTyped),
		"Subscribe":                 op.NewTyped[subscribeReq, subscribeResp]("Subscribe", h.subscribeTyped),
		"Unsubscribe":               op.NewTyped[unsubscribeReq, unsubscribeResp]("Unsubscribe", h.unsubscribeTyped),
		"ListSubscriptionsByTopic":  op.NewTyped[listSubscriptionsByTopicReq, listSubscriptionsByTopicResp]("ListSubscriptionsByTopic", h.listSubscriptionsByTopicTyped),
		"ListSubscriptions":         op.NewTyped[struct{}, listSubscriptionsResp]("ListSubscriptions", h.listSubscriptionsTyped),
		"GetSubscriptionAttributes": op.NewTyped[getSubscriptionAttributesReq, getSubscriptionAttributesResp]("GetSubscriptionAttributes", h.getSubscriptionAttributesTyped),
		"SetSubscriptionAttributes": op.NewTyped[setSubscriptionAttributesReq, setSubscriptionAttributesResp]("SetSubscriptionAttributes", h.setSubscriptionAttributesTyped),
		"ConfirmSubscription":       op.NewTyped[confirmSubscriptionReq, confirmSubscriptionResp]("ConfirmSubscription", h.confirmSubscriptionTyped),
		"Publish":                   op.NewTyped[publishReq, publishResp]("Publish", h.publishTyped),
		"PublishBatch":              op.NewTyped[publishBatchReq, publishBatchResp]("PublishBatch", h.publishBatchTyped),
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
	return []codec.Codec{codec.QueryXML}
}
