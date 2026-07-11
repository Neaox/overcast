package codec

import (
	"io"
	"net/http"
	"strconv"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/internal/protocol"
)

type rpcv2CBOR struct{}

type errorRecorder interface {
	RecordAWSError(*protocol.AWSError)
}

var RPCv2CBOR Codec = rpcv2CBOR{}

const (
	contentTypeCBOR         = "application/cbor"
	smithyProtocolRPCv2CBOR = "rpc-v2-cbor"
)

func (rpcv2CBOR) Name() string { return NameRPCv2CBOR }

func (rpcv2CBOR) Decode(r *http.Request, into any) *protocol.AWSError {
	if r.Body == nil {
		return nil
	}
	dec := cborlib.NewDecoder(r.Body)
	if err := dec.Decode(into); err != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		return protocol.ErrInvalidArgument(
			"The request body could not be parsed as CBOR: " + err.Error(),
		)
	}
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	return nil
}

func (rpcv2CBOR) WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		v = struct{}{}
	}
	body, err := cborlib.Marshal(v)
	if err != nil {
		RPCv2CBOR.WriteError(w, r, protocol.ErrInternalError)
		return
	}
	writeCBOR(w, r, status, body)
}

func (rpcv2CBOR) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	if rec, ok := w.(errorRecorder); ok {
		rec.RecordAWSError(aerr)
	}
	body, err := cborlib.Marshal(map[string]string{
		"__type":  aerr.Code,
		"message": aerr.Message,
	})
	if err != nil {
		body = []byte{}
	}
	writeCBOR(w, r, aerr.HTTPStatus, body)
}

func writeCBOR(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}
	w.Header().Set("Content-Type", contentTypeCBOR)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Smithy-Protocol", smithyProtocolRPCv2CBOR)
	w.Header().Set("x-amzn-requestid", protocol.RequestIDFromContext(r.Context()))
	if status == http.StatusNotImplemented {
		w.Header().Set("x-emulator-unsupported", "true")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
