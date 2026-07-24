package sqs

// handler_message.go contains handlers for SQS message operations:
// SendMessage, ReceiveMessage, DeleteMessage, SendMessageBatch, DeleteMessageBatch.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

const maxMessageBodyBytes = 1_048_576 // 1 MiB

// ---- Request / response types ----------------------------------------------

type sendMessageRequest struct {
	QueueUrl               string                      `json:"QueueUrl"`
	MessageBody            string                      `json:"MessageBody"`
	DelaySeconds           int                         `json:"DelaySeconds,omitempty"`
	MessageAttributes      map[string]MessageAttribute `json:"MessageAttributes,omitempty"`
	MessageDeduplicationId string                      `json:"MessageDeduplicationId,omitempty"`
	MessageGroupId         string                      `json:"MessageGroupId,omitempty"`
}

type sendMessageResponse struct {
	MessageId        string `json:"MessageId"`
	MD5OfMessageBody string `json:"MD5OfMessageBody"`
	SequenceNumber   string `json:"SequenceNumber,omitempty"` // FIFO only
}

type receiveMessageRequest struct {
	QueueUrl                    string   `json:"QueueUrl"`
	MaxNumberOfMessages         *int     `json:"MaxNumberOfMessages,omitempty"`
	VisibilityTimeout           *int     `json:"VisibilityTimeout,omitempty"`
	WaitTimeSeconds             *int     `json:"WaitTimeSeconds,omitempty"`
	ReceiveRequestAttemptId     string   `json:"ReceiveRequestAttemptId,omitempty"`
	AttributeNames              []string `json:"AttributeNames,omitempty"`
	MessageSystemAttributeNames []string `json:"MessageSystemAttributeNames,omitempty"`
	MessageAttributeNames       []string `json:"MessageAttributeNames,omitempty"`
}

type receiveMessageResponse struct {
	Messages []receivedMessage `json:"Messages,omitempty"`
}

type receivedMessage struct {
	MessageId         string                      `json:"MessageId"`
	ReceiptHandle     string                      `json:"ReceiptHandle"`
	MD5OfBody         string                      `json:"MD5OfBody"`
	Body              string                      `json:"Body"`
	Attributes        map[string]string           `json:"Attributes,omitempty"`
	MessageAttributes map[string]MessageAttribute `json:"MessageAttributes,omitempty"`
	visibilityVersion int
}

type deleteMessageRequest struct {
	QueueUrl      string `json:"QueueUrl"`
	ReceiptHandle string `json:"ReceiptHandle"`
}

type sendMessageBatchRequestEntry struct {
	Id                string                      `json:"Id"`
	MessageBody       string                      `json:"MessageBody"`
	DelaySeconds      int                         `json:"DelaySeconds,omitempty"`
	MessageAttributes map[string]MessageAttribute `json:"MessageAttributes,omitempty"`
}

type sendMessageBatchRequest struct {
	QueueUrl string                         `json:"QueueUrl"`
	Entries  []sendMessageBatchRequestEntry `json:"Entries"`
}

type sendMessageBatchResultEntry struct {
	Id               string `json:"Id"`
	MessageId        string `json:"MessageId"`
	MD5OfMessageBody string `json:"MD5OfMessageBody"`
}

type sendMessageBatchResponse struct {
	Successful []sendMessageBatchResultEntry `json:"Successful"`
	Failed     []interface{}                 `json:"Failed"`
}

type deleteMessageBatchRequestEntry struct {
	Id            string `json:"Id"`
	ReceiptHandle string `json:"ReceiptHandle"`
}

type deleteMessageBatchRequest struct {
	QueueUrl string                           `json:"QueueUrl"`
	Entries  []deleteMessageBatchRequestEntry `json:"Entries"`
}

type deleteMessageBatchResultEntry struct {
	Id string `json:"Id"`
}

type deleteMessageBatchResponse struct {
	Successful []deleteMessageBatchResultEntry `json:"Successful"`
	Failed     []interface{}                   `json:"Failed"`
}

type changeMessageVisibilityRequest struct {
	QueueUrl          string `json:"QueueUrl"`
	ReceiptHandle     string `json:"ReceiptHandle"`
	VisibilityTimeout int    `json:"VisibilityTimeout"`
}

type changeMessageVisibilityBatchEntry struct {
	Id                string `json:"Id"`
	ReceiptHandle     string `json:"ReceiptHandle"`
	VisibilityTimeout int    `json:"VisibilityTimeout"`
}

type changeMessageVisibilityBatchRequest struct {
	QueueUrl string                              `json:"QueueUrl"`
	Entries  []changeMessageVisibilityBatchEntry `json:"Entries"`
}

type changeMessageVisibilityBatchSuccessEntry struct {
	Id string `json:"Id"`
}

type changeMessageVisibilityBatchFailedEntry struct {
	Id          string `json:"Id"`
	Code        string `json:"Code"`
	Message     string `json:"Message"`
	SenderFault bool   `json:"SenderFault"`
}

type changeMessageVisibilityBatchResponse struct {
	Successful []changeMessageVisibilityBatchSuccessEntry `json:"Successful"`
	Failed     []changeMessageVisibilityBatchFailedEntry  `json:"Failed"`
}

// ---- Typed operations ------------------------------------------------------

func (h *Handler) sendMessageTyped(ctx context.Context, in *sendMessageRequest) (*sendMessageResponse, *protocol.AWSError) {
	if in.QueueUrl == "" {
		return nil, protocol.ErrMissingParameter("QueueUrl")
	}
	if in.MessageBody == "" {
		return nil, protocol.ErrMissingParameter("MessageBody")
	}
	if len(in.MessageBody) > maxMessageBodyBytes {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Value for parameter MessageBody is invalid. Reason: Message body must be no larger than 1048576 bytes.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if in.DelaySeconds < 0 || in.DelaySeconds > 900 {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Value for parameter DelaySeconds is invalid. Reason: DelaySeconds must be between 0 and 900, inclusive.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	fifo := isFifoQueue(q)
	if fifo && in.MessageGroupId == "" {
		return nil, protocol.ErrMissingParameter("MessageGroupId")
	}
	if fifo && in.DelaySeconds > 0 {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Value for parameter DelaySeconds is invalid. Reason: DelaySeconds cannot be set on FIFO queues.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	var dedupID string
	if fifo {
		dedupID = in.MessageDeduplicationId
		if dedupID == "" && q.Attributes["ContentBasedDeduplication"] == "true" {
			dedupID = md5Hex([]byte(in.MessageBody))
		}
		if dedupID == "" {
			return nil, &protocol.AWSError{
				Code:       "InvalidParameterValue",
				Message:    "The queue should either have ContentBasedDeduplication enabled or MessageDeduplicationId provided explicitly.",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if h.store.isDuplicate(ctx, queueName, dedupID) {
			msgID := uuid.New().String()
			bodyMD5 := md5Hex([]byte(in.MessageBody))
			return &sendMessageResponse{
				MessageId:        msgID,
				MD5OfMessageBody: bodyMD5,
			}, nil
		}
		h.store.recordDedup(ctx, queueName, dedupID)
	}

	msgID := uuid.New().String()
	bodyMD5 := md5Hex([]byte(in.MessageBody))

	visibleAfter := h.clk.Now()
	if in.DelaySeconds > 0 {
		visibleAfter = visibleAfter.Add(time.Duration(in.DelaySeconds) * time.Second)
	}

	msg := &Message{
		MessageID:     msgID,
		ReceiptHandle: encodeReceiptHandle(queueName, msgID),
		Body:          in.MessageBody,
		MD5OfBody:     bodyMD5,
		SentTimestamp: h.clk.Now().UnixMilli(),
		VisibleAfter:  visibleAfter,
		Attributes: map[string]string{
			"SenderId":                         h.cfg.AccountID,
			"SentTimestamp":                    strconv.FormatInt(h.clk.Now().UnixMilli(), 10),
			"ApproximateReceiveCount":          "0",
			"ApproximateFirstReceiveTimestamp": "0",
		},
		MessageAttributes:      in.MessageAttributes,
		MessageGroupId:         in.MessageGroupId,
		MessageDeduplicationId: dedupID,
	}

	var seqNum string
	if fifo {
		seqNum = strconv.FormatInt(h.seqNum.Add(1), 10)
		msg.SequenceNumber = seqNum
		msg.Attributes["MessageGroupId"] = in.MessageGroupId
		msg.Attributes["MessageDeduplicationId"] = dedupID
		msg.Attributes["SequenceNumber"] = seqNum
	}

	if aerr := h.store.putMessage(ctx, queueName, msg); aerr != nil {
		return nil, aerr
	}

	h.bus.Publish(ctx, events.Event{
		Type:   events.SQSMessageSent,
		Time:   h.clk.Now(),
		Source: serviceName,
		Payload: events.SQSMessagePayload{
			QueueName: queueName,
			MessageID: msgID,
		},
	})

	return &sendMessageResponse{
		MessageId:        msgID,
		MD5OfMessageBody: bodyMD5,
		SequenceNumber:   seqNum,
	}, nil
}

func (h *Handler) receiveMessageTyped(ctx context.Context, in *receiveMessageRequest) (*receiveMessageResponse, *protocol.AWSError) {
	storeCtx := context.WithoutCancel(ctx)

	queueName := queueNameFromURL(in.QueueUrl)
	q, aerr := h.store.getQueue(storeCtx, queueName)
	if aerr != nil {
		return nil, aerr
	}

	maxMessages, aerr := receiveMaxNumberOfMessages(in.MaxNumberOfMessages)
	if aerr != nil {
		return nil, aerr
	}
	waitTimeSeconds, aerr := receiveWaitTimeSeconds(in.WaitTimeSeconds, q)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := validateReceiveRequestAttemptID(in.ReceiveRequestAttemptId); aerr != nil {
		return nil, aerr
	}

	var visibilityTimeout int
	if in.VisibilityTimeout == nil {
		visibilityTimeout = serviceutil.ParseIntDefault(q.Attributes["VisibilityTimeout"], 30)
	} else {
		if aerr := validateVisibilityTimeout("VisibilityTimeout", *in.VisibilityTimeout); aerr != nil {
			return nil, aerr
		}
		visibilityTimeout = *in.VisibilityTimeout
	}

	systemAttrNames := requestedSystemAttributeNames(in)
	fifo := isFifoQueue(q)
	if fifo && in.ReceiveRequestAttemptId != "" {
		received, aerr := h.replayReceiveAttempt(storeCtx, queueName, in.ReceiveRequestAttemptId, visibilityTimeout, systemAttrNames, in.MessageAttributeNames)
		if aerr != nil {
			return nil, aerr
		}
		if received != nil {
			return &receiveMessageResponse{Messages: received}, nil
		}
	}

	received, aerr := h.selectVisibleMessages(storeCtx, queueName, q, maxMessages, visibilityTimeout, systemAttrNames, in.MessageAttributeNames)
	if aerr != nil {
		if waitTimeSeconds > 0 && isLongPollContextDone(aerr) {
			return &receiveMessageResponse{Messages: []receivedMessage{}}, nil
		}
		return nil, aerr
	}

	if len(received) == 0 && waitTimeSeconds > 0 {
		deadline := h.clk.After(time.Duration(waitTimeSeconds) * time.Second)
		ticker := h.clk.Ticker(100 * time.Millisecond)
		defer ticker.Stop()
	poll:
		for {
			select {
			case <-ctx.Done():
				break poll
			case <-deadline:
				break poll
			case <-ticker.C:
				received, aerr = h.selectVisibleMessages(storeCtx, queueName, q, maxMessages, visibilityTimeout, systemAttrNames, in.MessageAttributeNames)
				if aerr != nil || len(received) > 0 {
					break poll
				}
			}
		}
		if aerr != nil {
			if isLongPollContextDone(aerr) {
				received = []receivedMessage{}
			} else {
				return nil, aerr
			}
		}
	}

	if received == nil {
		received = []receivedMessage{}
	}
	if fifo && in.ReceiveRequestAttemptId != "" && len(received) > 0 {
		if aerr := h.store.putReceiveAttempt(storeCtx, queueName, in.ReceiveRequestAttemptId, newReceiveAttempt(h.clk.Now(), received)); aerr != nil {
			return nil, aerr
		}
	}

	return &receiveMessageResponse{Messages: received}, nil
}

func isLongPollContextDone(aerr *protocol.AWSError) bool {
	return errors.Is(aerr, context.Canceled) || errors.Is(aerr, context.DeadlineExceeded)
}

func receiveMaxNumberOfMessages(value *int) (int, *protocol.AWSError) {
	if value == nil {
		return 1, nil
	}
	if *value < 1 || *value > 10 {
		return 0, invalidSQSParameterValue("MaxNumberOfMessages", *value, "1 to 10")
	}
	return *value, nil
}

func receiveWaitTimeSeconds(value *int, q *Queue) (int, *protocol.AWSError) {
	if value == nil {
		wait := serviceutil.ParseIntDefault(q.Attributes["ReceiveMessageWaitTimeSeconds"], 0)
		return wait, nil
	}
	if *value < 0 || *value > 20 {
		return 0, invalidSQSParameterValue("WaitTimeSeconds", *value, "0 to 20")
	}
	return *value, nil
}

func invalidSQSParameterValue(name string, value int, validRange string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    "Invalid value for parameter " + name + ": " + strconv.Itoa(value) + ". Valid values are " + validRange + ".",
		HTTPStatus: http.StatusBadRequest,
	}
}

func validateVisibilityTimeout(name string, value int) *protocol.AWSError {
	if value < 0 || value > 43200 {
		return invalidSQSParameterValue(name, value, "0 to 43200")
	}
	return nil
}

// filterSystemAttributes returns the subset of a message's system attributes
// that the caller requested. AWS returns system attributes only when the
// request includes AttributeNames (deprecated) or MessageSystemAttributeNames;
// "All" (or ".*") returns every attribute. When nothing is requested the
// Attributes member is omitted entirely.
func filterSystemAttributes(attrs map[string]string, requested []string) map[string]string {
	if len(attrs) == 0 || len(requested) == 0 {
		return nil
	}
	if containsAllSelector(requested) {
		out := make(map[string]string, len(attrs))
		for k, v := range attrs {
			out[k] = v
		}
		return out
	}
	out := make(map[string]string)
	for _, name := range requested {
		if v, ok := attrs[name]; ok {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// filterMessageAttributes returns the subset of a message's user-defined
// message attributes that the caller requested via MessageAttributeNames.
// "All" (or ".*") returns every attribute; a "prefix.*" selector matches by
// prefix. When nothing is requested the MessageAttributes member is omitted.
func filterMessageAttributes(attrs map[string]MessageAttribute, requested []string) map[string]MessageAttribute {
	if len(attrs) == 0 || len(requested) == 0 {
		return nil
	}
	if containsAllSelector(requested) {
		out := make(map[string]MessageAttribute, len(attrs))
		for k, v := range attrs {
			out[k] = v
		}
		return out
	}
	out := make(map[string]MessageAttribute)
	for _, sel := range requested {
		if strings.HasSuffix(sel, ".*") {
			prefix := strings.TrimSuffix(sel, "*")
			for name, v := range attrs {
				if strings.HasPrefix(name, prefix) {
					out[name] = v
				}
			}
			continue
		}
		if v, ok := attrs[sel]; ok {
			out[sel] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// containsAllSelector reports whether the requested names include the AWS
// "return everything" selectors "All" or ".*".
func containsAllSelector(requested []string) bool {
	for _, name := range requested {
		if name == "All" || name == ".*" {
			return true
		}
	}
	return false
}

// requestedSystemAttributeNames merges the deprecated AttributeNames parameter
// with the current MessageSystemAttributeNames parameter. AWS accepts both and
// treats MessageSystemAttributeNames as the successor to AttributeNames.
func requestedSystemAttributeNames(in *receiveMessageRequest) []string {
	if len(in.AttributeNames) == 0 {
		return in.MessageSystemAttributeNames
	}
	if len(in.MessageSystemAttributeNames) == 0 {
		return in.AttributeNames
	}
	merged := make([]string, 0, len(in.AttributeNames)+len(in.MessageSystemAttributeNames))
	merged = append(merged, in.AttributeNames...)
	merged = append(merged, in.MessageSystemAttributeNames...)
	return merged
}

func (h *Handler) deleteMessageTyped(ctx context.Context, in *deleteMessageRequest) (*struct{}, *protocol.AWSError) {
	_, messageID, err := decodeReceiptHandle(in.ReceiptHandle)
	if err != nil {
		return nil, &protocol.AWSError{
			Code:       "ReceiptHandleIsInvalid",
			Message:    "The receipt handle is invalid.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	queueName := queueNameFromURL(in.QueueUrl)
	msg, aerr := h.store.getMessage(ctx, queueName, messageID)
	if aerr != nil {
		return nil, aerr
	}
	if msg.ReceiptHandle != in.ReceiptHandle {
		return nil, &protocol.AWSError{
			Code:       "ReceiptHandleIsInvalid",
			Message:    "The receipt handle has expired or been superseded.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if aerr := h.store.deleteMessage(ctx, queueName, messageID); aerr != nil {
		return nil, aerr
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:   events.SQSMessageDeleted,
			Time:   h.clk.Now(),
			Source: serviceName,
			Payload: events.SQSMessagePayload{
				QueueName: queueName,
				MessageID: messageID,
			},
		})
	}

	return &struct{}{}, nil
}

func (h *Handler) sendMessageBatchTyped(ctx context.Context, in *sendMessageBatchRequest) (*sendMessageBatchResponse, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	if _, aerr := h.store.getQueue(ctx, queueName); aerr != nil {
		return nil, aerr
	}

	var successful []sendMessageBatchResultEntry
	for _, entry := range in.Entries {
		msgID := uuid.New().String()
		bodyMD5 := md5Hex([]byte(entry.MessageBody))
		msg := &Message{
			MessageID:     msgID,
			ReceiptHandle: encodeReceiptHandle(queueName, msgID),
			Body:          entry.MessageBody,
			MD5OfBody:     bodyMD5,
			SentTimestamp: h.clk.Now().UnixMilli(),
			VisibleAfter:  h.clk.Now().Add(time.Duration(entry.DelaySeconds) * time.Second),
			Attributes:    map[string]string{"ApproximateReceiveCount": "0"},
		}
		_ = h.store.putMessage(ctx, queueName, msg)
		h.bus.Publish(ctx, events.Event{
			Type:   events.SQSMessageSent,
			Time:   h.clk.Now(),
			Source: serviceName,
			Payload: events.SQSMessagePayload{
				QueueName: queueName,
				MessageID: msgID,
			},
		})
		successful = append(successful, sendMessageBatchResultEntry{
			Id:               entry.Id,
			MessageId:        msgID,
			MD5OfMessageBody: bodyMD5,
		})
	}

	return &sendMessageBatchResponse{
		Successful: successful,
		Failed:     []interface{}{},
	}, nil
}

func (h *Handler) deleteMessageBatchTyped(ctx context.Context, in *deleteMessageBatchRequest) (*deleteMessageBatchResponse, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)
	var successful []deleteMessageBatchResultEntry
	var failed []interface{}
	for _, entry := range in.Entries {
		_, messageID, err := decodeReceiptHandle(entry.ReceiptHandle)
		if err != nil {
			failed = append(failed, map[string]string{
				"Id":      entry.Id,
				"Code":    "ReceiptHandleIsInvalid",
				"Message": "The receipt handle is invalid.",
			})
			continue
		}
		if msg, aerr := h.store.getMessage(ctx, queueName, messageID); aerr == nil {
			if msg.ReceiptHandle != entry.ReceiptHandle {
				failed = append(failed, map[string]string{
					"Id":      entry.Id,
					"Code":    "ReceiptHandleIsInvalid",
					"Message": "The receipt handle has expired or been superseded.",
				})
				continue
			}
		}
		_ = h.store.deleteMessage(ctx, queueName, messageID)
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:   events.SQSMessageDeleted,
				Time:   h.clk.Now(),
				Source: serviceName,
				Payload: events.SQSMessagePayload{
					QueueName: queueName,
					MessageID: messageID,
				},
			})
		}
		successful = append(successful, deleteMessageBatchResultEntry{Id: entry.Id})
	}
	if successful == nil {
		successful = []deleteMessageBatchResultEntry{}
	}
	if failed == nil {
		failed = []interface{}{}
	}

	return &deleteMessageBatchResponse{
		Successful: successful,
		Failed:     failed,
	}, nil
}

func (h *Handler) changeMessageVisibilityTyped(ctx context.Context, in *changeMessageVisibilityRequest) (*struct{}, *protocol.AWSError) {
	if aerr := validateVisibilityTimeout("VisibilityTimeout", in.VisibilityTimeout); aerr != nil {
		return nil, aerr
	}

	_, messageID, err := decodeReceiptHandle(in.ReceiptHandle)
	if err != nil {
		return nil, &protocol.AWSError{
			Code:       "ReceiptHandleIsInvalid",
			Message:    "The receipt handle is invalid.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	queueName := queueNameFromURL(in.QueueUrl)
	msg, aerr := h.store.getMessage(ctx, queueName, messageID)
	if aerr != nil {
		return nil, aerr
	}
	if msg.ReceiptHandle != in.ReceiptHandle {
		return nil, &protocol.AWSError{
			Code:       "ReceiptHandleIsInvalid",
			Message:    "The receipt handle has expired or been superseded.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	msg.VisibleAfter = h.clk.Now().Add(time.Duration(in.VisibilityTimeout) * time.Second)
	msg.VisibilityVersion++
	if aerr := h.store.putMessage(ctx, queueName, msg); aerr != nil {
		return nil, aerr
	}

	return &struct{}{}, nil
}

func (h *Handler) changeMessageVisibilityBatchTyped(ctx context.Context, in *changeMessageVisibilityBatchRequest) (*changeMessageVisibilityBatchResponse, *protocol.AWSError) {
	queueName := queueNameFromURL(in.QueueUrl)

	var successful []changeMessageVisibilityBatchSuccessEntry
	var failed []changeMessageVisibilityBatchFailedEntry

	for _, entry := range in.Entries {
		if aerr := validateVisibilityTimeout("VisibilityTimeout", entry.VisibilityTimeout); aerr != nil {
			failed = append(failed, changeMessageVisibilityBatchFailedEntry{
				Id:          entry.Id,
				Code:        aerr.Code,
				Message:     aerr.Message,
				SenderFault: true,
			})
			continue
		}

		_, messageID, err := decodeReceiptHandle(entry.ReceiptHandle)
		if err != nil {
			failed = append(failed, changeMessageVisibilityBatchFailedEntry{
				Id:          entry.Id,
				Code:        "ReceiptHandleIsInvalid",
				Message:     "The receipt handle is invalid.",
				SenderFault: true,
			})
			continue
		}

		msg, aerr := h.store.getMessage(ctx, queueName, messageID)
		if aerr != nil {
			failed = append(failed, changeMessageVisibilityBatchFailedEntry{
				Id:          entry.Id,
				Code:        aerr.Code,
				Message:     aerr.Message,
				SenderFault: false,
			})
			continue
		}

		if msg.ReceiptHandle != entry.ReceiptHandle {
			failed = append(failed, changeMessageVisibilityBatchFailedEntry{
				Id:          entry.Id,
				Code:        "ReceiptHandleIsInvalid",
				Message:     "The receipt handle has expired or been superseded.",
				SenderFault: true,
			})
			continue
		}

		msg.VisibleAfter = h.clk.Now().Add(time.Duration(entry.VisibilityTimeout) * time.Second)
		msg.VisibilityVersion++
		if aerr := h.store.putMessage(ctx, queueName, msg); aerr != nil {
			failed = append(failed, changeMessageVisibilityBatchFailedEntry{
				Id:          entry.Id,
				Code:        aerr.Code,
				Message:     aerr.Message,
				SenderFault: false,
			})
			continue
		}

		successful = append(successful, changeMessageVisibilityBatchSuccessEntry{Id: entry.Id})
	}

	if successful == nil {
		successful = []changeMessageVisibilityBatchSuccessEntry{}
	}
	if failed == nil {
		failed = []changeMessageVisibilityBatchFailedEntry{}
	}

	return &changeMessageVisibilityBatchResponse{
		Successful: successful,
		Failed:     failed,
	}, nil
}

// ---- Handlers --------------------------------------------------------------

func (h *Handler) SendMessage(w http.ResponseWriter, r *http.Request) {
	var req sendMessageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.QueueUrl, "QueueUrl") {
		return
	}
	if !serviceutil.RequireString(w, r, req.MessageBody, "MessageBody") {
		return
	}
	if len(req.MessageBody) > maxMessageBodyBytes {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Value for parameter MessageBody is invalid. Reason: Message body must be no larger than 1048576 bytes.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if req.DelaySeconds < 0 || req.DelaySeconds > 900 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Value for parameter DelaySeconds is invalid. Reason: DelaySeconds must be between 0 and 900, inclusive.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	q, aerr := h.store.getQueue(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	fifo := isFifoQueue(q)

	// FIFO validation: MessageGroupId is required.
	if fifo && req.MessageGroupId == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "The request must contain the parameter MessageGroupId.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if fifo && req.DelaySeconds > 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Value for parameter DelaySeconds is invalid. Reason: DelaySeconds cannot be set on FIFO queues.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// FIFO deduplication
	var dedupID string
	if fifo {
		dedupID = req.MessageDeduplicationId
		if dedupID == "" && q.Attributes["ContentBasedDeduplication"] == "true" {
			// Content-based dedup: use MD5 of the body as the dedup ID.
			dedupID = md5Hex([]byte(req.MessageBody))
		}
		if dedupID == "" {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterValue",
				Message:    "The queue should either have ContentBasedDeduplication enabled or MessageDeduplicationId provided explicitly.",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		if h.store.isDuplicate(r.Context(), queueName, dedupID) {
			// Duplicate — return success with the original message ID (AWS behaviour).
			// We return a new ID; real AWS returns the original, but the key behaviour
			// is that the message is not enqueued twice.
			msgID := uuid.New().String()
			bodyMD5 := md5Hex([]byte(req.MessageBody))
			protocol.WriteJSON(w, r, http.StatusOK, &sendMessageResponse{
				MessageId:        msgID,
				MD5OfMessageBody: bodyMD5,
			})
			return
		}
		h.store.recordDedup(r.Context(), queueName, dedupID)
	}

	msgID := uuid.New().String()
	bodyMD5 := md5Hex([]byte(req.MessageBody))

	// Apply delay: message is invisible until DelaySeconds have elapsed.
	visibleAfter := h.clk.Now()
	if req.DelaySeconds > 0 {
		visibleAfter = visibleAfter.Add(time.Duration(req.DelaySeconds) * time.Second)
	}

	msg := &Message{
		MessageID:     msgID,
		ReceiptHandle: encodeReceiptHandle(queueName, msgID),
		Body:          req.MessageBody,
		MD5OfBody:     bodyMD5,
		SentTimestamp: h.clk.Now().UnixMilli(),
		VisibleAfter:  visibleAfter,
		Attributes: map[string]string{
			"SenderId":                         h.cfg.AccountID,
			"SentTimestamp":                    strconv.FormatInt(h.clk.Now().UnixMilli(), 10),
			"ApproximateReceiveCount":          "0",
			"ApproximateFirstReceiveTimestamp": "0",
		},
		MessageAttributes:      req.MessageAttributes,
		MessageGroupId:         req.MessageGroupId,
		MessageDeduplicationId: dedupID,
	}

	// FIFO: assign a monotonically increasing sequence number.
	var seqNum string
	if fifo {
		seqNum = strconv.FormatInt(h.seqNum.Add(1), 10)
		msg.SequenceNumber = seqNum
		msg.Attributes["MessageGroupId"] = req.MessageGroupId
		msg.Attributes["MessageDeduplicationId"] = dedupID
		msg.Attributes["SequenceNumber"] = seqNum
	}

	if aerr := h.store.putMessage(r.Context(), queueName, msg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.bus.Publish(r.Context(), events.Event{
		Type:   events.SQSMessageSent,
		Time:   h.clk.Now(),
		Source: serviceName,
		Payload: events.SQSMessagePayload{
			QueueName: queueName,
			MessageID: msgID,
		},
	})

	protocol.WriteJSON(w, r, http.StatusOK, &sendMessageResponse{
		MessageId:        msgID,
		MD5OfMessageBody: bodyMD5,
		SequenceNumber:   seqNum,
	})
}

func (h *Handler) ReceiveMessage(w http.ResponseWriter, r *http.Request) {
	var req receiveMessageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	storeCtx := context.WithoutCancel(r.Context())

	queueName := queueNameFromURL(req.QueueUrl)
	if _, aerr := h.store.getQueue(storeCtx, queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	maxMessages, aerr := receiveMaxNumberOfMessages(req.MaxNumberOfMessages)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Load queue once for defaults, DLQ checks, and FIFO detection.
	q, aerr := h.store.getQueue(storeCtx, queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	waitTimeSeconds, aerr := receiveWaitTimeSeconds(req.WaitTimeSeconds, q)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateReceiveRequestAttemptID(req.ReceiveRequestAttemptId); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Determine visibility timeout:
	//   - nil (not sent by caller)   → use queue's VisibilityTimeout attribute
	//   - 0 (explicitly sent)         → 0 seconds (message re-visible immediately)
	//   - N (explicitly sent)         → N seconds
	// This mirrors AWS behaviour where omitting the parameter uses the queue default.
	var visibilityTimeout int
	if req.VisibilityTimeout == nil {
		visibilityTimeout = serviceutil.ParseIntDefault(q.Attributes["VisibilityTimeout"], 30)
	} else {
		if aerr := validateVisibilityTimeout("VisibilityTimeout", *req.VisibilityTimeout); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		visibilityTimeout = *req.VisibilityTimeout
	}
	systemAttrNames := requestedSystemAttributeNames(&req)
	fifo := isFifoQueue(q)
	if fifo && req.ReceiveRequestAttemptId != "" {
		received, aerr := h.replayReceiveAttempt(storeCtx, queueName, req.ReceiveRequestAttemptId, visibilityTimeout, systemAttrNames, req.MessageAttributeNames)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if received != nil {
			protocol.WriteJSON(w, r, http.StatusOK, &receiveMessageResponse{Messages: received})
			return
		}
	}

	received, aerr := h.selectVisibleMessages(storeCtx, queueName, q, maxMessages, visibilityTimeout, systemAttrNames, req.MessageAttributeNames)
	if aerr != nil {
		if waitTimeSeconds > 0 && isLongPollContextDone(aerr) {
			received = []receivedMessage{}
		} else {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}

	// Long polling: if no messages are immediately available and the caller
	// specified a WaitTimeSeconds > 0, poll until a message arrives or the
	// deadline expires. Use a 100 ms tick so we don't spin too aggressively.
	if len(received) == 0 && waitTimeSeconds > 0 {
		deadline := h.clk.After(time.Duration(waitTimeSeconds) * time.Second)
		ticker := h.clk.Ticker(100 * time.Millisecond)
		defer ticker.Stop()
	poll:
		for {
			select {
			case <-r.Context().Done():
				break poll
			case <-deadline:
				break poll
			case <-ticker.C:
				received, aerr = h.selectVisibleMessages(storeCtx, queueName, q, maxMessages, visibilityTimeout, systemAttrNames, req.MessageAttributeNames)
				if aerr != nil || len(received) > 0 {
					break poll
				}
			}
		}
		if aerr != nil {
			if isLongPollContextDone(aerr) {
				received = []receivedMessage{}
			} else {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
		}
	}

	if received == nil {
		received = []receivedMessage{} // always return array, never null
	}
	if fifo && req.ReceiveRequestAttemptId != "" && len(received) > 0 {
		if aerr := h.store.putReceiveAttempt(storeCtx, queueName, req.ReceiveRequestAttemptId, newReceiveAttempt(h.clk.Now(), received)); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}

	protocol.WriteJSON(w, r, http.StatusOK, &receiveMessageResponse{Messages: received})
}

// maxCandidateFetchLimit bounds selectVisibleMessages' retry-with-larger-limit
// loop. Not a production-scale emulator (see AGENTS.md's non-goals), so this
// is a safety cap against pathological inputs (e.g. every visible message
// stuck in one blocked FIFO group), not a tuned-for-scale value.
const maxCandidateFetchLimit = 5000

// candidateFetchLimit picks the initial candidate-fetch size for
// selectVisibleMessages' call to h.store.receiveCandidates.
//
// Standard queues have no ordering or grouping constraint, so the only
// reason a candidate goes unfulfilled is a DLQ redirect (moveToDLQ) consuming
// it without delivering it — a small multiplier absorbs that without
// fetching more than necessary on the common path; the retry loop still
// covers the pathological "every candidate is DLQ-eligible" case.
//
// FIFO queues must over-fetch more aggressively: group-locking and
// same-receive-call group dedup (see selectVisibleMessages) can skip many
// candidates before finding maxMessages deliverable ones from distinct
// groups. This is the accepted "over-fetch and select in Go" trade-off
// storage-plan.md 3.10 calls for — group selection is AWS behavioral
// semantics, not a structural predicate SQL should encode (see
// docs/plans/storage-access-plan.md's "fidelity principle").
func candidateFetchLimit(maxMessages int, fifo bool) int {
	if fifo {
		if base := maxMessages * 20; base > 100 {
			return base
		}
		return 100
	}
	return maxMessages * 4
}

// selectVisibleMessages fetches visible candidate messages from storage and
// returns up to maxMessages of them, marking them in-flight. It handles FIFO
// ordering/group-locking, DLQ movement, and event publishing. It is called
// both for immediate receives and from the long-polling loop in
// ReceiveMessage.
//
// Storage access is a bounded, indexed candidate fetch
// (h.store.receiveCandidates — docs/plans/storage-plan.md item 3.10) rather
// than a full-queue scan. FIFO's per-group locking and per-receive-call
// group dedup are AWS behavioral semantics that stay in Go (never
// re-implemented in SQL — see docs/plans/storage-access-plan.md's fidelity
// principle): candidates are over-fetched and filtered here exactly as they
// were when the input was the whole queue, so wire-observable behavior is
// unchanged; only how many storage rows get inspected per call differs. When
// the initial fetch doesn't yield enough deliverable messages (e.g. many
// candidates skipped for a blocked/already-delivered FIFO group, or DLQ
// redirects), the fetch limit doubles and retries — already-delivered or
// already-DLQ'd messages naturally drop out of the next fetch (their
// visible_at moved to the future, or the row was deleted), so no
// already-processed message can be double-delivered across retries.
//
// systemAttrNames and messageAttrNames control which system attributes and
// user-defined message attributes are included in the response, mirroring AWS
// which returns them only when explicitly requested.
func (h *Handler) selectVisibleMessages(ctx context.Context, queueName string, q *Queue, maxMessages, visibilityTimeout int, systemAttrNames, messageAttrNames []string) ([]receivedMessage, *protocol.AWSError) {
	rp, _ := parseRedrivePolicy(q.Attributes)
	fifo := isFifoQueue(q)
	now := h.clk.Now()

	// FIFO: identify which message groups currently have an in-flight (or
	// delayed) message. Within a group, only the first visible message may
	// be delivered; groups with any invisible message are blocked entirely.
	// Computed once, up front, from a visibility snapshot — matching the
	// previous full-scan implementation's behavior exactly.
	var blockedGroups map[string]bool
	if fifo {
		var aerr *protocol.AWSError
		blockedGroups, aerr = h.store.blockedGroups(ctx, queueName, now)
		if aerr != nil {
			return nil, aerr
		}
	}

	deliveredGroups := map[string]bool{}
	var received []receivedMessage

	limit := candidateFetchLimit(maxMessages, fifo)
	for {
		candidates, aerr := h.store.receiveCandidates(ctx, queueName, now, limit, fifo)
		if aerr != nil {
			return nil, aerr
		}

		for _, msg := range candidates {
			if len(received) >= maxMessages {
				break
			}

			// FIFO: skip this message if its group is blocked (has an
			// in-flight message) or if we already delivered a message from
			// this group in this receive call.
			if fifo && msg.MessageGroupId != "" {
				if blockedGroups[msg.MessageGroupId] || deliveredGroups[msg.MessageGroupId] {
					continue
				}
			}

			// Generate a fresh receipt handle for this receive. AWS issues a
			// new handle on every receive so callers cannot reuse handles
			// from a previous receive cycle once the visibility timeout has
			// expired.
			newHandle := encodeReceiptHandle(queueName, msg.MessageID)
			msg.ReceiptHandle = newHandle

			// Mark as invisible for visibilityTimeout seconds.
			msg.VisibleAfter = h.clk.Now().Add(time.Duration(visibilityTimeout) * time.Second)
			msg.ApproximateReceiveCount++
			if msg.Attributes == nil {
				msg.Attributes = map[string]string{}
			}
			msg.Attributes["ApproximateReceiveCount"] = strconv.Itoa(msg.ApproximateReceiveCount)
			if msg.ApproximateReceiveCount == 1 {
				msg.Attributes["ApproximateFirstReceiveTimestamp"] = strconv.FormatInt(h.clk.Now().UnixMilli(), 10)
			}

			// DLQ check: AWS moves a message after its receive count exceeds
			// maxReceiveCount, so the maxReceiveCount-th receive is still delivered.
			if rp != nil && msg.ApproximateReceiveCount > rp.MaxReceiveCount {
				if aerr := h.moveToDLQ(ctx, queueName, q, rp, msg); aerr != nil {
					return nil, aerr
				}
				// Skip: do not return this message to the caller. Deleted
				// from storage by moveToDLQ, so it cannot reappear in a
				// later retry round of this same call.
				continue
			}

			if aerr := h.store.putMessage(ctx, queueName, msg); aerr != nil {
				return nil, aerr
			}

			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SQSMessageInflight,
					Time:   h.clk.Now(),
					Source: serviceName,
					Payload: events.SQSMessagePayload{
						QueueName:               queueName,
						MessageID:               msg.MessageID,
						VisibleAfter:            msg.VisibleAfter.UnixMilli(),
						ApproximateReceiveCount: msg.ApproximateReceiveCount,
					},
				})
			}

			received = append(received, receivedMessageFromStored(msg, systemAttrNames, messageAttrNames))

			// FIFO: mark this group as delivered so no more messages from it
			// are returned in this receive call.
			if fifo && msg.MessageGroupId != "" {
				deliveredGroups[msg.MessageGroupId] = true
			}
		}

		if len(received) >= maxMessages {
			break
		}
		if len(candidates) < limit {
			// Storage returned fewer candidates than asked for — the queue
			// has no more visible messages to offer, so a bigger limit
			// cannot help.
			break
		}
		if limit >= maxCandidateFetchLimit {
			break
		}
		limit *= 2
	}

	return received, nil
}

func newReceiveAttempt(now time.Time, received []receivedMessage) *receiveAttempt {
	attempt := &receiveAttempt{
		ExpiresAtUnixMilli: now.Add(5 * time.Minute).UnixMilli(),
		Messages:           make([]receiveAttemptMessage, 0, len(received)),
	}
	for _, msg := range received {
		attempt.Messages = append(attempt.Messages, receiveAttemptMessage{
			MessageID:         msg.MessageId,
			ReceiptHandle:     msg.ReceiptHandle,
			VisibilityVersion: msg.visibilityVersion,
		})
	}
	return attempt
}

func validateReceiveRequestAttemptID(attemptID string) *protocol.AWSError {
	if attemptID == "" {
		return nil
	}
	if len(attemptID) > 128 {
		return invalidReceiveRequestAttemptID()
	}
	for _, r := range attemptID {
		if r < '!' || r > '~' {
			return invalidReceiveRequestAttemptID()
		}
	}
	return nil
}

func invalidReceiveRequestAttemptID() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    "Invalid value for parameter ReceiveRequestAttemptId. Must be 1 to 128 printable ASCII characters without spaces.",
		HTTPStatus: http.StatusBadRequest,
	}
}

func (h *Handler) replayReceiveAttempt(ctx context.Context, queueName, attemptID string, visibilityTimeout int, systemAttrNames, messageAttrNames []string) ([]receivedMessage, *protocol.AWSError) {
	attempt, aerr := h.store.getReceiveAttempt(ctx, queueName, attemptID)
	if aerr != nil || attempt == nil {
		return nil, aerr
	}

	received := make([]receivedMessage, 0, len(attempt.Messages))
	for _, attempted := range attempt.Messages {
		msg, aerr := h.store.getMessage(ctx, queueName, attempted.MessageID)
		if aerr != nil || msg.ReceiptHandle != attempted.ReceiptHandle || msg.VisibilityVersion != attempted.VisibilityVersion {
			return []receivedMessage{}, nil
		}
		msg.VisibleAfter = h.clk.Now().Add(time.Duration(visibilityTimeout) * time.Second)
		if aerr := h.store.putMessage(ctx, queueName, msg); aerr != nil {
			return nil, aerr
		}
		received = append(received, receivedMessageFromStored(msg, systemAttrNames, messageAttrNames))
	}
	return received, nil
}

func receivedMessageFromStored(msg *Message, systemAttrNames, messageAttrNames []string) receivedMessage {
	return receivedMessage{
		MessageId:         msg.MessageID,
		ReceiptHandle:     msg.ReceiptHandle,
		MD5OfBody:         msg.MD5OfBody,
		Body:              msg.Body,
		Attributes:        filterSystemAttributes(msg.Attributes, systemAttrNames),
		MessageAttributes: filterMessageAttributes(msg.MessageAttributes, messageAttrNames),
		visibilityVersion: msg.VisibilityVersion,
	}
}

func (h *Handler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	var req deleteMessageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Decode the opaque receipt handle to obtain the queue name and message ID.
	_, messageID, err := decodeReceiptHandle(req.ReceiptHandle)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ReceiptHandleIsInvalid",
			Message:    "The receipt handle is invalid.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)

	// Verify the handle matches the one currently stored for this message.
	// This rejects stale handles that were superseded by a later ReceiveMessage.
	msg, aerr := h.store.getMessage(r.Context(), queueName, messageID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if msg.ReceiptHandle != req.ReceiptHandle {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ReceiptHandleIsInvalid",
			Message:    "The receipt handle has expired or been superseded.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if aerr := h.store.deleteMessage(r.Context(), queueName, messageID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:   events.SQSMessageDeleted,
			Time:   h.clk.Now(),
			Source: serviceName,
			Payload: events.SQSMessagePayload{
				QueueName: queueName,
				MessageID: messageID,
			},
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

func (h *Handler) SendMessageBatch(w http.ResponseWriter, r *http.Request) {
	var req sendMessageBatchRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	if _, aerr := h.store.getQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var successful []sendMessageBatchResultEntry
	for _, entry := range req.Entries {
		msgID := uuid.New().String()
		bodyMD5 := md5Hex([]byte(entry.MessageBody))
		msg := &Message{
			MessageID:     msgID,
			ReceiptHandle: encodeReceiptHandle(queueName, msgID),
			Body:          entry.MessageBody,
			MD5OfBody:     bodyMD5,
			SentTimestamp: h.clk.Now().UnixMilli(),
			VisibleAfter:  h.clk.Now().Add(time.Duration(entry.DelaySeconds) * time.Second),
			Attributes:    map[string]string{"ApproximateReceiveCount": "0"},
		}
		_ = h.store.putMessage(r.Context(), queueName, msg)
		h.bus.Publish(r.Context(), events.Event{
			Type:   events.SQSMessageSent,
			Time:   h.clk.Now(),
			Source: serviceName,
			Payload: events.SQSMessagePayload{
				QueueName: queueName,
				MessageID: msgID,
			},
		})
		successful = append(successful, sendMessageBatchResultEntry{
			Id:               entry.Id,
			MessageId:        msgID,
			MD5OfMessageBody: bodyMD5,
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, &sendMessageBatchResponse{
		Successful: successful,
		Failed:     []interface{}{},
	})
}

func (h *Handler) DeleteMessageBatch(w http.ResponseWriter, r *http.Request) {
	var req deleteMessageBatchRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	var successful []deleteMessageBatchResultEntry
	var failed []interface{}
	for _, entry := range req.Entries {
		_, messageID, err := decodeReceiptHandle(entry.ReceiptHandle)
		if err != nil {
			failed = append(failed, map[string]string{
				"Id":      entry.Id,
				"Code":    "ReceiptHandleIsInvalid",
				"Message": "The receipt handle is invalid.",
			})
			continue
		}
		// Verify handle still matches the stored message (not superseded).
		if msg, aerr := h.store.getMessage(r.Context(), queueName, messageID); aerr == nil {
			if msg.ReceiptHandle != entry.ReceiptHandle {
				failed = append(failed, map[string]string{
					"Id":      entry.Id,
					"Code":    "ReceiptHandleIsInvalid",
					"Message": "The receipt handle has expired or been superseded.",
				})
				continue
			}
		}
		_ = h.store.deleteMessage(r.Context(), queueName, messageID)
		if h.bus != nil {
			h.bus.Publish(r.Context(), events.Event{
				Type:   events.SQSMessageDeleted,
				Time:   h.clk.Now(),
				Source: serviceName,
				Payload: events.SQSMessagePayload{
					QueueName: queueName,
					MessageID: messageID,
				},
			})
		}
		successful = append(successful, deleteMessageBatchResultEntry{Id: entry.Id})
	}
	if successful == nil {
		successful = []deleteMessageBatchResultEntry{}
	}
	if failed == nil {
		failed = []interface{}{}
	}

	protocol.WriteJSON(w, r, http.StatusOK, &deleteMessageBatchResponse{
		Successful: successful,
		Failed:     failed,
	})
}

// ChangeMessageVisibilityBatch handles the SQS ChangeMessageVisibilityBatch operation.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibilityBatch.html
func (h *Handler) ChangeMessageVisibilityBatch(w http.ResponseWriter, r *http.Request) {
	var req changeMessageVisibilityBatchRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)

	type successEntry struct {
		Id string `json:"Id"`
	}
	type failedEntry struct {
		Id          string `json:"Id"`
		Code        string `json:"Code"`
		Message     string `json:"Message"`
		SenderFault bool   `json:"SenderFault"`
	}

	var successful []successEntry
	var failed []failedEntry

	for _, entry := range req.Entries {
		if aerr := validateVisibilityTimeout("VisibilityTimeout", entry.VisibilityTimeout); aerr != nil {
			failed = append(failed, failedEntry{
				Id:          entry.Id,
				Code:        aerr.Code,
				Message:     aerr.Message,
				SenderFault: true,
			})
			continue
		}

		_, messageID, err := decodeReceiptHandle(entry.ReceiptHandle)
		if err != nil {
			failed = append(failed, failedEntry{
				Id:          entry.Id,
				Code:        "ReceiptHandleIsInvalid",
				Message:     "The receipt handle is invalid.",
				SenderFault: true,
			})
			continue
		}

		msg, aerr := h.store.getMessage(r.Context(), queueName, messageID)
		if aerr != nil {
			failed = append(failed, failedEntry{
				Id:          entry.Id,
				Code:        aerr.Code,
				Message:     aerr.Message,
				SenderFault: false,
			})
			continue
		}

		if msg.ReceiptHandle != entry.ReceiptHandle {
			failed = append(failed, failedEntry{
				Id:          entry.Id,
				Code:        "ReceiptHandleIsInvalid",
				Message:     "The receipt handle has expired or been superseded.",
				SenderFault: true,
			})
			continue
		}

		msg.VisibleAfter = h.clk.Now().Add(time.Duration(entry.VisibilityTimeout) * time.Second)
		msg.VisibilityVersion++
		if aerr := h.store.putMessage(r.Context(), queueName, msg); aerr != nil {
			failed = append(failed, failedEntry{
				Id:          entry.Id,
				Code:        aerr.Code,
				Message:     aerr.Message,
				SenderFault: false,
			})
			continue
		}

		successful = append(successful, successEntry{Id: entry.Id})
	}

	if successful == nil {
		successful = []successEntry{}
	}
	if failed == nil {
		failed = []failedEntry{}
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"Successful": successful,
		"Failed":     failed,
	})
}

// ---- PeekMessages ----------------------------------------------------------

// peekMessage is the response element for the non-AWS PeekMessages endpoint.
type peekMessage struct {
	MessageID               string                      `json:"MessageId"`
	ReceiptHandle           string                      `json:"ReceiptHandle"`
	Body                    string                      `json:"Body"`
	MD5OfBody               string                      `json:"MD5OfBody"`
	Attributes              map[string]string           `json:"Attributes"`
	MessageAttributes       map[string]MessageAttribute `json:"MessageAttributes,omitempty"`
	Inflight                bool                        `json:"Inflight"`
	Delayed                 bool                        `json:"Delayed"`      // true when invisible due to send-delay (never received)
	VisibleAfter            int64                       `json:"VisibleAfter"` // Unix milliseconds; 0 when not in-flight
	ApproximateReceiveCount int                         `json:"ApproximateReceiveCount"`
}

// PeekMessages is a non-AWS extension that returns all messages in a queue
// without modifying any state — no receive-count increment, no visibility
// timeout applied. In-flight (invisible) messages are included and flagged.
//
// Route: GET /{accountID}/{queueName}.
func (h *Handler) PeekMessages(w http.ResponseWriter, r *http.Request) {
	queueName := chi.URLParam(r, "queueName")

	if _, aerr := h.store.getQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	msgs, aerr := h.store.listMessages(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	result := make([]peekMessage, 0, len(msgs))
	for _, m := range msgs {
		inflight := !m.IsVisible(h.clk)
		// A message is delayed when it is not yet visible and has never been
		// received (ApproximateReceiveCount == 0). Once received, the invisible
		// period is a visibility timeout and the message is truly in-flight.
		delayed := inflight && m.ApproximateReceiveCount == 0
		var visibleAfterMs int64
		if inflight {
			visibleAfterMs = m.VisibleAfter.UnixMilli()
		}
		result = append(result, peekMessage{
			MessageID:               m.MessageID,
			ReceiptHandle:           m.ReceiptHandle,
			Body:                    m.Body,
			MD5OfBody:               m.MD5OfBody,
			Attributes:              m.Attributes,
			MessageAttributes:       m.MessageAttributes,
			Inflight:                inflight,
			Delayed:                 delayed,
			VisibleAfter:            visibleAfterMs,
			ApproximateReceiveCount: m.ApproximateReceiveCount,
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Messages": result})
}
