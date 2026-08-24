//go:build dev

package lambda

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Function management
		capabilities.Capability{Service: "lambda", Operation: "ListFunctions", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns all stored functions; empty list if none"},
		capabilities.Capability{Service: "lambda", Operation: "CreateFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Stores metadata; validates Runtime against the pinned model and refuses runtimes past AWS's block-create date; a modeled runtime with no execution image returns 501 and persists nothing; auto-creates CWL log group; VpcConfig and ImageConfig supported; LoggingConfig honoured for both Text and JSON LogFormat, with ApplicationLogLevel/SystemLogLevel filtering in JSON mode; FileSystemConfigs round-trip (EFS mounts in live mode; S3 Files runtime mounts tracked in #647); TracingConfig, EphemeralStorage and KMSKeyArn are validated, stored and echoed but change nothing — X-Ray tracing is not emulated, the ephemeral storage size is not enforced on the container, and environment variables are not encrypted at rest; DeadLetterConfig is validated, stored, echoed and honoured — a failed asynchronous invocation is delivered to the SQS queue or SNS topic it names; Code.S3ObjectVersion fetches that version of the object; an unfetchable Code.S3Bucket/S3Key/S3ObjectVersion fails the create and persists nothing"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Qualifier deletes only that published version, with its qualified policy and provisioned concurrency; refuses $LATEST and versions an alias references; unqualified deletes the function"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunction", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns FunctionConfiguration + Code location block; TracingConfig and EphemeralStorage always present, defaulting to PassThrough and 512 MB as on AWS; DeadLetterConfig reported only when the function has one"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionConfiguration", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Returns FunctionConfiguration only (no Code block); TracingConfig and EphemeralStorage always present, defaulting to PassThrough and 512 MB as on AWS; DeadLetterConfig reported only when the function has one"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionCode", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Updates code zip or image URI and Architectures; S3ObjectVersion fetches that version of the object, and a version that cannot be read leaves the function untouched; generates new RevisionId"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionConfiguration", Category: "Function management",
			Status: capabilities.StatusSupported, Notes: "Presence-aware updates for supported configuration, including TracingConfig, EphemeralStorage and KMSKeyArn (recorded and echoed, never enforced) and DeadLetterConfig (an explicit empty TargetArn removes the target); a Runtime past AWS's block-update date is refused; LoggingConfig with explicit members applies, including LogFormat JSON, but an explicitly empty LoggingConfig object still returns 501 because AWS's semantics for it are uncaptured (#660); unsupported advanced fields fail before mutation"},
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
			Status: capabilities.StatusSupported, Notes: "Container-based execution via Docker; falls back to stub when Docker unavailable; under LogFormat JSON the START/END/REPORT lines become Telemetry-API-shaped platform.start, platform.runtimeDone and platform.report records, filtered by SystemLogLevel, and function output is filtered by ApplicationLogLevel; the in-container init also reports the INIT phase as platform.initStart, platform.initRuntimeDone and platform.initReport, ordered against the phase's own output, and Telemetry/Logs API subscribers receive every platform record in either log format — subscriptions via the Logs API (2020-08-15) and the Telemetry API (2022-07-01, schemaVersion-aware, with the documented cross-API exclusivity), invocation records carrying the runtime's real X-Amzn-Trace-Id, platform.runtimeDone metrics and its responseLatency span measured by the in-container init, errorType Runtime.ExitError on a crashed runtime's records, deliveries batched per the subscription's buffering configuration, and a lost delivery reported to its subscriber as platform.logsDropped; an InvocationType=Event invocation whose function errors is retried per the function's FunctionEventInvokeConfig (AWS's default of twice, waiting AWS's one minute then two, when unconfigured) and then delivered to its on-failure destination and its DeadLetterConfig target; MaximumEventAgeInSeconds is measured from acceptance and discards the event before the next attempt rather than after it, but the resulting record's condition reads RetriesExhausted because AWS does not document a distinct value for an aged-out event; records AWS/Lambda CloudWatch metrics (Invocations, Errors, Duration, Throttles, ConcurrentExecutions — service-metrics-platform.md Lambda pilot) at this outcome boundary for every invocation mechanism (sync, async, function URLs, event source mappings); DryRun never invokes and so never records; Resource/ExecutedVersion metric dimensions are not recorded yet"},
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
			Status: capabilities.StatusSupported, Notes: "SQS→Lambda, DynamoDB Streams→Lambda; `FunctionResponseTypes: [\"ReportBatchItemFailures\"]` is honoured; Tags are stored and readable through ListTags"},
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

		// Asynchronous invocation configuration
		capabilities.Capability{Service: "lambda", Operation: "PutFunctionEventInvokeConfig", Category: "Asynchronous invocation",
			Status: capabilities.StatusSupported, Notes: "Overwrites the configuration, removing members the request omits; MaximumRetryAttempts and MaximumEventAgeInSeconds are validated against AWS's ranges and honoured by the async invoke path; SQS, SNS, Lambda and EventBridge destinations receive AWS's invocation record; an S3 on-failure destination returns 501 because the record is not written to S3, and an S3 on-success destination is rejected as AWS rejects it"},
		capabilities.Capability{Service: "lambda", Operation: "UpdateFunctionEventInvokeConfig", Category: "Asynchronous invocation",
			Status: capabilities.StatusSupported, Notes: "Partial update; members the request omits keep their stored value, which is the only difference from Put"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionEventInvokeConfig", Category: "Asynchronous invocation",
			Status: capabilities.StatusSupported, Notes: "ResourceNotFoundException when the function has no configuration; LastModified is Unix seconds, as AWS returns for this resource"},
		capabilities.Capability{Service: "lambda", Operation: "DeleteFunctionEventInvokeConfig", Category: "Asynchronous invocation",
			Status: capabilities.StatusSupported, Notes: "Returns 204; ResourceNotFoundException when there is no configuration to delete"},
		capabilities.Capability{Service: "lambda", Operation: "ListFunctionEventInvokeConfigs", Category: "Asynchronous invocation",
			Status: capabilities.StatusSupported, Notes: "Every qualifier's configuration for the function; MaxItems is validated but the result is a single page, so NextMarker is never returned"},

		// Concurrency & configuration
		capabilities.Capability{Service: "lambda", Operation: "PutFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "Enforced: over-limit invokes get 429 TooManyRequestsException; 0 throttles the function entirely"},
		capabilities.Capability{Service: "lambda", Operation: "GetFunctionConcurrency", Category: "Concurrency & configuration",
			Status: capabilities.StatusSupported, Notes: "A function with no reservation answers 200 with an empty body, as on AWS; a reservation of 0 is reported rather than omitted, since 0 is the documented way to switch a function off. ResourceNotFoundException is for the function itself"},
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

		// Tags
		capabilities.Capability{Service: "lambda", Operation: "TagResource", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "Function and event-source-mapping ARNs; merges tags; max 50; validates key/value lengths; rejects qualified ARNs and non-ARN resources"},
		capabilities.Capability{Service: "lambda", Operation: "UntagResource", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "Removes specified keys; idempotent on missing keys; tagKeys is required"},
		capabilities.Capability{Service: "lambda", Operation: "ListTags", Category: "Tags",
			Status: capabilities.StatusSupported, Notes: "Returns the resource's tags; code-signing-config, capacity-provider and network-connector ARNs return 501"},
	)
}
