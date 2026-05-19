//go:build dev

package lambda

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Function management
		capabilities.Capability{Service: "lambda", Operation: "ListFunctions", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns all stored functions; empty list if none"},
		capabilities.Capability{Service: "lambda", Operation: "CreateFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Stores metadata; validates runtime; deprecated runtimes rejected; auto-creates CWL log group; VpcConfig and ImageConfig supported"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunction", Category: "Function management",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "GetFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns FunctionConfiguration + Code location block"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionConfiguration", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns FunctionConfiguration only (no Code block)"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionCode", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Updates code zip; generates new RevisionId"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionConfiguration", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Patches Timeout/MemorySize/Description/Handler/Role/Environment/Layers/VpcConfig/ImageConfig; generates new RevisionId"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionCodeSigningConfig", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Always returns ResourceNotFoundException; code signing is not enforced by the emulator"},

		// Invocation
		capabilities.Capability{Service: "lambda", Operation: "Invoke", Category: "Invocation",
			Status: capabilities.StatusSupported, Notes: "Container-based execution via Docker; falls back to stub when Docker unavailable"},
		capabilities.Capability{Service: "lambda", Operation: "InvokeAsync", Category: "Invocation",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "lambda", Operation: "InvokeWithResponseStream", Category: "Invocation",
			Status: capabilities.StatusSupported, Notes: "Invokes synchronously, wraps result in AWS event stream binary encoding (initial-response → PayloadChunk → InvokeComplete); RequestResponse only"},

		// Aliases & versions
		capabilities.Capability{Service: "lambda", Operation: "PublishVersion", Category: "Aliases & versions",
			Status: capabilities.StatusSupported, Notes: "Immutable snapshot of function config; version numbers are monotonically incrementing integers"},
		capabilities.Capability{Service: "lambda", Operation: "ListVersionsByFunction", Category: "Aliases & versions",
			Status: capabilities.StatusSupported, Notes: "Always includes `$LATEST` as first entry"},
		capabilities.Capability{Service: "lambda", Operation: "CreateAlias", Category: "Aliases & versions",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "UpdateAlias", Category: "Aliases & versions",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "DeleteAlias", Category: "Aliases & versions",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "GetAlias", Category: "Aliases & versions",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "ListAliases", Category: "Aliases & versions",
			Status: capabilities.StatusSupported},

		// Event source mappings
		capabilities.Capability{Service: "lambda", Operation: "CreateEventSourceMapping", Category: "Event source mappings",
			Status: capabilities.StatusSupported, Notes: "SQS→Lambda, DynamoDB Streams→Lambda"},
		capabilities.Capability{Service: "lambda", Operation: "GetEventSourceMapping", Category: "Event source mappings",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "UpdateEventSourceMapping", Category: "Event source mappings",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "DeleteEventSourceMapping", Category: "Event source mappings",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "ListEventSourceMappings", Category: "Event source mappings",
			Status: capabilities.StatusSupported, Notes: "Filters by `FunctionName` and `EventSourceArn`"},

		// Layers
		capabilities.Capability{Service: "lambda", Operation: "PublishLayerVersion", Category: "Layers",
			Status: capabilities.StatusSupported, Notes: "Increments per-layer version counter; stores zip content"},
		capabilities.Capability{Service: "lambda", Operation: "GetLayerVersion", Category: "Layers",
			Status: capabilities.StatusSupported, Notes: "Returns metadata and content info for the specified version"},
		capabilities.Capability{Service: "lambda", Operation: "ListLayerVersions", Category: "Layers",
			Status: capabilities.StatusSupported, Notes: "Returns all versions for a layer, newest first"},
		capabilities.Capability{Service: "lambda", Operation: "ListLayers", Category: "Layers",
			Status: capabilities.StatusSupported, Notes: "Returns distinct layer names with their latest matching version"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteLayerVersion", Category: "Layers",
			Status: capabilities.StatusSupported, Notes: "Removes the specific layer version; 404 if not found"},

		// Concurrency & configuration
		capabilities.Capability{Service: "lambda", Operation: "PutFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Stores reserved concurrency limit; 0 = throttled"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Returns 404 if no concurrency limit is set"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Clears reserved concurrency limit; returns 204"},
		capabilities.Capability{Service: "lambda", Operation: "PutProvisionedConcurrencyConfig", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Stores config per qualifier; immediately reports Status=READY (no actual provisioning)"},
		capabilities.Capability{Service: "lambda", Operation: "GetProvisionedConcurrencyConfig", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Returns ProvisionedConcurrencyConfigNotFoundException if not set"},
	)
}
