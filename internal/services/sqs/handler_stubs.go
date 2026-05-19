package sqs

// handler_stubs.go contains every SQS handler that is not yet implemented.
// Each method returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go. handler.go is the authoritative inventory of what
// actually works.

import (
	"context"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (h *Handler) addPermissionTyped(context.Context, *struct{}) (*struct{}, *protocol.AWSError) {
	return nil, protocol.ErrNotImplemented
}

func (h *Handler) removePermissionTyped(context.Context, *struct{}) (*struct{}, *protocol.AWSError) {
	return nil, protocol.ErrNotImplemented
}

// ChangeMessageVisibility adjusts the visibility timeout of an in-flight message.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibility.html
func (h *Handler) ChangeMessageVisibility(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueUrl          string `json:"QueueUrl"`
		ReceiptHandle     string `json:"ReceiptHandle"`
		VisibilityTimeout int    `json:"VisibilityTimeout"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

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

	// VisibilityTimeout of 0 makes the message immediately visible again.
	msg.VisibleAfter = h.clk.Now().Add(time.Duration(req.VisibilityTimeout) * time.Second)
	if aerr := h.store.putMessage(r.Context(), queueName, msg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// AddPermission handles the SQS AddPermission operation.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_AddPermission.html
func (h *Handler) AddPermission(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// RemovePermission handles the SQS RemovePermission operation.
// AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_RemovePermission.html
func (h *Handler) RemovePermission(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
