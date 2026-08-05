//go:build dev

package lambda

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Function management
		capabilities.Capability{Service: "lambda", Operation: "ListFunctions", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns all stored functions; empty list if none"},
		capabilities.Capability{Service: "lambda", Operation: "CreateFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Stores metadata; validates runtime; deprecated runtimes rejected; auto-creates CWL log group; VpcConfig and ImageConfig supported; FileSystemConfigs round-trip (EFS mounts in live mode; S3 Files runtime mounts tracked in #647)"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunction", Category: "Function management",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "GetFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns FunctionConfiguration + Code location block"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionConfiguration", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns FunctionConfiguration only (no Code block)"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionCode", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Updates code zip or image URI and Architectures; generates new RevisionId"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionConfiguration", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Presence-aware updates for supported configuration; unsupported advanced fields fail before mutation"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionCodeSigningConfig", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns the associated config; ResourceNotFoundException when the function has none"},
		capabilities.Capability{Service: "lambda", Operation: "PutFunctionCodeSigningConfig", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Stores the association and validates the ARN shape; signature validation is not emulated"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunctionCodeSigningConfig", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Removes the association; idempotent"},

		// Resource-based policies
		capabilities.Capability{Service: "lambda", Operation: "AddPermission", Category: "Resource-based policies",
			Status: capabilities.StatusUnsupported, Notes: "Policy lifecycle and validation are implemented, but statements are not yet enforced during invocation (#629)"},
		capabilities.Capability{Service: "lambda", Operation: "GetPolicy", Category: "Resource-based policies",
			Status: capabilities.StatusSupported, Notes: "Returns the stored AWS policy document and revision ID"},
		capabilities.Capability{Service: "lambda", Operation: "RemovePermission", Category: "Resource-based policies",
			Status: capabilities.StatusSupported, Notes: "Removes a statement by ID; supports revision preconditions"},

		// Code signing configurations
		capabilities.Capability{Service: "lambda", Operation: "CreateCodeSigningConfig", Category: "Code signing",
			Status: capabilities.StatusSupported, Notes: "Stored as a real resource; AllowedPublishers required, UntrustedArtifactOnDeployment defaults to Warn"},
		capabilities.Capability{Service: "lambda", Operation: "GetCodeSigningConfig", Category: "Code signing",
			Status: capabilities.StatusSupported, Notes: "Returns the stored configuration"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateCodeSigningConfig", Category: "Code signing",
			Status: capabilities.StatusSupported, Notes: "Partial update; omitted members keep their stored value"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteCodeSigningConfig", Category: "Code signing",
			Status: capabilities.StatusSupported, Notes: "ResourceConflictException while a function still references it"},
		capabilities.Capability{Service: "lambda", Operation: "ListCodeSigningConfigs", Category: "Code signing",
			Status: capabilities.StatusSupported, Notes: "Region-scoped; pagination not implemented"},
		capabilities.Capability{Service: "lambda", Operation: "ListFunctionsByCodeSigningConfig", Category: "Code signing",
			Status: capabilities.StatusSupported, Notes: "Returns the ARNs of functions referencing the configuration"},

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

		// Function URLs
		capabilities.Capability{Service: "lambda", Operation: "CreateFunctionUrlConfig", Category: "Function URLs",
			Status: capabilities.StatusSupported, Notes: "FunctionUrl always echoes the caller's Host (see docs/networking.md); AuthType stored but never enforced"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionUrlConfig", Category: "Function URLs",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionUrlConfig", Category: "Function URLs",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunctionUrlConfig", Category: "Function URLs",
			Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "lambda", Operation: "ListFunctionUrlConfigs", Category: "Function URLs",
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
			Status: capabilities.StatusSupported, Notes: "Enforced: over-limit invokes get 429 TooManyRequestsException; 0 throttles the function entirely"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Returns 404 if no concurrency limit is set"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Clears reserved concurrency limit; returns 204"},
		capabilities.Capability{Service: "lambda", Operation: "PutProvisionedConcurrencyConfig", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Pre-warms the requested execution environments in the background (IN_PROGRESS then READY); FAILED when Docker is unavailable"},
		capabilities.Capability{Service: "lambda", Operation: "GetProvisionedConcurrencyConfig", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Reports live Allocated/Available; ProvisionedConcurrencyConfigNotFoundException if not set"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteProvisionedConcurrencyConfig", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Releases the reservation; the environments age out on the idle TTL rather than being killed"},
		capabilities.Capability{Service: "lambda", Operation: "ListProvisionedConcurrencyConfigs", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Single page; NextMarker is always null"},
	)
}
