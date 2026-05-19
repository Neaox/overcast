package sns

// handler_topic.go contains SNS topic lifecycle handlers:
// CreateTopic, DeleteTopic, ListTopics, GetTopicAttributes, SetTopicAttributes.
//
// Wire protocol: AWS Query (form-encoded POST body, XML responses).

import (
	"encoding/xml"
	"net/http"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

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
			Payload: events.ResourcePayload{Name: name},
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
			Payload: events.ResourcePayload{Name: topic.Name},
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
