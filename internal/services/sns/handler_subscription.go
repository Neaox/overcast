package sns

// handler_subscription.go contains SNS subscription handlers:
// Subscribe, Unsubscribe, ListSubscriptionsByTopic, ListSubscriptions.
//
// Wire protocol: AWS Query (form-encoded POST body, XML responses).

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- XML response types ----------------------------------------------------

type xmlSubscribeResponse struct {
	XMLName          xml.Name                  `xml:"SubscribeResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlSubscribeResult        `xml:"SubscribeResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}
type xmlSubscribeResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}

type xmlUnsubscribeResponse struct {
	XMLName          xml.Name                  `xml:"UnsubscribeResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlListSubscriptionsByTopicResponse struct {
	XMLName          xml.Name                          `xml:"ListSubscriptionsByTopicResponse"`
	Xmlns            string                            `xml:"xmlns,attr"`
	Result           xmlListSubscriptionsByTopicResult `xml:"ListSubscriptionsByTopicResult"`
	ResponseMetadata protocol.ResponseMetadata         `xml:"ResponseMetadata"`
}
type xmlListSubscriptionsByTopicResult struct {
	Subscriptions []xmlSubscriptionMember `xml:"Subscriptions>member"`
	NextToken     string                  `xml:"NextToken,omitempty"`
}

type xmlListSubscriptionsResponse struct {
	XMLName          xml.Name                   `xml:"ListSubscriptionsResponse"`
	Xmlns            string                     `xml:"xmlns,attr"`
	Result           xmlListSubscriptionsResult `xml:"ListSubscriptionsResult"`
	ResponseMetadata protocol.ResponseMetadata  `xml:"ResponseMetadata"`
}
type xmlListSubscriptionsResult struct {
	Subscriptions []xmlSubscriptionMember `xml:"Subscriptions>member"`
	NextToken     string                  `xml:"NextToken,omitempty"`
}

type xmlSubscriptionMember struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
	Owner           string `xml:"Owner"`
	Protocol        string `xml:"Protocol"`
	Endpoint        string `xml:"Endpoint"`
	TopicArn        string `xml:"TopicArn"`
}

// ---- Handlers --------------------------------------------------------------

// Subscribe handles SNS Subscribe.
// SQS subscriptions auto-confirm — no confirmation flow needed.
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}
	proto, ok := h.requireForm(w, r, "Protocol")
	if !ok {
		return
	}
	endpoint, ok := h.requireForm(w, r, "Endpoint")
	if !ok {
		return
	}

	topic, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// application and firehose require infrastructure (mobile push / Kinesis
	// Data Firehose) that Overcast cannot emulate.
	switch strings.ToLower(proto) {
	case "application", "firehose":
		protocol.WriteQueryXMLError(w, r, errInvalidProtocol(proto))
		return
	}

	// Real AWS requires the subscription endpoint to be in the same region as
	// the topic (applies to sqs and lambda protocols which use ARN endpoints).
	switch strings.ToLower(proto) {
	case "sqs", "lambda":
		if endpointRegion := serviceutil.ARNRegion(endpoint); endpointRegion != "" {
			if topicRegion := serviceutil.ARNRegion(topicArn); topicRegion != "" && topicRegion != endpointRegion {
				protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
					Code:       "InvalidParameter",
					Message:    "Invalid parameter: SQS endpoint ARN must be in the same region as the SNS topic.",
					HTTPStatus: http.StatusBadRequest,
				})
				return
			}
		}
	}

	subID := uuid.New().String()
	subARN := topicArn + ":" + subID

	queueName := ""
	if strings.EqualFold(proto, "sqs") {
		queueName = queueNameFromARN(endpoint)
	}

	sub := &Subscription{
		SubscriptionARN: subARN,
		TopicARN:        topicArn,
		TopicName:       topic.Name,
		Protocol:        strings.ToLower(proto),
		Endpoint:        endpoint,
		QueueName:       queueName,
		Owner:           h.cfg.AccountID,
	}
	if aerr := h.snsStore.putSubscription(r.Context(), sub); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.SNSSubscriptionCreated,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: subARN},
		})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlSubscribeResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlSubscribeResult{SubscriptionArn: subARN},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// Unsubscribe handles SNS Unsubscribe.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	subArn, ok := h.requireForm(w, r, "SubscriptionArn")
	if !ok {
		return
	}
	if aerr := h.snsStore.deleteSubscription(r.Context(), subArn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.SNSSubscriptionDeleted,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: subArn},
		})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlUnsubscribeResponse{
		Xmlns:            snsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ListSubscriptionsByTopic handles SNS ListSubscriptionsByTopic.
func (h *Handler) ListSubscriptionsByTopic(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}

	topicName := topicNameFromARN(topicArn)
	subs, aerr := h.snsStore.listSubscriptionsByTopic(r.Context(), topicName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
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
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlListSubscriptionsByTopicResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlListSubscriptionsByTopicResult{Subscriptions: members},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ListSubscriptions handles SNS ListSubscriptions.
func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, aerr := h.snsStore.listAllSubscriptions(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
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
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlListSubscriptionsResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlListSubscriptionsResult{Subscriptions: members},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ---- GetSubscriptionAttributes / SetSubscriptionAttributes / ConfirmSubscription ----

type xmlGetSubscriptionAttributesResponse struct {
	XMLName          xml.Name                           `xml:"GetSubscriptionAttributesResponse"`
	Xmlns            string                             `xml:"xmlns,attr"`
	Result           xmlGetSubscriptionAttributesResult `xml:"GetSubscriptionAttributesResult"`
	ResponseMetadata protocol.ResponseMetadata          `xml:"ResponseMetadata"`
}
type xmlGetSubscriptionAttributesResult struct {
	Attributes []xmlAttributeEntry `xml:"Attributes>entry"`
}

type xmlSetSubscriptionAttributesResponse struct {
	XMLName          xml.Name                  `xml:"SetSubscriptionAttributesResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlConfirmSubscriptionResponse struct {
	XMLName          xml.Name                     `xml:"ConfirmSubscriptionResponse"`
	Xmlns            string                       `xml:"xmlns,attr"`
	Result           xmlConfirmSubscriptionResult `xml:"ConfirmSubscriptionResult"`
	ResponseMetadata protocol.ResponseMetadata    `xml:"ResponseMetadata"`
}
type xmlConfirmSubscriptionResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}

// GetSubscriptionAttributes returns a subscription's attributes as a key/value map.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_GetSubscriptionAttributes.html
func (h *Handler) GetSubscriptionAttributes(w http.ResponseWriter, r *http.Request) {
	subArn, ok := h.requireForm(w, r, "SubscriptionArn")
	if !ok {
		return
	}

	sub, aerr := h.snsStore.getSubscriptionByARN(r.Context(), subArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Build attribute map from known fields + stored Attributes overrides.
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

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlGetSubscriptionAttributesResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlGetSubscriptionAttributesResult{Attributes: entries},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// SetSubscriptionAttributes sets a single attribute on a subscription.
// The most important attribute is FilterPolicy (JSON string).
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_SetSubscriptionAttributes.html
func (h *Handler) SetSubscriptionAttributes(w http.ResponseWriter, r *http.Request) {
	subArn, ok := h.requireForm(w, r, "SubscriptionArn")
	if !ok {
		return
	}
	attrName, ok := h.requireForm(w, r, "AttributeName")
	if !ok {
		return
	}
	attrValue := r.FormValue("AttributeValue")

	sub, aerr := h.snsStore.getSubscriptionByARN(r.Context(), subArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if sub.Attributes == nil {
		sub.Attributes = make(map[string]string)
	}
	sub.Attributes[attrName] = attrValue

	if aerr := h.snsStore.putSubscription(r.Context(), sub); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlSetSubscriptionAttributesResponse{
		Xmlns:            snsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ConfirmSubscription handles SNS ConfirmSubscription.
// The emulator auto-confirms all subscriptions — no token validation is performed.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_ConfirmSubscription.html
func (h *Handler) ConfirmSubscription(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}
	// Validate the topic exists.
	if _, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// The emulator doesn't track pending confirmations — return a synthetic ARN.
	subARN := topicArn + ":confirmed"

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlConfirmSubscriptionResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlConfirmSubscriptionResult{SubscriptionArn: subARN},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}
