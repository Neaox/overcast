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
//
// No initial-response event opens this stream, unlike CloudWatch Logs'
// StartLiveTail. This is a restJson1 operation whose initial output members
// travel in HTTP headers (X-Amz-Executed-Version, Content-Type) rather than in
// a document, so the SDKs have nothing to wait for: the generated event-stream
// reader in aws-sdk-go-v2/service/lambda has no initial-response handling at
// all. See internal/protocol/eventstream.InitialResponseEventType for the case
// that does need one.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/eventstream"
	"go.uber.org/zap"
)

// InvokeWithResponseStream handles
// POST /2021-11-15/functions/{name}/response-streaming-invocations.
func (h *Handler) InvokeWithResponseStream(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
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
		log.Warn("invoke-stream: record invocation", zap.String("function", name), zap.Error(err))
	}

	rt := h.runtimes.runtimeFor(ctx, fn.Runtime)
	if rt == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidRuntimeException",
			Message:    "No runtime available for " + fn.Runtime + ".",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	tail := strings.EqualFold(r.Header.Get("X-Amz-Log-Type"), "Tail")

	result := h.invokeSync(ctx, fn, rt, payload, name, InvokeOptions{LogTail: tail})
	if result.throttle != nil {
		writeThrottleError(w, r, result.throttle)
		return
	}

	// Begin streaming response.
	w.Header().Set("Content-Type", eventstream.ContentType)
	w.Header().Set("X-Amz-Executed-Version", "$LATEST")
	if result != nil && result.LogResult != "" {
		w.Header().Set("X-Amz-Log-Result", result.LogResult)
	}
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// Event 1: PayloadChunk (only when invocation succeeded).
	if result != nil && len(result.Payload) > 0 {
		_ = eventstream.WriteEvent(w, "PayloadChunk", "application/octet-stream", result.Payload)
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
	_ = eventstream.WriteEvent(w, "InvokeComplete", eventstream.JSONContentType, completePayload)
	if hasFlusher {
		flusher.Flush()
	}
}
