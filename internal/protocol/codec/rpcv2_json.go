package codec

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/overcast-sh/overcast/internal/protocol"
)

type rpcv2JSON struct{}

var RPCv2JSON Codec = rpcv2JSON{}

const (
	contentTypeRPCv2JSON    = "application/json"
	smithyProtocolRPCv2JSON = "rpc-v2-json"
)

func (rpcv2JSON) Name() string { return NameRPCv2JSON }

func (rpcv2JSON) Decode(r *http.Request, into any) *protocol.AWSError {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(into); err != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		return protocol.ErrInvalidArgument("The request body could not be parsed as JSON: " + err.Error())
	}
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	return nil
}

func (rpcv2JSON) WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		v = struct{}{}
	}
	body, err := json.Marshal(v)
	if err != nil {
		RPCv2JSON.WriteError(w, r, protocol.ErrInternalError)
		return
	}
	writeRPCv2JSON(w, r, status, body)
}

func (rpcv2JSON) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	if rec, ok := w.(errorRecorder); ok {
		rec.RecordAWSError(aerr)
	}
	body, err := json.Marshal(struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}{Type: aerr.Code, Message: aerr.Message})
	if err != nil {
		body = []byte("{}")
	}
	writeRPCv2JSON(w, r, aerr.HTTPStatus, body)
}

func writeRPCv2JSON(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}
	w.Header().Set("Content-Type", contentTypeRPCv2JSON)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Smithy-Protocol", smithyProtocolRPCv2JSON)
	w.Header().Set("x-amzn-requestid", protocol.RequestIDFromContext(r.Context()))
	if status == http.StatusNotImplemented {
		w.Header().Set("x-emulator-unsupported", "true")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
