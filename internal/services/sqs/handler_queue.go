package sqs

// handler_queue.go contains handlers for SQS queue lifecycle operations:
// CreateQueue, GetQueueURL, GetQueueAttributes, SetQueueAttributes,
// DeleteQueue, ListQueues, PurgeQueue.

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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

// ---- Typed operations ------------------------------------------------------

func (h *Handler) createQueueTyped(ctx context.Context, in *createQueueRequest) (*createQueueResponse, *protocol.AWSError) {
	if in.QueueName == "" {
		return nil, protocol.ErrMissingParameter("QueueName")
	}

	isFifo := strings.HasSuffix(in.QueueName, ".fifo") || in.Attributes["FifoQueue"] == "true"
	if in.Attributes["FifoQueue"] == "true" && !strings.HasSuffix(in.QueueName, ".fifo") {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "The queue name must end in .fifo for FIFO queues.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	existing, _ := h.store.getQueue(ctx, in.QueueName)
	if existing != nil {
		return &createQueueResponse{QueueUrl: existing.URL}, nil
	}

	queueURL := h.queueURL(in.QueueName)
	attrs := defaultQueueAttributes()
	for k, v := range in.Attributes {
		attrs[k] = v
	}

	if isFifo {
		attrs["FifoQueue"] = "true"
		if _, ok := attrs["ContentBasedDeduplication"]; !ok {
			attrs["ContentBasedDeduplication"] = "false"
		}
	}

	if aerr := h.validateRedrivePolicyContext(ctx, attrs); aerr != nil {
		return nil, aerr
	}

	q := &Queue{
		Name:             in.QueueName,
		URL:              queueURL,
		ARN:              protocol.QueueARN(middleware.RegionFromContext(ctx, h.cfg.Region), h.cfg.AccountID, in.QueueName),
		Attributes:       attrs,
		CreatedTimestamp: h.clk.Now().Unix(),
		Tags:             in.Tags,
	}

	if aerr := h.store.putQueue(ctx, q); aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SQSQueueCreated,
			Time:    h.clk.Now(),
			Source:  "sqs",
			Payload: events.ResourcePayload{Name: in.QueueName},
		})
	}
	return &createQueueResponse{QueueUrl: queueURL}, nil
}

func (h *Handler) getQueueURLTyped(ctx context.Context, in *getQueueURLRequest) (*getQueueURLResponse, *protocol.AWSError) {
	if in.QueueName == "" {
		return nil, protocol.ErrMissingParameter("QueueName")
	}
	q, aerr := h.store.getQueue(ctx, in.QueueName)
	if aerr != nil {
		return nil, aerr
	}
	return &getQueueURLResponse{QueueUrl: q.URL}, nil
}

func (h *Handler) getQueueAttributesTyped(ctx context.Context, in *getQueueAttributesRequest) (*getQueueAttributesResponse, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	msgs, _ := h.store.listMessages(ctx, queueName)
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
	if len(in.AttributeNames) > 0 && in.AttributeNames[0] != "All" {
		filtered := make(map[string]string)
		for _, name := range in.AttributeNames {
			if v, ok := attrs[name]; ok {
				filtered[name] = v
			}
		}
		attrs = filtered
	}

	return &getQueueAttributesResponse{Attributes: attrs}, nil
}

func (h *Handler) setQueueAttributesTyped(ctx context.Context, in *setQueueAttributesRequest) (*struct{}, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	for k, v := range in.Attributes {
		q.Attributes[k] = v
	}

	if aerr := h.validateRedrivePolicyContext(ctx, q.Attributes); aerr != nil {
		return nil, aerr
	}

	if aerr := h.store.putQueue(ctx, q); aerr != nil {
		return nil, aerr
	}

	return &struct{}{}, nil
}

func (h *Handler) deleteQueueTyped(ctx context.Context, in *deleteQueueRequest) (*struct{}, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	if aerr := h.store.deleteQueue(ctx, queueName); aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SQSQueueDeleted,
			Time:    h.clk.Now(),
			Source:  "sqs",
			Payload: events.ResourcePayload{Name: queueName},
		})
	}
	return &struct{}{}, nil
}

// listQueuesTyped is the typed implementation of SQS:ListQueues.
// The legacy h.ListQueues remains below and is removed once the dispatcher path
// is the default (Phase 3).
func (h *Handler) listQueuesTyped(ctx context.Context, in *listQueuesRequest) (*listQueuesResponse, *protocol.AWSError) {
	queues, aerr := h.store.listQueues(ctx, in.QueueNamePrefix)
	if aerr != nil {
		return nil, aerr
	}
	urls := make([]string, len(queues))
	for i, q := range queues {
		urls[i] = q.URL
	}
	return &listQueuesResponse{QueueUrls: urls}, nil
}

func (h *Handler) purgeQueueTyped(ctx context.Context, in *purgeQueueRequest) (*struct{}, *protocol.AWSError) {
	if in.QueueUrl == "" {
		return nil, protocol.ErrMissingParameter("QueueUrl")
	}
	queueName := queueNameFromURL(in.QueueUrl)
	if aerr := h.purgeQueue(ctx, queueName); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) purgeQueue(ctx context.Context, queueName string) *protocol.AWSError {
	if _, aerr := h.store.getQueue(ctx, queueName); aerr != nil {
		return aerr
	}

	// Verified against AWS docs (2026-05-16): PurgeQueue enters a 60-second
	// in-progress window. AWS accepts the purge quickly, and messages sent during
	// the window may be deleted while purging.
	if aerr := h.store.startPurge(ctx, queueName); aerr != nil {
		return aerr
	}
	if aerr := h.store.deleteMessagesByQueuePrefix(ctx, queueName); aerr != nil {
		return aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.SQSQueuePurged,
			Source:  "sqs",
			Payload: events.ResourcePayload{Name: queueName},
		})
	}

	return nil
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

	isFifo := strings.HasSuffix(req.QueueName, ".fifo") || req.Attributes["FifoQueue"] == "true"

	// Validate FIFO naming rules: FifoQueue=true requires .fifo suffix and vice versa.
	if req.Attributes["FifoQueue"] == "true" && !strings.HasSuffix(req.QueueName, ".fifo") {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "The queue name must end in .fifo for FIFO queues.",
			HTTPStatus: http.StatusBadRequest,
		})
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

	// If .fifo suffix, ensure FifoQueue attribute is set.
	if isFifo {
		attrs["FifoQueue"] = "true"
		// Default ContentBasedDeduplication to false if not set.
		if _, ok := attrs["ContentBasedDeduplication"]; !ok {
			attrs["ContentBasedDeduplication"] = "false"
		}
	}

	// Validate RedrivePolicy if provided.
	if aerr := h.validateRedrivePolicy(r, attrs); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	q := &Queue{
		Name:             req.QueueName,
		URL:              queueURL,
		ARN:              protocol.QueueARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, req.QueueName),
		Attributes:       attrs,
		CreatedTimestamp: h.clk.Now().Unix(),
		Tags:             req.Tags,
	}

	if aerr := h.store.putQueue(r.Context(), q); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.SQSQueueCreated,
			Time:    h.clk.Now(),
			Source:  "sqs",
			Payload: events.ResourcePayload{Name: req.QueueName},
		})
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

	// Validate RedrivePolicy if updated.
	if aerr := h.validateRedrivePolicy(r, q.Attributes); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
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

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.SQSQueueDeleted,
			Time:    h.clk.Now(),
			Source:  "sqs",
			Payload: events.ResourcePayload{Name: queueName},
		})
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
	if !serviceutil.RequireString(w, r, req.QueueUrl, "QueueUrl") {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	if aerr := h.purgeQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// ---- Queue tags ------------------------------------------------------------

type tagQueueRequest struct {
	QueueUrl string            `json:"QueueUrl"`
	Tags     map[string]string `json:"Tags"`
}

type untagQueueRequest struct {
	QueueUrl string   `json:"QueueUrl"`
	TagKeys  []string `json:"TagKeys"`
}

type listQueueTagsRequest struct {
	QueueUrl string `json:"QueueUrl"`
}

type listQueueTagsResponse struct {
	Tags map[string]string `json:"Tags"`
}

func (h *Handler) tagQueueTyped(ctx context.Context, in *tagQueueRequest) (*struct{}, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	if q.Tags == nil {
		q.Tags = make(map[string]string)
	}
	for k, v := range in.Tags {
		q.Tags[k] = v
	}

	if aerr := h.store.putQueue(ctx, q); aerr != nil {
		return nil, aerr
	}

	return &struct{}{}, nil
}

func (h *Handler) untagQueueTyped(ctx context.Context, in *untagQueueRequest) (*struct{}, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	for _, k := range in.TagKeys {
		delete(q.Tags, k)
	}

	if aerr := h.store.putQueue(ctx, q); aerr != nil {
		return nil, aerr
	}

	return &struct{}{}, nil
}

func (h *Handler) listQueueTagsTyped(ctx context.Context, in *listQueueTagsRequest) (*listQueueTagsResponse, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	tags := q.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	return &listQueueTagsResponse{Tags: tags}, nil
}

// TagQueue handles the SQS TagQueue operation.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_TagQueue.html
func (h *Handler) TagQueue(w http.ResponseWriter, r *http.Request) {
	var req tagQueueRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	q, aerr := h.store.getQueue(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if q.Tags == nil {
		q.Tags = make(map[string]string)
	}
	for k, v := range req.Tags {
		q.Tags[k] = v
	}

	if aerr := h.store.putQueue(r.Context(), q); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// UntagQueue handles the SQS UntagQueue operation.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_UntagQueue.html
func (h *Handler) UntagQueue(w http.ResponseWriter, r *http.Request) {
	var req untagQueueRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	q, aerr := h.store.getQueue(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	for _, k := range req.TagKeys {
		delete(q.Tags, k)
	}

	if aerr := h.store.putQueue(r.Context(), q); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// ListQueueTags handles the SQS ListQueueTags operation.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueueTags.html
func (h *Handler) ListQueueTags(w http.ResponseWriter, r *http.Request) {
	var req listQueueTagsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	q, aerr := h.store.getQueue(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	tags := q.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	protocol.WriteJSON(w, r, http.StatusOK, &listQueueTagsResponse{Tags: tags})
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
