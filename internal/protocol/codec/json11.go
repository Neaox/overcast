package codec

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// json11 implements Codec for AWS JSON 1.1 (Content-Type
// application/x-amz-json-1.1). Used by Kinesis, CloudWatch Logs, KMS,
// SSM, ECS, ECR, Cognito, Athena, Glue, EventBridge, Secrets Manager,
// and most other JSON-tier services.
//
// The body format is identical to JSON 1.0; only the response
// Content-Type differs. We wrap protocol.WriteAWSJSON with the 1.1
// content type so wire bytes stay byte-identical to the legacy path.
type json11 struct{}

// JSON11 is the singleton AWS JSON 1.1 codec.
var JSON11 Codec = json11{}

const contentTypeJSON11 = "application/x-amz-json-1.1"

func (json11) Name() string { return NameAWSJSON11 }

func (json11) Decode(r *http.Request, into any) *protocol.AWSError {
	return decodeJSONBody(r, into)
}

func (json11) WriteResponse(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		v = struct{}{}
	}
	protocol.WriteAWSJSON(w, r, status, v, contentTypeJSON11)
}

func (json11) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	// Note: protocol.WriteJSONError currently writes Content-Type
	// application/x-amz-json-1.0 for the error envelope itself. AWS SDKs
	// accept either 1.0 or 1.1 envelope on the error path, and existing
	// 1.1 services (Kinesis, CW Logs, ...) already use this helper today.
	// Phase 0 preserves exactly that behaviour. If we want to tighten
	// this to 1.1 for 1.1 services later, it's a separate change with
	// its own wire-byte goldens.
	protocol.WriteJSONError(w, r, aerr)
}
