package sqs

// handler_message.go contains handlers for SQS message operations:
// SendMessage, ReceiveMessage, DeleteMessage, SendMessageBatch, DeleteMessageBatch.

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
)

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
}

type receiveMessageRequest struct {
	QueueUrl              string   `json:"QueueUrl"`
	MaxNumberOfMessages   int      `json:"MaxNumberOfMessages,omitempty"`
	VisibilityTimeout     *int     `json:"VisibilityTimeout,omitempty"`
	WaitTimeSeconds       int      `json:"WaitTimeSeconds,omitempty"`
	AttributeNames        []string `json:"AttributeNames,omitempty"`
	MessageAttributeNames []string `json:"MessageAttributeNames,omitempty"`
}

type receiveMessageResponse struct {
	Messages []receivedMessage `json:"Messages"`
}

type receivedMessage struct {
	MessageId         string                      `json:"MessageId"`
	ReceiptHandle     string                      `json:"ReceiptHandle"`
	MD5OfBody         string                      `json:"MD5OfBody"`
	Body              string                      `json:"Body"`
	Attributes        map[string]string           `json:"Attributes,omitempty"`
	MessageAttributes map[string]MessageAttribute `json:"MessageAttributes,omitempty"`
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

	queueName := queueNameFromURL(req.QueueUrl)
	if _, aerr := h.store.getQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	msgID := uuid.New().String()
	bodyMD5 := fmt.Sprintf("%x", md5.Sum([]byte(req.MessageBody)))

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
		MessageAttributes: req.MessageAttributes,
	}

	if aerr := h.store.putMessage(r.Context(), queueName, msg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, &sendMessageResponse{
		MessageId:        msgID,
		MD5OfMessageBody: bodyMD5,
	})
}

func (h *Handler) ReceiveMessage(w http.ResponseWriter, r *http.Request) {
	var req receiveMessageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	queueName := queueNameFromURL(req.QueueUrl)
	if _, aerr := h.store.getQueue(r.Context(), queueName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	maxMessages := serviceutil.ClampInt(req.MaxNumberOfMessages, 1, 10)

	// Determine visibility timeout:
	//   - nil (not sent by caller)   → use queue's VisibilityTimeout attribute
	//   - 0 (explicitly sent)         → 0 seconds (message re-visible immediately)
	//   - N (explicitly sent)         → N seconds
	// This mirrors AWS behaviour where omitting the parameter uses the queue default.
	var visibilityTimeout int
	if req.VisibilityTimeout == nil {
		q, aerr := h.store.getQueue(r.Context(), queueName)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		visibilityTimeout = serviceutil.ParseIntDefault(q.Attributes["VisibilityTimeout"], 30)
	} else {
		visibilityTimeout = *req.VisibilityTimeout
	}

	// Note: WaitTimeSeconds (long polling) is not implemented in v1.
	// We return immediately with available messages.
	// TODO(priority:P2): implement long polling with a blocking wait up to WaitTimeSeconds.

	allMessages, aerr := h.store.listMessages(r.Context(), queueName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var received []receivedMessage
	for _, msg := range allMessages {
		if len(received) >= maxMessages {
			break
		}
		if !msg.IsVisible(h.clk) {
			continue
		}

		// Generate a fresh receipt handle for this receive. AWS issues a new
		// handle on every receive so callers cannot reuse handles from a
		// previous receive cycle once the visibility timeout has expired.
		newHandle := encodeReceiptHandle(queueName, msg.MessageID)
		msg.ReceiptHandle = newHandle

		// Mark as invisible for visibilityTimeout seconds.
		msg.VisibleAfter = h.clk.Now().Add(time.Duration(visibilityTimeout) * time.Second)
		msg.ApproximateReceiveCount++
		msg.Attributes["ApproximateReceiveCount"] = strconv.Itoa(msg.ApproximateReceiveCount)
		if msg.ApproximateReceiveCount == 1 {
			msg.Attributes["ApproximateFirstReceiveTimestamp"] = strconv.FormatInt(h.clk.Now().UnixMilli(), 10)
		}

		if aerr := h.store.putMessage(r.Context(), queueName, msg); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}

		received = append(received, receivedMessage{
			MessageId:         msg.MessageID,
			ReceiptHandle:     newHandle,
			MD5OfBody:         msg.MD5OfBody,
			Body:              msg.Body,
			Attributes:        msg.Attributes,
			MessageAttributes: msg.MessageAttributes,
		})
	}

	if received == nil {
		received = []receivedMessage{} // always return array, never null
	}

	protocol.WriteJSON(w, r, http.StatusOK, &receiveMessageResponse{Messages: received})
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
		bodyMD5 := fmt.Sprintf("%x", md5.Sum([]byte(entry.MessageBody)))
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
