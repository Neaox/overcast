package lambda

// handler_stream.go implements InvokeWithResponseStream
// (POST /2021-11-15/functions/{name}/response-streaming-invocations).
//
// The response body uses the AWS event stream binary encoding
// (application/vnd.amazon.eventstream). For simplicity this emulator
// invokes the function synchronously and wraps the resulting payload in a
// single set of events — the caller sees a complete response as expected.
//
// Event sequence:
//  1. PayloadChunk       — the raw function response bytes
//  2. InvokeComplete     — {} or {"ErrorCode": "...", "ErrorDetails": "..."}

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/eventstream"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// InvokeWithResponseStream handles
// POST /2021-11-15/functions/{name}/response-streaming-invocations.
func (h *Handler) InvokeWithResponseStream(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	// Only RequestResponse is valid here.
	invType := r.Header.Get("X-Amz-Invocation-Type")
	if invType != "" && !strings.EqualFold(invType, "RequestResponse") {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRequestContentException",
			Message:    "InvokeWithResponseStream only supports RequestResponse invocation type.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if aerr := checkInvokableState(fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, 6*1024*1024))
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRequestContentException",
			Message:    "Could not read request body.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	if err := h.ls.addInvocation(ctx, fn, payload); err != nil {
		h.log.Warn("invoke-stream: record invocation", zap.String("function", name), zap.Error(err))
	}

	var rt Runtime
	for _, candidate := range h.runtimes.get() {
		if candidate.CanHandle(fn.Runtime) {
			rt = candidate
			break
		}
	}
	if rt == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRuntimeException",
			Message:    "No runtime available for " + fn.Runtime + ".",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	result := h.invokeSync(ctx, fn, rt, payload, name)

	// Begin streaming response.
	w.Header().Set("Content-Type", eventstream.ContentType)
	w.Header().Set("X-Amz-Executed-Version", "$LATEST")
	logType := r.Header.Get("X-Amz-Log-Type")
	if result != nil && result.LogResult != "" && strings.EqualFold(logType, "Tail") {
		w.Header().Set("X-Amz-Log-Result", result.LogResult)
	}
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// Event 1: PayloadChunk (only when invocation succeeded).
	if result != nil && len(result.Payload) > 0 {
		_ = eventstream.WriteMessage(w, []eventstream.Header{
			{Name: ":message-type", Value: "event"},
			{Name: ":event-type", Value: "PayloadChunk"},
			{Name: ":content-type", Value: "application/octet-stream"},
		}, result.Payload)
		if hasFlusher {
			flusher.Flush()
		}
	}

	// Event 2: InvokeComplete.
	var completePayload []byte
	if result != nil && result.FunctionError != "" {
		completePayload, _ = json.Marshal(map[string]string{
			"ErrorCode":    result.FunctionError,
			"ErrorDetails": fmt.Sprintf("Function returned error: %s", result.FunctionError),
		})
	} else if result == nil {
		completePayload, _ = json.Marshal(map[string]string{
			"ErrorCode":    "Lambda.AWSLambdaException",
			"ErrorDetails": "Function invocation failed",
		})
	} else {
		completePayload = []byte("{}")
	}
	_ = eventstream.WriteMessage(w, []eventstream.Header{
		{Name: ":message-type", Value: "event"},
		{Name: ":event-type", Value: "InvokeComplete"},
		{Name: ":content-type", Value: "application/json"},
	}, completePayload)
	if hasFlusher {
		flusher.Flush()
	}
}
