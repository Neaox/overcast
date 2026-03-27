package protocol

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
)

// WriteXML serialises v as XML and writes it with the correct Content-Type
// and request ID headers. Services call this for successful responses.
func WriteXML(w http.ResponseWriter, r *http.Request, status int, v any) {
	reqID := RequestIDFromContext(r.Context())

	body, err := xml.Marshal(v)
	if err != nil {
		WriteXMLError(w, r, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", reqID)
	w.WriteHeader(status)
	w.Write([]byte(xml.Header))
	w.Write(body)
}

// WriteJSON serialises v as JSON and writes it with the correct Content-Type
// and request ID headers. Services call this for successful responses.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	reqID := RequestIDFromContext(r.Context())

	body, err := json.Marshal(v)
	if err != nil {
		WriteJSONError(w, r, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("x-amzn-requestid", reqID)
	w.WriteHeader(status)
	w.Write(body)
}

// WriteEmpty writes a 200 OK with no body and the standard request ID header.
// Used for operations like DeleteObject which return 204 or an empty 200.
func WriteEmpty(w http.ResponseWriter, r *http.Request, status int) {
	reqID := RequestIDFromContext(r.Context())
	w.Header().Set("x-amz-request-id", reqID)
	w.WriteHeader(status)
}
