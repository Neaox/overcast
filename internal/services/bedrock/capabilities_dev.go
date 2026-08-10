//go:build dev

package bedrock

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Inference
		capabilities.Capability{Service: "bedrock", Operation: "InvokeModel", Category: "Inference",
			Status: capabilities.StatusInert, Notes: "POST /model/{modelId}/invoke — accepted and answered 200; the required payload is not parsed and no model runs. The response body is an opaque blob whose format belongs to the model, which Overcast does not emulate, so it returns one self-describing field rather than any model family's shape",
			DocsURL: "[docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html)"},
		capabilities.Capability{Service: "bedrock", Operation: "Converse", Category: "Inference",
			Status: capabilities.StatusInert, Notes: "POST /model/{modelId}/converse — accepted and answered with a complete ConverseResponse, every @required member present; no inference runs, so the assistant text is canned and the token counts and latency are zero",
			DocsURL: "[docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html)"},
	)
}
