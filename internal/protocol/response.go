package protocol

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
)

const defaultAWSJSONContentType = "application/x-amz-json-1.0"

// WriteXML serialises v as XML and writes it with the correct Content-Type
// and request ID headers. Services call this for successful responses.
func WriteXML(w http.ResponseWriter, r *http.Request, status int, v any) {
	reqID := RequestIDFromContext(r.Context())

	body, err := xml.Marshal(v)
	if err != nil {
		WriteXMLError(w, r, ErrInternalError)
		return
	}

	// Drain the request body so the HTTP/1.1 connection can be reused.
	if r.Body != nil {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck
	}

	full := append([]byte(xml.Header), body...)
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(full)))
	w.Header().Set("x-amz-request-id", reqID)
	w.WriteHeader(status)
	w.Write(full) //nolint:errcheck
}

// WriteJSON serialises v as JSON and writes it with the correct Content-Type
// and request ID headers. Services call this for successful responses.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	WriteAWSJSON(w, r, status, v, defaultAWSJSONContentType)
}

// WriteAWSJSON serialises v as JSON and writes it with the provided AWS JSON
// content type and request ID headers.
//
// Pass an explicit content type per service protocol, for example:
//   - application/x-amz-json-1.0
//   - application/x-amz-json-1.1
//
// If contentType is empty, application/x-amz-json-1.0 is used.
func WriteAWSJSON(w http.ResponseWriter, r *http.Request, status int, v any, contentType string) {
	reqID := RequestIDFromContext(r.Context())

	body, err := json.Marshal(v)
	if err != nil {
		WriteJSONError(w, r, ErrInternalError)
		return
	}

	// Drain the request body so the HTTP/1.1 connection can be reused.
	if r.Body != nil {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck
	}

	if contentType == "" {
		contentType = defaultAWSJSONContentType
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("x-amzn-requestid", reqID)
	w.WriteHeader(status)
	w.Write(body) //nolint:errcheck
}

// WriteEmpty writes a response with no body and the standard request ID header.
// Used for operations like DeleteObject which return 204 or an empty 200.
func WriteEmpty(w http.ResponseWriter, r *http.Request, status int) {
	if r.Body != nil {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck
	}
	reqID := RequestIDFromContext(r.Context())
	w.Header().Set("x-amz-request-id", reqID)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(status)
}

// ResponseMetadata is embedded in AWS Query-protocol XML responses.
// It carries the request ID that SDKs surface as response.ResultMetadata.
type ResponseMetadata struct {
	RequestID string `xml:"RequestId"`
}

// QueryResponseMetadata returns a ResponseMetadata populated from the request context.
func QueryResponseMetadata(r *http.Request) ResponseMetadata {
	return ResponseMetadata{RequestID: RequestIDFromContext(r.Context())}
}

// WriteQueryXML serialises v as XML with text/xml content type.
// Used by Query-protocol services (SNS, STS, IAM). The request ID is set as both a
// response header and should be embedded in the response struct's ResponseMetadata.
// The request body is drained so the HTTP/1.1 connection can be reused by the SDK client.
func WriteQueryXML(w http.ResponseWriter, r *http.Request, status int, v any) {
	reqID := RequestIDFromContext(r.Context())

	body, err := xml.Marshal(v)
	if err != nil {
		WriteQueryXMLError(w, r, ErrInternalError)
		return
	}

	// Drain the request body so the HTTP/1.1 connection can be reused by the
	// SDK client. Without this, the client logs "failed to close HTTP response
	// body, this may affect connection reuse".
	if r.Body != nil {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		r.Body.Close()              //nolint:errcheck
	}

	full := append([]byte(xml.Header), body...)
	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(full)))
	w.Header().Set("x-amzn-requestid", reqID)
	w.WriteHeader(status)
	w.Write(full) //nolint:errcheck
}
