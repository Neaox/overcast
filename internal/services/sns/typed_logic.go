package sns

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/smtp"
)

// ---- Request types (json tags used by codec.Decode for form mapping) ----

type createTopicReq struct {
	Name string     `json:"Name"`
	Tags []tagEntry `json:"Tags"`
}

type deleteTopicReq struct {
	TopicArn string `json:"TopicArn"`
}

type getTopicAttributesReq struct {
	TopicArn string `json:"TopicArn"`
}

type setTopicAttributesReq struct {
	TopicArn       string `json:"TopicArn"`
	AttributeName  string `json:"AttributeName"`
	AttributeValue string `json:"AttributeValue"`
}

type subscribeReq struct {
	TopicArn string `json:"TopicArn"`
	Protocol string `json:"Protocol"`
	Endpoint string `json:"Endpoint"`
}

type unsubscribeReq struct {
	SubscriptionArn string `json:"SubscriptionArn"`
}

type listSubscriptionsByTopicReq struct {
	TopicArn string `json:"TopicArn"`
}

type getSubscriptionAttributesReq struct {
	SubscriptionArn string `json:"SubscriptionArn"`
}

type setSubscriptionAttributesReq struct {
	SubscriptionArn string `json:"SubscriptionArn"`
	AttributeName   string `json:"AttributeName"`
	AttributeValue  string `json:"AttributeValue"`
}

type confirmSubscriptionReq struct {
	TopicArn string `json:"TopicArn"`
	Token    string `json:"Token"`
}

type publishReq struct {
	TopicArn string `json:"TopicArn"`
	Message  string `json:"Message"`
	Subject  string `json:"Subject"`
}

type publishBatchEntry struct {
	Id      string `json:"Id"`
	Message string `json:"Message"`
	Subject string `json:"Subject"`
}

type publishBatchReq struct {
	TopicArn string              `json:"TopicArn"`
	Entries  []publishBatchEntry `json:"PublishBatchRequestEntries"`
}

type tagResourceReq struct {
	ResourceArn string     `json:"ResourceArn"`
	Tags        []tagEntry `json:"Tags"`
}

type tagEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type untagResourceReq struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

type listTagsForResourceReq struct {
	ResourceArn string `json:"ResourceArn"`
}

// ---- Response types (xml tags for QueryXML codec WriteResponse) ----

type snsRespMeta struct {
	RequestId string `xml:"RequestId"`
}

func snsMetaFromCtx(ctx context.Context) snsRespMeta {
	return snsRespMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}

// Topic responses

type createTopicResp struct {
	XMLName struct{}          `xml:"CreateTopicResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  createTopicResult `xml:"CreateTopicResult"`
	Meta    snsRespMeta       `xml:"ResponseMetadata"`
}

type createTopicResult struct {
	TopicArn string `xml:"TopicArn"`
}

type deleteTopicResp struct {
	XMLName struct{}    `xml:"DeleteTopicResponse"`
	Xmlns   string      `xml:"xmlns,attr"`
	Meta    snsRespMeta `xml:"ResponseMetadata"`
}

type listTopicsResp struct {
	XMLName struct{}         `xml:"ListTopicsResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  listTopicsResult `xml:"ListTopicsResult"`
	Meta    snsRespMeta      `xml:"ResponseMetadata"`
}

type listTopicsResult struct {
	Topics    []xmlTopicMember `xml:"Topics>member"`
	NextToken string           `xml:"NextToken,omitempty"`
}

type getTopicAttributesResp struct {
	XMLName struct{}                 `xml:"GetTopicAttributesResponse"`
	Xmlns   string                   `xml:"xmlns,attr"`
	Result  getTopicAttributesResult `xml:"GetTopicAttributesResult"`
	Meta    snsRespMeta              `xml:"ResponseMetadata"`
}

type getTopicAttributesResult struct {
	Attributes []xmlAttributeEntry `xml:"Attributes>entry"`
}

type setTopicAttributesResp struct {
	XMLName struct{}    `xml:"SetTopicAttributesResponse"`
	Xmlns   string      `xml:"xmlns,attr"`
	Meta    snsRespMeta `xml:"ResponseMetadata"`
}

// Subscription responses

type subscribeResp struct {
	XMLName struct{}        `xml:"SubscribeResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  subscribeResult `xml:"SubscribeResult"`
	Meta    snsRespMeta     `xml:"ResponseMetadata"`
}

type subscribeResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}

type unsubscribeResp struct {
	XMLName struct{}    `xml:"UnsubscribeResponse"`
	Xmlns   string      `xml:"xmlns,attr"`
	Meta    snsRespMeta `xml:"ResponseMetadata"`
}

type listSubscriptionsByTopicResp struct {
	XMLName struct{}                       `xml:"ListSubscriptionsByTopicResponse"`
	Xmlns   string                         `xml:"xmlns,attr"`
	Result  listSubscriptionsByTopicResult `xml:"ListSubscriptionsByTopicResult"`
	Meta    snsRespMeta                    `xml:"ResponseMetadata"`
}

type listSubscriptionsByTopicResult struct {
	Subscriptions []xmlSubscriptionMember `xml:"Subscriptions>member"`
	NextToken     string                  `xml:"NextToken,omitempty"`
}

type listSubscriptionsResp struct {
	XMLName struct{}                `xml:"ListSubscriptionsResponse"`
	Xmlns   string                  `xml:"xmlns,attr"`
	Result  listSubscriptionsResult `xml:"ListSubscriptionsResult"`
	Meta    snsRespMeta             `xml:"ResponseMetadata"`
}

type listSubscriptionsResult struct {
	Subscriptions []xmlSubscriptionMember `xml:"Subscriptions>member"`
	NextToken     string                  `xml:"NextToken,omitempty"`
}

type getSubscriptionAttributesResp struct {
	XMLName struct{}                        `xml:"GetSubscriptionAttributesResponse"`
	Xmlns   string                          `xml:"xmlns,attr"`
	Result  getSubscriptionAttributesResult `xml:"GetSubscriptionAttributesResult"`
	Meta    snsRespMeta                     `xml:"ResponseMetadata"`
}

type getSubscriptionAttributesResult struct {
	Attributes []xmlAttributeEntry `xml:"Attributes>entry"`
}

type setSubscriptionAttributesResp struct {
	XMLName struct{}    `xml:"SetSubscriptionAttributesResponse"`
	Xmlns   string      `xml:"xmlns,attr"`
	Meta    snsRespMeta `xml:"ResponseMetadata"`
}

type confirmSubscriptionResp struct {
	XMLName struct{}                  `xml:"ConfirmSubscriptionResponse"`
	Xmlns   string                    `xml:"xmlns,attr"`
	Result  confirmSubscriptionResult `xml:"ConfirmSubscriptionResult"`
	Meta    snsRespMeta               `xml:"ResponseMetadata"`
}

type confirmSubscriptionResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}

// Publish responses

type publishResp struct {
	XMLName struct{}      `xml:"PublishResponse"`
	Xmlns   string        `xml:"xmlns,attr"`
	Result  publishResult `xml:"PublishResult"`
	Meta    snsRespMeta   `xml:"ResponseMetadata"`
}

type publishResult struct {
	MessageId string `xml:"MessageId"`
}

type publishBatchResp struct {
	XMLName struct{}           `xml:"PublishBatchResponse"`
	Xmlns   string             `xml:"xmlns,attr"`
	Result  publishBatchResult `xml:"PublishBatchResult"`
	Meta    snsRespMeta        `xml:"ResponseMetadata"`
}

type publishBatchResult struct {
	Successful []xmlPublishBatchSuccess `xml:"Successful>member"`
	Failed     []xmlPublishBatchFailed  `xml:"Failed>member"`
}

// Tag responses
//
// TagResource and UntagResource have an (empty) output shape in the SNS
// model, so botocore requires the empty <TagResourceResult/> /
// <UntagResourceResult/> element before ResponseMetadata — without it the
// AWS CLI fails client-side even though the operation succeeded. Operations
// with no output shape at all (e.g. SetTopicAttributes) correctly omit the
// element.

type tagResourceResp struct {
	XMLName struct{}    `xml:"TagResourceResponse"`
	Xmlns   string      `xml:"xmlns,attr"`
	Result  struct{}    `xml:"TagResourceResult"`
	Meta    snsRespMeta `xml:"ResponseMetadata"`
}

type untagResourceResp struct {
	XMLName struct{}    `xml:"UntagResourceResponse"`
	Xmlns   string      `xml:"xmlns,attr"`
	Result  struct{}    `xml:"UntagResourceResult"`
	Meta    snsRespMeta `xml:"ResponseMetadata"`
}

type listTagsForResourceResp struct {
	XMLName struct{}                  `xml:"ListTagsForResourceResponse"`
	Xmlns   string                    `xml:"xmlns,attr"`
	Result  listTagsForResourceResult `xml:"ListTagsForResourceResult"`
	Meta    snsRespMeta               `xml:"ResponseMetadata"`
}

type listTagsForResourceResult struct {
	Tags []xmlTagMemberTyped `xml:"Tags>member"`
}

type xmlTagMemberTyped struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// ---- Typed handler functions ----

func (h *Handler) createTopicTyped(ctx context.Context, req *createTopicReq) (*createTopicResp, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("Name")
	}

	// Idempotent: AWS returns the existing topic's ARN without creating a new
	// topic, and leaves its tags alone — a repeat CreateTopic is not a retag.
	if existing, _ := h.snsStore.getTopic(ctx, req.Name); existing != nil {
		return &createTopicResp{
			Xmlns:  snsXMLNS,
			Result: createTopicResult{TopicArn: existing.ARN},
			Meta:   snsMetaFromCtx(ctx),
		}, nil
	}

	tags, aerr := createTopicTags(req.Tags)
	if aerr != nil {
		return nil, aerr
	}

	arn := protocol.TopicARN(middleware.RegionFromContext(ctx, h.cfg.Region), h.cfg.AccountID, req.Name)
	attrs := map[string]string{
		"TopicArn":                arn,
		"SubscriptionsConfirmed":  "0",
		"SubscriptionsPending":    "0",
		"SubscriptionsDeleted":    "0",
		"EffectiveDeliveryPolicy": `{"defaultHealthyRetryPolicy":{"minDelayTarget":20,"maxDelayTarget":20,"numRetries":3,"numMaxDelayRetries":0,"numNoDelayRetries":0,"numMinDelayRetries":0,"backoffFunction":"linear"},"sicklyRetryPolicy":null,"throttlePolicy":null,"guaranteed":false}`,
		"DisplayName":             req.Name,
		"Policy":                  "",
		"DeliveryPolicy":          "",
		"Owner":                   h.cfg.AccountID,
	}

	topic := &Topic{
		Name:             req.Name,
		ARN:              arn,
		Attributes:       attrs,
		Tags:             tags,
		CreatedTimestamp: h.clk.Now().Unix(),
	}
	if aerr := h.snsStore.putTopic(ctx, topic); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SNSTopicCreated,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: req.Name, ARN: arn},
		})
	}
	return &createTopicResp{
		Xmlns:  snsXMLNS,
		Result: createTopicResult{TopicArn: arn},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) deleteTopicTyped(ctx context.Context, req *deleteTopicReq) (*deleteTopicResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}

	topic, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.snsStore.deleteTopic(ctx, topic.Name); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SNSTopicDeleted,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: topic.Name, ARN: topic.ARN},
		})
	}
	return &deleteTopicResp{
		Xmlns: snsXMLNS,
		Meta:  snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) listTopicsTyped(ctx context.Context, _ *struct{}) (*listTopicsResp, *protocol.AWSError) {
	topics, aerr := h.snsStore.listTopics(ctx)
	if aerr != nil {
		return nil, aerr
	}
	members := make([]xmlTopicMember, 0, len(topics))
	for _, t := range topics {
		members = append(members, xmlTopicMember{TopicArn: t.ARN})
	}
	return &listTopicsResp{
		Xmlns:  snsXMLNS,
		Result: listTopicsResult{Topics: members},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) getTopicAttributesTyped(ctx context.Context, req *getTopicAttributesReq) (*getTopicAttributesResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}
	topic, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn)
	if aerr != nil {
		return nil, aerr
	}
	entries := make([]xmlAttributeEntry, 0, len(topic.Attributes))
	for k, v := range topic.Attributes {
		entries = append(entries, xmlAttributeEntry{Key: k, Value: v})
	}
	return &getTopicAttributesResp{
		Xmlns:  snsXMLNS,
		Result: getTopicAttributesResult{Attributes: entries},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) setTopicAttributesTyped(ctx context.Context, req *setTopicAttributesReq) (*setTopicAttributesResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}

	topic, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn)
	if aerr != nil {
		return nil, aerr
	}
	if topic.Attributes == nil {
		topic.Attributes = map[string]string{}
	}
	if req.AttributeName != "" {
		topic.Attributes[req.AttributeName] = req.AttributeValue
	}
	if aerr := h.snsStore.putTopic(ctx, topic); aerr != nil {
		return nil, aerr
	}
	return &setTopicAttributesResp{
		Xmlns: snsXMLNS,
		Meta:  snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) subscribeTyped(ctx context.Context, req *subscribeReq) (*subscribeResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}
	if req.Protocol == "" {
		return nil, protocol.ErrMissingParameter("Protocol")
	}
	if req.Endpoint == "" {
		return nil, protocol.ErrMissingParameter("Endpoint")
	}

	topic, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn)
	if aerr != nil {
		return nil, aerr
	}

	switch strings.ToLower(req.Protocol) {
	case "application", "firehose":
		return nil, errInvalidProtocol(req.Protocol)
	}

	// Real AWS requires the subscription endpoint to be in the same region as
	// the topic (applies to sqs and lambda protocols which use ARN endpoints).
	switch strings.ToLower(req.Protocol) {
	case "sqs", "lambda":
		if endpointRegion := serviceutil.ARNRegion(req.Endpoint); endpointRegion != "" {
			if topicRegion := serviceutil.ARNRegion(req.TopicArn); topicRegion != "" && topicRegion != endpointRegion {
				return nil, &protocol.AWSError{
					Code:       "InvalidParameter",
					Message:    "Invalid parameter: SQS endpoint ARN must be in the same region as the SNS topic.",
					HTTPStatus: http.StatusBadRequest,
				}
			}
		}
	}

	subID := uuid.New().String()
	subARN := req.TopicArn + ":" + subID

	queueName := ""
	if strings.EqualFold(req.Protocol, "sqs") {
		queueName = queueNameFromARN(req.Endpoint)
	}

	sub := &Subscription{
		SubscriptionARN: subARN,
		TopicARN:        req.TopicArn,
		TopicName:       topic.Name,
		Protocol:        strings.ToLower(req.Protocol),
		Endpoint:        req.Endpoint,
		QueueName:       queueName,
		Owner:           h.cfg.AccountID,
	}
	if aerr := h.snsStore.putSubscription(ctx, sub); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SNSSubscriptionCreated,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: subARN, ARN: subARN},
		})
	}
	return &subscribeResp{
		Xmlns:  snsXMLNS,
		Result: subscribeResult{SubscriptionArn: subARN},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) unsubscribeTyped(ctx context.Context, req *unsubscribeReq) (*unsubscribeResp, *protocol.AWSError) {
	if req.SubscriptionArn == "" {
		return nil, protocol.ErrMissingParameter("SubscriptionArn")
	}
	if aerr := h.snsStore.deleteSubscription(ctx, req.SubscriptionArn); aerr != nil {
		return nil, aerr
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SNSSubscriptionDeleted,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: req.SubscriptionArn, ARN: req.SubscriptionArn},
		})
	}
	return &unsubscribeResp{
		Xmlns: snsXMLNS,
		Meta:  snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) listSubscriptionsByTopicTyped(ctx context.Context, req *listSubscriptionsByTopicReq) (*listSubscriptionsByTopicResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}

	topicName := topicNameFromARN(req.TopicArn)
	subs, aerr := h.snsStore.listSubscriptionsByTopic(ctx, topicName)
	if aerr != nil {
		return nil, aerr
	}

	members := make([]xmlSubscriptionMember, 0, len(subs))
	for _, s := range subs {
		members = append(members, xmlSubscriptionMember{
			SubscriptionArn: s.SubscriptionARN,
			Owner:           s.Owner,
			Protocol:        s.Protocol,
			Endpoint:        s.Endpoint,
			TopicArn:        s.TopicARN,
		})
	}
	return &listSubscriptionsByTopicResp{
		Xmlns:  snsXMLNS,
		Result: listSubscriptionsByTopicResult{Subscriptions: members},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) listSubscriptionsTyped(ctx context.Context, _ *struct{}) (*listSubscriptionsResp, *protocol.AWSError) {
	subs, aerr := h.snsStore.listAllSubscriptions(ctx)
	if aerr != nil {
		return nil, aerr
	}
	members := make([]xmlSubscriptionMember, 0, len(subs))
	for _, s := range subs {
		members = append(members, xmlSubscriptionMember{
			SubscriptionArn: s.SubscriptionARN,
			Owner:           s.Owner,
			Protocol:        s.Protocol,
			Endpoint:        s.Endpoint,
			TopicArn:        s.TopicARN,
		})
	}
	return &listSubscriptionsResp{
		Xmlns:  snsXMLNS,
		Result: listSubscriptionsResult{Subscriptions: members},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) getSubscriptionAttributesTyped(ctx context.Context, req *getSubscriptionAttributesReq) (*getSubscriptionAttributesResp, *protocol.AWSError) {
	if req.SubscriptionArn == "" {
		return nil, protocol.ErrMissingParameter("SubscriptionArn")
	}

	sub, aerr := h.snsStore.getSubscriptionByARN(ctx, req.SubscriptionArn)
	if aerr != nil {
		return nil, aerr
	}

	attrs := map[string]string{
		"SubscriptionArn": sub.SubscriptionARN,
		"TopicArn":        sub.TopicARN,
		"Protocol":        sub.Protocol,
		"Endpoint":        sub.Endpoint,
		"Owner":           sub.Owner,
	}
	for k, v := range sub.Attributes {
		attrs[k] = v
	}

	entries := make([]xmlAttributeEntry, 0, len(attrs))
	for k, v := range attrs {
		entries = append(entries, xmlAttributeEntry{Key: k, Value: v})
	}

	return &getSubscriptionAttributesResp{
		Xmlns:  snsXMLNS,
		Result: getSubscriptionAttributesResult{Attributes: entries},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) setSubscriptionAttributesTyped(ctx context.Context, req *setSubscriptionAttributesReq) (*setSubscriptionAttributesResp, *protocol.AWSError) {
	if req.SubscriptionArn == "" {
		return nil, protocol.ErrMissingParameter("SubscriptionArn")
	}
	if req.AttributeName == "" {
		return nil, protocol.ErrMissingParameter("AttributeName")
	}

	sub, aerr := h.snsStore.getSubscriptionByARN(ctx, req.SubscriptionArn)
	if aerr != nil {
		return nil, aerr
	}

	if sub.Attributes == nil {
		sub.Attributes = make(map[string]string)
	}
	sub.Attributes[req.AttributeName] = req.AttributeValue

	if aerr := h.snsStore.putSubscription(ctx, sub); aerr != nil {
		return nil, aerr
	}

	return &setSubscriptionAttributesResp{
		Xmlns: snsXMLNS,
		Meta:  snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) confirmSubscriptionTyped(ctx context.Context, req *confirmSubscriptionReq) (*confirmSubscriptionResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}
	if _, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn); aerr != nil {
		return nil, aerr
	}
	subARN := req.TopicArn + ":confirmed"

	return &confirmSubscriptionResp{
		Xmlns:  snsXMLNS,
		Result: confirmSubscriptionResult{SubscriptionArn: subARN},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) publishTyped(ctx context.Context, req *publishReq) (*publishResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}
	if req.Message == "" {
		return nil, protocol.ErrMissingParameter("Message")
	}

	topic, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn)
	if aerr != nil {
		return nil, aerr
	}

	msgID := uuid.New().String()
	envelope := snsNotificationEnvelope{
		Type:             "Notification",
		MessageId:        msgID,
		TopicArn:         topic.ARN,
		Subject:          req.Subject,
		Message:          req.Message,
		Timestamp:        h.clk.Now().UTC().Format(snsTimestampFormat),
		SignatureVersion: "1",
		Signature:        "EXAMPLE",
		SigningCertURL:   "EXAMPLE",
	}

	subs, aerr := h.snsStore.listSubscriptionsByTopic(ctx, topic.Name)
	if aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:   events.SNSMessagePublished,
			Time:   h.clk.Now(),
			Source: "sns",
			Payload: events.SNSPublishPayload{
				TopicName: topic.Name,
				MessageID: msgID,
			},
		})
	}

	origin := publishOrigin(ctx)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.fanOut(context.WithoutCancel(ctx), origin, topic.Name, msgID, req.Subject, req.Message, envelope, subs, nil, nil)
	}()

	return &publishResp{
		Xmlns:  snsXMLNS,
		Result: publishResult{MessageId: msgID},
		Meta:   snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) publishBatchTyped(ctx context.Context, req *publishBatchReq) (*publishBatchResp, *protocol.AWSError) {
	if req.TopicArn == "" {
		return nil, protocol.ErrMissingParameter("TopicArn")
	}

	topic, aerr := h.snsStore.getTopicByARN(ctx, req.TopicArn)
	if aerr != nil {
		return nil, aerr
	}

	subs, _ := h.snsStore.listSubscriptionsByTopic(ctx, topic.Name)
	origin := publishOrigin(ctx)

	var successful []xmlPublishBatchSuccess
	var failed []xmlPublishBatchFailed

	for _, entry := range req.Entries {
		msgID := uuid.New().String()
		envelope := snsNotificationEnvelope{
			Type:             "Notification",
			MessageId:        msgID,
			TopicArn:         topic.ARN,
			Subject:          entry.Subject,
			Message:          entry.Message,
			Timestamp:        h.clk.Now().UTC().Format(snsTimestampFormat),
			SignatureVersion: "1",
			Signature:        "EXAMPLE",
			SigningCertURL:   "EXAMPLE",
		}

		h.wg.Add(1)
		envCopy := envelope
		go func() {
			defer h.wg.Done()
			h.fanOut(context.WithoutCancel(ctx), origin, topic.Name, msgID, entry.Subject, entry.Message, envCopy, subs, nil, nil)
		}()

		successful = append(successful, xmlPublishBatchSuccess{Id: entry.Id, MessageId: msgID})
	}

	return &publishBatchResp{
		Xmlns: snsXMLNS,
		Result: publishBatchResult{
			Successful: successful,
			Failed:     failed,
		},
		Meta: snsMetaFromCtx(ctx),
	}, nil
}

var _ = xml.Marshal       // keep xml import
var _ = smtp.BuildMessage // keep smtp import via handler_publish use

// ---- Tag typed handlers -----------------------------------------------------

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceReq) (*tagResourceResp, *protocol.AWSError) {
	if req.ResourceArn == "" {
		return nil, protocol.ErrMissingParameter("ResourceArn")
	}

	incoming := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		incoming[t.Key] = t.Value
	}

	if aerr := serviceutil.ApplyInlineTags(ctx, req.ResourceArn, incoming, snsTagCfg,
		func(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
			return h.snsStore.getTopicByARNForTagging(ctx, arn)
		},
		func(ctx context.Context, t *Topic) *protocol.AWSError { return h.snsStore.putTopic(ctx, t) },
	); aerr != nil {
		return nil, aerr
	}

	return &tagResourceResp{
		Xmlns: snsXMLNS,
		Meta:  snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceReq) (*untagResourceResp, *protocol.AWSError) {
	if req.ResourceArn == "" {
		return nil, protocol.ErrMissingParameter("ResourceArn")
	}

	if aerr := serviceutil.RemoveInlineTags(ctx, req.ResourceArn, req.TagKeys,
		func(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
			return h.snsStore.getTopicByARNForTagging(ctx, arn)
		},
		func(ctx context.Context, t *Topic) *protocol.AWSError { return h.snsStore.putTopic(ctx, t) },
	); aerr != nil {
		return nil, aerr
	}

	return &untagResourceResp{
		Xmlns: snsXMLNS,
		Meta:  snsMetaFromCtx(ctx),
	}, nil
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceReq) (*listTagsForResourceResp, *protocol.AWSError) {
	if req.ResourceArn == "" {
		return nil, protocol.ErrMissingParameter("ResourceArn")
	}

	tags, aerr := serviceutil.ListInlineTags(ctx, req.ResourceArn,
		func(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
			return h.snsStore.getTopicByARNForTagging(ctx, arn)
		},
	)
	if aerr != nil {
		return nil, aerr
	}

	members := serviceutil.TagElements(tags, func(k, v string) xmlTagMemberTyped {
		return xmlTagMemberTyped{Key: k, Value: v}
	})

	return &listTagsForResourceResp{
		Xmlns: snsXMLNS,
		Result: listTagsForResourceResult{
			Tags: members,
		},
		Meta: snsMetaFromCtx(ctx),
	}, nil
}
