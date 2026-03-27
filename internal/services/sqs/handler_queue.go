package sqs

// handler_queue.go contains handlers for SQS queue lifecycle operations:
// CreateQueue, GetQueueURL, GetQueueAttributes, SetQueueAttributes,
// DeleteQueue, ListQueues, PurgeQueue.

import (
	"net/http"
	"strconv"

	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
)

// ---- Request / response types ----------------------------------------------

type createQueueRequest struct {
	QueueName  string            `json:"QueueName"`
	Attributes map[string]string `json:"Attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type createQueueResponse struct {
	QueueUrl string `json:"QueueUrl"`
}

type getQueueURLRequest struct {
	QueueName              string `json:"QueueName"`
	QueueOwnerAWSAccountId string `json:"QueueOwnerAWSAccountId,omitempty"`
}

type getQueueURLResponse struct {
	QueueUrl string `json:"QueueUrl"`
}

type getQueueAttributesRequest struct {
	QueueUrl       string   `json:"QueueUrl"`
	AttributeNames []string `json:"AttributeNames"`
}

type getQueueAttributesResponse struct {
	Attributes map[string]string `json:"Attributes"`
}

type setQueueAttributesRequest struct {
	QueueUrl   string            `json:"QueueUrl"`
	Attributes map[string]string `json:"Attributes"`
}

type deleteQueueRequest struct {
	QueueUrl string `json:"QueueUrl"`
}

type listQueuesRequest struct {
	QueueNamePrefix string `json:"QueueNamePrefix,omitempty"`
	MaxResults      int    `json:"MaxResults,omitempty"`
}

type listQueuesResponse struct {
	QueueUrls []string `json:"QueueUrls"`
}

type purgeQueueRequest struct {
	QueueUrl string `json:"QueueUrl"`
}

// ---- Handlers --------------------------------------------------------------

func (h *Handler) CreateQueue(w http.ResponseWriter, r *http.Request) {
	var req createQueueRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.QueueName, "QueueName") {
		return
	}

	// Check for existing queue.
	existing, _ := h.store.getQueue(r.Context(), req.QueueName)
	if existing != nil {
		// AWS returns the existing queue URL (idempotent if attributes match).
		protocol.WriteJSON(w, r, http.StatusOK, &createQueueResponse{QueueUrl: existing.URL})
		return
	}

	queueURL := h.queueURL(req.QueueName)
	attrs := defaultQueueAttributes()
	for k, v := range req.Attributes {
		attrs[k] = v
	}

	q := &Queue{
		Name:             req.QueueName,
		URL:              queueURL,
		ARN:              protocol.QueueARN(h.cfg.Region, h.cfg.AccountID, req.QueueName),
		Attributes:       attrs,
		CreatedTimestamp: h.clk.Now().Unix(),
		Tags:             req.Tags,
	}

	if aerr := h.store.putQueue(r.Context(), q); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, &createQueueResponse{QueueUrl: queueURL})
}

func (h *Handler) GetQueueURL(w http.ResponseWriter, r *http.Request) {
	var req getQueueURLRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.QueueName, "QueueName") {
		return
	}

	q, aerr := h.store.getQueue(r.Context(), req.QueueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, &getQueueURLResponse{QueueUrl: q.URL})
}

func (h *Handler) GetQueueAttributes(w http.ResponseWriter, r *http.Request) {
	var req getQueueAttributesRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	q, aerr := h.store.getQueue(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Count visible messages for ApproximateNumberOfMessages.
	msgs, _ := h.store.listMessages(r.Context(), queueName)
	visibleCount := 0
	for _, m := range msgs {
		if m.IsVisible(h.clk) {
			visibleCount++
		}
	}
	q.Attributes["ApproximateNumberOfMessages"] = strconv.Itoa(visibleCount)
	q.Attributes["ApproximateNumberOfMessagesNotVisible"] = strconv.Itoa(len(msgs) - visibleCount)
	q.Attributes["QueueArn"] = q.ARN

	attrs := q.Attributes
	// If specific attributes are requested, filter to those.
	if len(req.AttributeNames) > 0 && req.AttributeNames[0] != "All" {
		filtered := make(map[string]string)
		for _, name := range req.AttributeNames {
			if v, ok := attrs[name]; ok {
				filtered[name] = v
			}
		}
		attrs = filtered
	}

	protocol.WriteJSON(w, r, http.StatusOK, &getQueueAttributesResponse{Attributes: attrs})
}

func (h *Handler) SetQueueAttributes(w http.ResponseWriter, r *http.Request) {
	var req setQueueAttributesRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	q, aerr := h.store.getQueue(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	for k, v := range req.Attributes {
		q.Attributes[k] = v
	}

	if aerr := h.store.putQueue(r.Context(), q); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

func (h *Handler) DeleteQueue(w http.ResponseWriter, r *http.Request) {
	var req deleteQueueRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	if aerr := h.store.deleteQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

func (h *Handler) ListQueues(w http.ResponseWriter, r *http.Request) {
	var req listQueuesRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queues, aerr := h.store.listQueues(r.Context(), req.QueueNamePrefix)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	urls := make([]string, len(queues))
	for i, q := range queues {
		urls[i] = q.URL
	}

	protocol.WriteJSON(w, r, http.StatusOK, &listQueuesResponse{QueueUrls: urls})
}

func (h *Handler) PurgeQueue(w http.ResponseWriter, r *http.Request) {
	var req purgeQueueRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	if _, aerr := h.store.getQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	msgs, aerr := h.store.listMessages(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	for _, msg := range msgs {
		_ = h.store.deleteMessage(r.Context(), queueName, msg.MessageID)
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// ---- Helpers ---------------------------------------------------------------

// defaultQueueAttributes returns the AWS default queue attribute values.
func defaultQueueAttributes() map[string]string {
	return map[string]string{
		"VisibilityTimeout":             "30",
		"MaximumMessageSize":            "262144",
		"MessageRetentionPeriod":        "345600",
		"DelaySeconds":                  "0",
		"ReceiveMessageWaitTimeSeconds": "0",
	}
}
