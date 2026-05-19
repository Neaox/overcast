package codec

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// json10 implements Codec for AWS JSON 1.0 (Content-Type
// application/x-amz-json-1.0). Used by SQS, DynamoDB, and Step Functions.
//
// Wraps protocol.WriteAWSJSON / protocol.WriteJSONError so wire bytes are
// byte-identical to the legacy direct-call path.
type json10 struct{}

// JSON10 is the singleton AWS JSON 1.0 codec.
var JSON10 Codec = json10{}

const contentTypeJSON10 = "application/x-amz-json-1.0"

func (json10) Name() string { return NameAWSJSON10 }

func (json10) Decode(r *http.Request, into any) *protocol.AWSError {
	return decodeJSONBody(r, into)
}

func (json10) WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		v = struct{}{}
	}
	protocol.WriteAWSJSON(w, r, status, v, contentTypeJSON10)
}

func (json10) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	// protocol.WriteJSONError uses application/x-amz-json-1.0 and matches
	// the legacy SQS/DynamoDB error format byte-for-byte.
	protocol.WriteJSONError(w, r, aerr)
}

// decodeJSONBody is shared between json10 and json11 — the body format is
// identical, only the response Content-Type differs.
func decodeJSONBody(r *http.Request, into any) *protocol.AWSError {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		// Drain so the connection can be reused even after a parse error.
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		return protocol.ErrInvalidArgument(
			"The request body could not be parsed as JSON: " + err.Error(),
		)
	}
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	return nil
}
