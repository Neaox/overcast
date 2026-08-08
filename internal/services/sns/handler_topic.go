package sns

// handler_topic.go contains SNS topic lifecycle handlers:
// CreateTopic, DeleteTopic, ListTopics, GetTopicAttributes, SetTopicAttributes,
// TagResource, UntagResource, ListTagsForResource.
//
// Wire protocol: AWS Query (form-encoded POST body, XML responses).

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// snsTagCfg tunes shared tag validation to return SNS-specific error codes.
var snsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "TagLimitExceeded",
	InvalidCode:     "InvalidParameter",
	ExceededMessage: "Can't add more than 50 tags to a topic.",
}

// ---- XML response types ----------------------------------------------------

const snsXMLNS = "http://sns.amazonaws.com/doc/2010-03-31/"

type xmlCreateTopicResponse struct {
	XMLName          xml.Name                  `xml:"CreateTopicResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlCreateTopicResult      `xml:"CreateTopicResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}
type xmlCreateTopicResult struct {
	TopicArn string `xml:"TopicArn"`
}

type xmlDeleteTopicResponse struct {
	XMLName          xml.Name                  `xml:"DeleteTopicResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlListTopicsResponse struct {
	XMLName          xml.Name                  `xml:"ListTopicsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlListTopicsResult       `xml:"ListTopicsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}
type xmlListTopicsResult struct {
	Topics    []xmlTopicMember `xml:"Topics>member"`
	NextToken string           `xml:"NextToken,omitempty"`
}
type xmlTopicMember struct {
	TopicArn string `xml:"TopicArn"`
}

type xmlGetTopicAttributesResponse struct {
	XMLName          xml.Name                    `xml:"GetTopicAttributesResponse"`
	Xmlns            string                      `xml:"xmlns,attr"`
	Result           xmlGetTopicAttributesResult `xml:"GetTopicAttributesResult"`
	ResponseMetadata protocol.ResponseMetadata   `xml:"ResponseMetadata"`
}
type xmlGetTopicAttributesResult struct {
	Attributes []xmlAttributeEntry `xml:"Attributes>entry"`
}
type xmlAttributeEntry struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type xmlSetTopicAttributesResponse struct {
	XMLName          xml.Name                  `xml:"SetTopicAttributesResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

// ---- Handlers --------------------------------------------------------------

// CreateTopic handles SNS CreateTopic. Idempotent — returns existing ARN if topic exists.
func (h *Handler) CreateTopic(w http.ResponseWriter, r *http.Request) {
	name, ok := h.requireForm(w, r, "Name")
	if !ok {
		return
	}

	// Idempotent: return existing topic if it already exists.
	if existing, _ := h.snsStore.getTopic(r.Context(), name); existing != nil {
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateTopicResponse{
			Xmlns:            snsXMLNS,
			Result:           xmlCreateTopicResult{TopicArn: existing.ARN},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	arn := protocol.TopicARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, name)
	attrs := map[string]string{
		"TopicArn":                arn,
		"SubscriptionsConfirmed":  "0",
		"SubscriptionsPending":    "0",
		"SubscriptionsDeleted":    "0",
		"EffectiveDeliveryPolicy": `{"defaultHealthyRetryPolicy":{"minDelayTarget":20,"maxDelayTarget":20,"numRetries":3,"numMaxDelayRetries":0,"numNoDelayRetries":0,"numMinDelayRetries":0,"backoffFunction":"linear"},"sicklyRetryPolicy":null,"throttlePolicy":null,"guaranteed":false}`,
		"DisplayName":             name,
		"Policy":                  "",
		"DeliveryPolicy":          "",
		"Owner":                   h.cfg.AccountID,
	}

	topic := &Topic{
		Name:             name,
		ARN:              arn,
		Attributes:       attrs,
		CreatedTimestamp: h.clk.Now().Unix(),
	}
	if aerr := h.snsStore.putTopic(r.Context(), topic); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.SNSTopicCreated,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: name, ARN: arn},
		})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateTopicResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlCreateTopicResult{TopicArn: arn},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// DeleteTopic handles SNS DeleteTopic.
func (h *Handler) DeleteTopic(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}

	topic, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.snsStore.deleteTopic(r.Context(), topic.Name); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.SNSTopicDeleted,
			Time:    h.clk.Now(),
			Source:  "sns",
			Payload: events.ResourcePayload{Name: topic.Name, ARN: topic.ARN},
		})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteTopicResponse{
		Xmlns:            snsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ListTopics handles SNS ListTopics.
func (h *Handler) ListTopics(w http.ResponseWriter, r *http.Request) {
	topics, aerr := h.snsStore.listTopics(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	members := make([]xmlTopicMember, 0, len(topics))
	for _, t := range topics {
		members = append(members, xmlTopicMember{TopicArn: t.ARN})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlListTopicsResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlListTopicsResult{Topics: members},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// GetTopicAttributes handles SNS GetTopicAttributes.
func (h *Handler) GetTopicAttributes(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}
	topic, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	entries := make([]xmlAttributeEntry, 0, len(topic.Attributes))
	for k, v := range topic.Attributes {
		entries = append(entries, xmlAttributeEntry{Key: k, Value: v})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlGetTopicAttributesResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlGetTopicAttributesResult{Attributes: entries},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// SetTopicAttributes handles SNS SetTopicAttributes.
func (h *Handler) SetTopicAttributes(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}
	attrName := r.FormValue("AttributeName")
	attrValue := r.FormValue("AttributeValue")

	topic, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if topic.Attributes == nil {
		topic.Attributes = map[string]string{}
	}
	if attrName != "" {
		topic.Attributes[attrName] = attrValue
	}
	if aerr := h.snsStore.putTopic(r.Context(), topic); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlSetTopicAttributesResponse{
		Xmlns:            snsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ---- Tag XML response types -------------------------------------------------

type xmlTagMember struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// The empty result elements are required by botocore — see the comment on
// tagResourceResp in typed_logic.go, which serves the same envelope on the
// typed dispatch path.
type xmlTagResourceResponse struct {
	XMLName          xml.Name                  `xml:"TagResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"TagResourceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlUntagResourceResponse struct {
	XMLName          xml.Name                  `xml:"UntagResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"UntagResourceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlListTagsForResourceResponse struct {
	XMLName          xml.Name                     `xml:"ListTagsForResourceResponse"`
	Xmlns            string                       `xml:"xmlns,attr"`
	Result           xmlListTagsForResourceResult `xml:"ListTagsForResourceResult"`
	ResponseMetadata protocol.ResponseMetadata    `xml:"ResponseMetadata"`
}

type xmlListTagsForResourceResult struct {
	Tags []xmlTagMember `xml:"Tags>member"`
}

// ---- Tag handlers -----------------------------------------------------------

// TagResource handles SNS TagResource.
// Tags arrive as Tags.Tag.N.Key / Tags.Tag.N.Value (1-indexed, the member
// locationName is "Tag" in the SNS model).
func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, ok := h.requireForm(w, r, "ResourceArn")
	if !ok {
		return
	}

	incoming := make(map[string]string)
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.Tag.%d.Key", i))
		if key == "" {
			break
		}
		incoming[key] = r.FormValue(fmt.Sprintf("Tags.Tag.%d.Value", i))
	}

	if aerr := serviceutil.ApplyInlineTags(r.Context(), resourceArn, incoming, snsTagCfg,
		func(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
			return h.snsStore.getTopicByARNForTagging(ctx, arn)
		},
		func(ctx context.Context, t *Topic) *protocol.AWSError { return h.snsStore.putTopic(ctx, t) },
	); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlTagResourceResponse{
		Xmlns:            snsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// UntagResource handles SNS UntagResource.
// Tag keys arrive as TagKeys.member.N (1-indexed).
func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, ok := h.requireForm(w, r, "ResourceArn")
	if !ok {
		return
	}

	var tagKeys []string
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		tagKeys = append(tagKeys, key)
	}

	if aerr := serviceutil.RemoveInlineTags(r.Context(), resourceArn, tagKeys,
		func(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
			return h.snsStore.getTopicByARNForTagging(ctx, arn)
		},
		func(ctx context.Context, t *Topic) *protocol.AWSError { return h.snsStore.putTopic(ctx, t) },
	); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlUntagResourceResponse{
		Xmlns:            snsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ListTagsForResource handles SNS ListTagsForResource.
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, ok := h.requireForm(w, r, "ResourceArn")
	if !ok {
		return
	}

	tags, aerr := serviceutil.ListInlineTags(r.Context(), resourceArn,
		func(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
			return h.snsStore.getTopicByARNForTagging(ctx, arn)
		},
	)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	members := make([]xmlTagMember, 0, len(tags))
	for k, v := range tags {
		members = append(members, xmlTagMember{Key: k, Value: v})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlListTagsForResourceResponse{
		Xmlns: snsXMLNS,
		Result: xmlListTagsForResourceResult{
			Tags: members,
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}
