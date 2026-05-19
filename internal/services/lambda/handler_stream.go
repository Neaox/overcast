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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// streamContentType is the MIME type for the AWS binary event stream format.
const streamContentType = "application/vnd.amazon.eventstream"

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
	w.Header().Set("Content-Type", streamContentType)
	w.Header().Set("X-Amz-Executed-Version", "$LATEST")
	logType := r.Header.Get("X-Amz-Log-Type")
	if result != nil && result.LogResult != "" && strings.EqualFold(logType, "Tail") {
		w.Header().Set("X-Amz-Log-Result", result.LogResult)
	}
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// Event 1: PayloadChunk (only when invocation succeeded).
	if result != nil && len(result.Payload) > 0 {
		writeEventStreamMessage(w, []esHeader{
			{name: ":message-type", value: "event"},
			{name: ":event-type", value: "PayloadChunk"},
			{name: ":content-type", value: "application/octet-stream"},
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
	writeEventStreamMessage(w, []esHeader{
		{name: ":message-type", value: "event"},
		{name: ":event-type", value: "InvokeComplete"},
		{name: ":content-type", value: "application/json"},
	}, completePayload)
	if hasFlusher {
		flusher.Flush()
	}
}

// ─── AWS Event Stream binary encoding ────────────────────────────────────────
//
// Message format (big-endian):
//
//	Prelude (12 bytes):
//	  total_byte_length   uint32  — includes all fields including both CRCs
//	  headers_byte_length uint32  — byte length of the headers section
//	  prelude_crc         uint32  — CRC32 of the first 8 bytes (lengths)
//	Headers (variable):
//	  name_byte_length    uint8
//	  name                bytes
//	  value_type          uint8   — 7 = string
//	  value_byte_length   uint16
//	  value               bytes
//	Payload (variable)
//	Message CRC           uint32  — CRC32 of everything preceding this field

type esHeader struct {
	name  string
	value string
}

// writeEventStreamMessage encodes and writes a single event stream message.
func writeEventStreamMessage(w io.Writer, headers []esHeader, payload []byte) {
	msg := encodeEventStreamMessage(headers, payload)
	_, _ = w.Write(msg)
}

func encodeEventStreamMessage(headers []esHeader, payload []byte) []byte {
	// Build headers section.
	var hdrLen int
	for _, h := range headers {
		// 1 (name len) + len(name) + 1 (type) + 2 (value len) + len(value)
		hdrLen += 1 + len(h.name) + 1 + 2 + len(h.value)
	}
	hdrBuf := make([]byte, 0, hdrLen)
	for _, h := range headers {
		hdrBuf = append(hdrBuf, byte(len(h.name)))
		hdrBuf = append(hdrBuf, []byte(h.name)...)
		hdrBuf = append(hdrBuf, 7) // string type
		hdrBuf = binary.BigEndian.AppendUint16(hdrBuf, uint16(len(h.value)))
		hdrBuf = append(hdrBuf, []byte(h.value)...)
	}

	// Total = 12 (prelude) + headers + payload + 4 (trailing CRC)
	totalLen := uint32(12 + len(hdrBuf) + len(payload) + 4)
	hdrsLen := uint32(len(hdrBuf))

	buf := make([]byte, 0, int(totalLen))
	buf = binary.BigEndian.AppendUint32(buf, totalLen)
	buf = binary.BigEndian.AppendUint32(buf, hdrsLen)

	// Prelude CRC: over the first 8 bytes.
	prelCRC := crc32.ChecksumIEEE(buf[:8])
	buf = binary.BigEndian.AppendUint32(buf, prelCRC)

	buf = append(buf, hdrBuf...)
	buf = append(buf, payload...)

	// Message CRC: over everything so far.
	msgCRC := crc32.ChecksumIEEE(buf)
	buf = binary.BigEndian.AppendUint32(buf, msgCRC)

	return buf
}
