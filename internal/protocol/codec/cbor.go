package codec

import (
	"io"
	"net/http"
	"reflect"
	"strconv"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/internal/protocol"
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

// cborDecMode decodes a CBOR map into an `any` field as map[string]any rather
// than the library default map[any]any.
//
// This is not a style preference. A member typed `any` in a request struct —
// CloudWatch's PutMetricAlarm Metrics and EvaluationCriteria are the ones that
// found it — holds whatever the caller sent, and a service then hands that
// value on to code that expects the shape encoding/json produces: persisted
// through json.Marshal, compared, logged. json.Marshal refuses map[any]any
// outright, so the whole request failed with a 500 InternalError that named
// nothing, and only over CBOR. Every other protocol Overcast decodes goes
// through encoding/json, so this is what makes an `any` member mean the same
// thing on the CBOR door as on the others.
//
// A CBOR map with non-string keys now fails the decode instead, which is the
// right answer: Smithy documents and structures are string-keyed, so such a
// body was never a valid request for any operation.
var cborDecMode = func() cborlib.DecMode {
	mode, err := cborlib.DecOptions{
		DefaultMapType: reflect.TypeOf(map[string]any(nil)),
	}.DecMode()
	if err != nil {
		// The options above are constant, so this cannot fail at runtime; a
		// build that changes them and does fail should say so immediately
		// rather than silently decode differently.
		panic("codec: building CBOR decode mode: " + err.Error())
	}
	return mode
}()

func (rpcv2CBOR) Name() string { return NameRPCv2CBOR }

func (rpcv2CBOR) Decode(r *http.Request, into any) *protocol.AWSError {
	if r.Body == nil {
		return nil
	}
	dec := cborDecMode.NewDecoder(r.Body)
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
