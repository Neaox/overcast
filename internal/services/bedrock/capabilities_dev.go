//go:build dev

package bedrock

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Inference
		capabilities.Capability{Service: "bedrock", Operation: "InvokeModel", Category: "Inference",
			Status: capabilities.StatusSupported, Notes: "POST /model/{modelId}/invoke — invokes model",
			DocsURL: "[docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html)"},
		capabilities.Capability{Service: "bedrock", Operation: "Converse", Category: "Inference",
			Status: capabilities.StatusSupported, Notes: "POST /model/{modelId}/converse — chat API",
			DocsURL: "[docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html)"},
	)
}
