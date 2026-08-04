package lambda

// handler_functions.go — implemented function management handlers.
//
// Implemented:
//   - ListFunctions         GET  /2015-03-31/functions
//   - CreateFunction        POST /2015-03-31/functions
//   - GetFunction           GET  /2015-03-31/functions/{name}
//   - GetFunctionConfiguration GET /2015-03-31/functions/{name}/configuration
//   - UpdateFunctionCode    PUT  /2015-03-31/functions/{name}/code
//   - UpdateFunctionConfiguration PUT /2015-03-31/functions/{name}/configuration
//   - DeleteFunction        DELETE /2015-03-31/functions/{name}
//   - GetFunctionCodeSigningConfig    GET    /2020-06-30/functions/{name}/code-signing-config
//   - PutFunctionCodeSigningConfig    PUT    /2020-06-30/functions/{name}/code-signing-config
//   - DeleteFunctionCodeSigningConfig DELETE /2020-06-30/functions/{name}/code-signing-config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── Wire format types ───────────────────────────────────────────────────────

// functionConfiguration is the full FunctionConfiguration shape returned by
// CreateFunction, GetFunction, GetFunctionConfiguration, UpdateFunctionCode,
// and UpdateFunctionConfiguration. All optional fields use omitempty so that
// nil/zero values are omitted, matching the real AWS SDK wire format.
//
// https://docs.aws.amazon.com/lambda/latest/api/API_FunctionConfiguration.html
type functionConfiguration struct {
	FunctionName      string             `json:"FunctionName"`
	FunctionArn       string             `json:"FunctionArn"`
	Runtime           string             `json:"Runtime,omitempty"`
	Handler           string             `json:"Handler,omitempty"`
	Role              string             `json:"Role,omitempty"`
	Description       string             `json:"Description,omitempty"`
	Timeout           int                `json:"Timeout,omitempty"`
	MemorySize        int                `json:"MemorySize,omitempty"`
	CodeSize          int64              `json:"CodeSize,omitempty"`
	LastModified      string             `json:"LastModified,omitempty"`
	RevisionId        string             `json:"RevisionId,omitempty"`
	PackageType       string             `json:"PackageType,omitempty"`
	Architectures     []string           `json:"Architectures,omitempty"`
	State             string             `json:"State,omitempty"`
	StateReason       string             `json:"StateReason,omitempty"`
	StateReasonCode   string             `json:"StateReasonCode,omitempty"`
	CodeSha256        string             `json:"CodeSha256,omitempty"`
	Environment       *functionEnvConf   `json:"Environment,omitempty"`
	LoggingConfig     *loggingConfig     `json:"LoggingConfig,omitempty"`
	Layers            []LayerVersionLink `json:"Layers,omitempty"`
	ImageUri          string             `json:"ImageUri,omitempty"`
	ImageConfig       *imageConfigWire   `json:"ImageConfig,omitempty"`
	VpcConfig         *vpcConfigResponse `json:"VpcConfig,omitempty"`
	FileSystemConfigs []FileSystemConfig `json:"FileSystemConfigs,omitempty"`
}

// imageConfigWire is the AWS wire format for ImageConfig.
type imageConfigWire struct {
	EntryPoint       []string `json:"EntryPoint,omitempty"`
	Command          []string `json:"Command,omitempty"`
	WorkingDirectory string   `json:"WorkingDirectory,omitempty"`
}

type functionEnvConf struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

// loggingConfig carries the auto-created CWL log group for the function.
type loggingConfig struct {
	LogGroup            string `json:"LogGroup,omitempty"`
	LogFormat           string `json:"LogFormat,omitempty"`
	ApplicationLogLevel string `json:"ApplicationLogLevel,omitempty"`
	SystemLogLevel      string `json:"SystemLogLevel,omitempty"`
}

// createFunctionRequest matches the AWS CreateFunction request body.
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateFunction.html
type createFunctionRequest struct {
	FunctionName  string            `json:"FunctionName"`
	Runtime       string            `json:"Runtime"`
	Handler       string            `json:"Handler"`
	Role          string            `json:"Role"`
	Description   string            `json:"Description,omitempty"`
	Timeout       int               `json:"Timeout,omitempty"`
	MemorySize    int               `json:"MemorySize,omitempty"`
	Environment   *envVariables     `json:"Environment,omitempty"`
	Code          *functionCode     `json:"Code,omitempty"`
	PackageType   string            `json:"PackageType,omitempty"`
	Architectures []string          `json:"Architectures,omitempty"`
	Tags          map[string]string `json:"Tags,omitempty"`
	LoggingConfig *loggingConfig    `json:"LoggingConfig,omitempty"`
	VpcConfig     *vpcConfigRequest `json:"VpcConfig,omitempty"`
	ImageConfig   *imageConfigWire  `json:"ImageConfig,omitempty"`
	// FileSystemConfigs mount EFS access points into the execution environment.
	FileSystemConfigs []FileSystemConfig `json:"FileSystemConfigs,omitempty"`
	// Optional; AWS requires only FunctionName, Role and Code.
	CodeSigningConfigArn string `json:"CodeSigningConfigArn,omitempty"`
	// Layers is a list of layer version ARNs to attach to the function at creation.
	Layers                  []string        `json:"Layers,omitempty"`
	DeadLetterConfig        json.RawMessage `json:"DeadLetterConfig"`
	TracingConfig           json.RawMessage `json:"TracingConfig"`
	EphemeralStorage        json.RawMessage `json:"EphemeralStorage"`
	KMSKeyArn               json.RawMessage `json:"KMSKeyArn"`
	SnapStart               json.RawMessage `json:"SnapStart"`
	RuntimeManagementConfig json.RawMessage `json:"RuntimeManagementConfig"`
	RecursiveLoop           json.RawMessage `json:"RecursiveLoop"`
}

type envVariables struct {
	Variables map[string]string `json:"Variables"`
}

type functionCode struct {
	ZipFile         []byte `json:"ZipFile,omitempty"`
	S3Bucket        string `json:"S3Bucket,omitempty"`
	S3Key           string `json:"S3Key,omitempty"`
	S3ObjectVersion string `json:"S3ObjectVersion,omitempty"`
	ImageUri        string `json:"ImageUri,omitempty"`
}

// updateFunctionCodeRequest matches AWS UpdateFunctionCode request body.
type updateFunctionCodeRequest struct {
	ZipFile       []byte   `json:"ZipFile,omitempty"`
	S3Bucket      string   `json:"S3Bucket,omitempty"`
	S3Key         string   `json:"S3Key,omitempty"`
	ImageUri      string   `json:"ImageUri,omitempty"`
	Architectures []string `json:"Architectures,omitempty"`
}

// updateFunctionConfigurationRequest matches AWS UpdateFunctionConfiguration body.
type updateFunctionConfigurationRequest struct {
	Description   *string        `json:"Description"`
	Handler       *string        `json:"Handler"`
	Role          *string        `json:"Role"`
	Timeout       *int           `json:"Timeout"`
	MemorySize    *int           `json:"MemorySize"`
	Runtime       *string        `json:"Runtime"`
	Environment   *envVariables  `json:"Environment,omitempty"`
	LoggingConfig *loggingConfig `json:"LoggingConfig"`
	// Layers is a list of layer version ARNs to attach. An empty slice clears all layers.
	// A nil field means "no change".
	Layers      []string          `json:"Layers,omitempty"`
	VpcConfig   *vpcConfigRequest `json:"VpcConfig,omitempty"`
	ImageConfig *imageConfigWire  `json:"ImageConfig,omitempty"`
	// FileSystemConfigs replaces the function's EFS mounts. An empty slice
	// clears them; a nil field means "no change".
	FileSystemConfigs       []FileSystemConfig `json:"FileSystemConfigs,omitempty"`
	DeadLetterConfig        json.RawMessage    `json:"DeadLetterConfig"`
	TracingConfig           json.RawMessage    `json:"TracingConfig"`
	EphemeralStorage        json.RawMessage    `json:"EphemeralStorage"`
	KMSKeyArn               json.RawMessage    `json:"KMSKeyArn"`
	SnapStart               json.RawMessage    `json:"SnapStart"`
	RuntimeManagementConfig json.RawMessage    `json:"RuntimeManagementConfig"`
	RecursiveLoop           json.RawMessage    `json:"RecursiveLoop"`
}

// getFunctionResponse matches AWS GetFunction response body.
// https://docs.aws.amazon.com/lambda/latest/api/API_GetFunction.html
type getFunctionResponse struct {
	Configuration functionConfiguration `json:"Configuration"`
	Code          *getFunctionCode      `json:"Code,omitempty"`
	Tags          map[string]string     `json:"Tags,omitempty"`
}

type getFunctionCode struct {
	Location       string `json:"Location"`
	RepositoryType string `json:"RepositoryType"`
}

// vpcConfigRequest matches the VpcConfig block in create/update requests.
type vpcConfigRequest struct {
	SubnetIds               []string `json:"SubnetIds,omitempty"`
	SecurityGroupIds        []string `json:"SecurityGroupIds,omitempty"`
	Ipv6AllowedForDualStack bool     `json:"Ipv6AllowedForDualStack,omitempty"`
}

// vpcConfigResponse is the VpcConfig block returned in function configuration.
type vpcConfigResponse struct {
	SubnetIds               []string `json:"SubnetIds"`
	SecurityGroupIds        []string `json:"SecurityGroupIds"`
	Ipv6AllowedForDualStack bool     `json:"Ipv6AllowedForDualStack"`
	VpcId                   string   `json:"VpcId"`
}

// listFunctionsResponse matches the AWS Lambda ListFunctions wire format.
type listFunctionsResponse struct {
	Functions  []*functionConfiguration `json:"Functions"`
	NextMarker *string                  `json:"NextMarker,omitempty"`
}

func lambdaInvalidParameter(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterValueException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// localMountPathRe matches AWS's LocalMountPath constraint: an absolute path
// directly under /mnt.
var localMountPathRe = regexp.MustCompile(`^/mnt/[a-zA-Z0-9-_.]+$`)

// validateFileSystemConfigs enforces the AWS FileSystemConfig rules shared by
// CreateFunction and UpdateFunctionConfiguration: at most one config, an EFS
// access-point ARN, and a /mnt/<name> mount path.
// https://docs.aws.amazon.com/lambda/latest/api/API_FileSystemConfig.html
func validateFileSystemConfigs(configs []FileSystemConfig) *protocol.AWSError {
	if len(configs) == 0 {
		return nil
	}
	if len(configs) > 1 {
		return lambdaInvalidParameter("FileSystemConfigs can contain at most 1 config.")
	}
	fsc := configs[0]
	if !strings.Contains(fsc.Arn, ":elasticfilesystem:") || !strings.Contains(fsc.Arn, ":access-point/fsap-") {
		return lambdaInvalidParameter("FileSystemConfigs[0].Arn must be an EFS access point ARN.")
	}
	if !localMountPathRe.MatchString(fsc.LocalMountPath) {
		return lambdaInvalidParameter("LocalMountPath must be an absolute path under /mnt (for example /mnt/efs).")
	}
	return nil
}

func validateArchitectures(architectures []string) *protocol.AWSError {
	if len(architectures) != 1 || (architectures[0] != "x86_64" && architectures[0] != "arm64") {
		return lambdaInvalidParameter("1 validation error detected: Value '" + strings.Join(architectures, ",") + "' at 'architectures' failed to satisfy constraint: Member must satisfy enum value set: [x86_64, arm64]")
	}
	return nil
}

func unsupportedFunctionConfigurationField(fields ...struct {
	name string
	raw  json.RawMessage
}) *protocol.AWSError {
	for _, field := range fields {
		if len(field.raw) > 0 && string(field.raw) != "null" {
			return lambdaInvalidParameter(field.name + " is not supported by this Lambda emulator.")
		}
	}
	return nil
}

// ─── runtime metadata ────────────────────────────────────────────────────────

// RuntimeInfo describes a Lambda runtime with its metadata.
type RuntimeInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Family         string `json:"family"`
	DefaultHandler string `json:"defaultHandler"`
	ImageURI       string `json:"imageUri,omitempty"`
	Deprecated     bool   `json:"deprecated"`
	// Supported indicates the emulator can actually execute this runtime.
	Supported bool `json:"supported"`
}

// deprecatedRuntimes is used for O(1) validation lookups in CreateFunction and
// UpdateFunctionConfiguration. Sourced from the known-deprecated set plus
// legacy runtime IDs that are too old to appear on ECR.
var deprecatedRuntimes = func() map[string]bool {
	m := make(map[string]bool, len(knownDeprecated))
	for id := range knownDeprecated {
		m[id] = true
	}
	return m
}()

// ─── conversion helpers ───────────────────────────────────────────────────────

func functionToConfig(fn *Function) *functionConfiguration {
	// The internal "image" sentinel is not exposed in API responses;
	// image functions have no Runtime field on the wire.
	runtime := fn.Runtime
	if runtime == "image" {
		runtime = ""
	}

	cfg := &functionConfiguration{
		FunctionName:    fn.Name,
		FunctionArn:     fn.ARN,
		Runtime:         runtime,
		Handler:         fn.Handler,
		Role:            fn.Role,
		Description:     fn.Description,
		Timeout:         fn.Timeout,
		MemorySize:      fn.MemorySize,
		CodeSize:        fn.CodeSize,
		CodeSha256:      codeSha256(fn),
		LastModified:    fn.LastModified,
		RevisionId:      fn.RevisionId,
		PackageType:     fn.PackageType,
		Architectures:   fn.Architectures,
		State:           fn.State,
		StateReason:     fn.StateReason,
		StateReasonCode: fn.StateReasonCode,
		ImageUri:        fn.ImageUri,
		LoggingConfig: &loggingConfig{
			LogGroup:            fn.logGroupName(),
			LogFormat:           fn.LogFormat,
			ApplicationLogLevel: fn.ApplicationLogLevel,
			SystemLogLevel:      fn.SystemLogLevel,
		},
	}
	if cfg.LoggingConfig.LogFormat == "" {
		cfg.LoggingConfig.LogFormat = "Text"
	}
	if len(fn.Environment) > 0 {
		cfg.Environment = &functionEnvConf{Variables: fn.Environment}
	}
	if len(fn.Layers) > 0 {
		cfg.Layers = fn.Layers
	}
	if fn.VpcConfig != nil {
		cfg.VpcConfig = &vpcConfigResponse{
			SubnetIds:               fn.VpcConfig.SubnetIds,
			SecurityGroupIds:        fn.VpcConfig.SecurityGroupIds,
			Ipv6AllowedForDualStack: fn.VpcConfig.Ipv6AllowedForDualStack,
			VpcId:                   fn.VpcConfig.VpcId,
		}
	}
	if fn.ImageConfig != nil {
		cfg.ImageConfig = &imageConfigWire{
			EntryPoint:       fn.ImageConfig.EntryPoint,
			Command:          fn.ImageConfig.Command,
			WorkingDirectory: fn.ImageConfig.WorkingDirectory,
		}
	}
	if len(fn.FileSystemConfigs) > 0 {
		cfg.FileSystemConfigs = fn.FileSystemConfigs
	}
	return cfg
}

// ─── Handler methods ──────────────────────────────────────────────────────────

// ListFunctions handles GET /2015-03-31/functions.
func (h *Handler) ListFunctions(w http.ResponseWriter, r *http.Request) {
	h.log.Debug("list functions")
	fns, aerr := h.ls.listFunctions(r.Context())
	if aerr != nil {
		h.log.Error("list functions: store error", zap.Error(aerr.Unwrap()))
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	configs := make([]*functionConfiguration, 0, len(fns))
	for _, fn := range fns {
		configs = append(configs, functionToConfig(fn))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listFunctionsResponse{Functions: configs})
}

// ListRuntimes handles GET /_lambda/runtimes (emulator-only).
// Fetches available runtimes from ECR Public on first call and caches the result.
func (h *Handler) ListRuntimes(w http.ResponseWriter, _ *http.Request) {
	catalog := h.rtCache.get(h.runtimes.get())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Runtimes []RuntimeInfo `json:"runtimes"`
	}{Runtimes: catalog})
}

// CreateFunction handles POST /2015-03-31/functions.
func (h *Handler) CreateFunction(w http.ResponseWriter, r *http.Request) {
	var req createFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}

	// Validate required fields.
	if req.FunctionName == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("FunctionName"))
		return
	}
	if req.Role == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Role"))
		return
	}
	if aerr := unsupportedFunctionConfigurationField(
		struct {
			name string
			raw  json.RawMessage
		}{"DeadLetterConfig", req.DeadLetterConfig},
		struct {
			name string
			raw  json.RawMessage
		}{"TracingConfig", req.TracingConfig},
		struct {
			name string
			raw  json.RawMessage
		}{"EphemeralStorage", req.EphemeralStorage},
		struct {
			name string
			raw  json.RawMessage
		}{"KMSKeyArn", req.KMSKeyArn},
		struct {
			name string
			raw  json.RawMessage
		}{"SnapStart", req.SnapStart},
		struct {
			name string
			raw  json.RawMessage
		}{"RuntimeManagementConfig", req.RuntimeManagementConfig},
		struct {
			name string
			raw  json.RawMessage
		}{"RecursiveLoop", req.RecursiveLoop},
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Reject deprecated runtimes. The check is done only when a runtime is specified
	// (PackageType=Image functions don't require Runtime).
	h.log.Debug("create function", zap.String("function", req.FunctionName), zap.String("runtime", req.Runtime))
	if req.Runtime != "" && deprecatedRuntimes[req.Runtime] {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"The runtime "+req.Runtime+" is no longer supported. Please update your function to a supported runtime.",
		))
		return
	}

	ctx := r.Context()

	h.log.Debug("create function", zap.String("function", req.FunctionName), zap.String("runtime", req.Runtime))

	// Duplicate check.
	existing, aerr := h.ls.getFunction(ctx, req.FunctionName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceConflictException",
			Message:    "Function already exist: " + req.FunctionName,
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	// Apply AWS defaults.
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 3
	}
	memorySize := req.MemorySize
	if memorySize <= 0 {
		memorySize = 128
	}
	packageType := req.PackageType
	if packageType == "" {
		packageType = "Zip"
	}
	if packageType != "Zip" && packageType != "Image" {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("1 validation error detected: Value '"+packageType+"' at 'packageType' failed to satisfy constraint: Member must satisfy enum value set: [Zip, Image]"))
		return
	}
	if req.Timeout > 900 {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("1 validation error detected: Value '"+fmt.Sprint(req.Timeout)+"' at 'timeout' failed to satisfy constraint: Member must have value less than or equal to 900"))
		return
	}
	if req.MemorySize > 0 && (req.MemorySize < 128 || req.MemorySize > 32768) {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("1 validation error detected: Value '"+fmt.Sprint(req.MemorySize)+"' at 'memorySize' failed to satisfy constraint: Member must have value between 128 and 32768"))
		return
	}

	// Validate package-type-specific required fields from the AWS CreateFunction API.
	if packageType == "Image" {
		if req.Runtime != "" {
			protocol.WriteJSONError(w, r, lambdaInvalidParameter("Runtime is not supported for Image package type functions."))
			return
		}
		if req.Code == nil || req.Code.ImageUri == "" {
			protocol.WriteJSONError(w, r, lambdaInvalidParameter("Please provide ImageUri when PackageType is Image."))
			return
		}
	} else {
		if req.Code == nil {
			protocol.WriteJSONError(w, r, lambdaInvalidParameter("Please provide a source for function code."))
			return
		}
		if req.Runtime == "" {
			protocol.WriteJSONError(w, r, lambdaInvalidParameter("Runtime is required for Zip package type functions."))
			return
		}
		if req.Handler == "" {
			protocol.WriteJSONError(w, r, lambdaInvalidParameter("Handler is required for Zip package type functions."))
			return
		}
	}

	architectures := req.Architectures
	if len(architectures) == 0 {
		architectures = []string{"x86_64"}
	}
	if aerr := validateArchitectures(architectures); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Build the function domain object.
	runtime := req.Runtime
	if packageType == "Image" && runtime == "" {
		// Internal sentinel so ContainerRuntime.CanHandle dispatches correctly.
		runtime = "image"
	}

	fn := &Function{
		Name:            req.FunctionName,
		ARN:             protocol.LambdaARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, req.FunctionName),
		Runtime:         runtime,
		Handler:         req.Handler,
		Role:            req.Role,
		Description:     req.Description,
		Timeout:         timeout,
		MemorySize:      memorySize,
		PackageType:     packageType,
		Architectures:   architectures,
		State:           "Pending",
		StateReason:     "The function is being created.",
		StateReasonCode: "Creating",
		RevisionId:      uuid.NewString(),
		LastModified:    h.clk.Now().UTC().Format(time.RFC3339),
		Tags:            copyTags(req.Tags),
	}
	if req.LoggingConfig != nil {
		fn.LogGroup = req.LoggingConfig.LogGroup
		fn.LogFormat = req.LoggingConfig.LogFormat
		fn.ApplicationLogLevel = req.LoggingConfig.ApplicationLogLevel
		fn.SystemLogLevel = req.LoggingConfig.SystemLogLevel
	}
	if req.CodeSigningConfigArn != "" {
		if !codeSigningConfigARNPattern.MatchString(req.CodeSigningConfigArn) {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterValueException",
				Message:    "Invalid code signing config arn: " + req.CodeSigningConfigArn,
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		exists, aerr := h.codeSigningConfigExists(r, req.CodeSigningConfigArn)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if !exists {
			protocol.WriteJSONError(w, r, errCodeSigningConfigNotFoundForFunction(req.CodeSigningConfigArn))
			return
		}
		fn.CodeSigningConfigArn = req.CodeSigningConfigArn
	}
	if req.Environment != nil {
		fn.Environment = req.Environment.Variables
	}
	if req.Code != nil {
		fn.setCode(req.Code.ZipFile)
		fn.CodeS3Bucket = req.Code.S3Bucket
		fn.CodeS3Key = req.Code.S3Key
		fn.ImageUri = req.Code.ImageUri

		// Eagerly fetch the code from S3 when the caller provided
		// S3Bucket/S3Key but no inline ZipFile. CDK deploys upload the zip
		// to S3 *before* CreateFunction, so the s3SyncWatcher (which only
		// fires on subsequent PutObjects) wouldn't otherwise see this code
		// and the function would invoke with an empty zip.
		if len(fn.CodeZip) == 0 && fn.CodeS3Bucket != "" && fn.CodeS3Key != "" && h.s3Fetch != nil {
			if zip, err := h.s3Fetch(ctx, fn.CodeS3Bucket, fn.CodeS3Key); err == nil {
				fn.setCode(zip)
			} else {
				h.log.Warn("lambda: create function: s3 fetch failed",
					zap.String("function", fn.Name),
					zap.String("bucket", fn.CodeS3Bucket),
					zap.String("key", fn.CodeS3Key),
					zap.Error(err),
				)
			}
		}
	}
	if req.VpcConfig != nil {
		fn.VpcConfig = &VpcConfig{
			SubnetIds:               req.VpcConfig.SubnetIds,
			SecurityGroupIds:        req.VpcConfig.SecurityGroupIds,
			Ipv6AllowedForDualStack: req.VpcConfig.Ipv6AllowedForDualStack,
		}
		// Resolve VpcId from the first subnet, if a resolver is available.
		if resolver := h.getVPCResolver(); resolver != nil && len(req.VpcConfig.SubnetIds) > 0 {
			fn.VpcConfig.VpcId = resolver.VpcIDForSubnet(ctx, req.VpcConfig.SubnetIds[0])
		}
	}
	if len(req.FileSystemConfigs) > 0 {
		if aerr := validateFileSystemConfigs(req.FileSystemConfigs); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		fn.FileSystemConfigs = req.FileSystemConfigs
	}
	if req.ImageConfig != nil {
		fn.ImageConfig = &ImageConfig{
			EntryPoint:       req.ImageConfig.EntryPoint,
			Command:          req.ImageConfig.Command,
			WorkingDirectory: req.ImageConfig.WorkingDirectory,
		}
	}
	if len(req.Layers) > 0 {
		links := make([]LayerVersionLink, 0, len(req.Layers))
		for _, arn := range req.Layers {
			// Real AWS requires layers to be in the same region as the function.
			if layerRegion := serviceutil.ARNRegion(arn); layerRegion != "" {
				if fnRegion := middleware.RegionFromContext(ctx, h.cfg.Region); layerRegion != fnRegion {
					protocol.WriteJSONError(w, r, &protocol.AWSError{
						Code:       "InvalidParameterValueException",
						Message:    "Layer ARN must be in the same region as the function.",
						HTTPStatus: http.StatusBadRequest,
					})
					return
				}
			}
			lv, aerr := h.ls.getLayerVersionByARN(ctx, arn)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			if lv != nil {
				links = append(links, LayerVersionLink{ARN: arn, CodeSize: lv.CodeSize})
			} else {
				links = append(links, LayerVersionLink{ARN: arn})
			}
		}
		fn.Layers = links
	}

	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Kick off the Docker image pull in the background if the runtime is
	// ready. The function stays in "Pending" until the pull completes, then
	// flips to "Active" — matching AWS semantics and keeping the cold-pull
	// cost off the first Invoke. If the runtime isn't wired (tests, or
	// Docker still initialising), mark Active immediately.
	if h.prewarmer != nil {
		h.prewarmer(fn, func(pullErr error) {
			bgCtx := middleware.ContextWithRegion(context.Background(), serviceutil.ARNRegion(fn.ARN))
			nextState, nextReason, nextReasonCode := "Active", "", ""
			if pullErr != nil {
				nextState = "Failed"
				nextReason = "Failed to pull container image: " + pullErr.Error()
				nextReasonCode = "ImagePullError"
				h.log.Warn("lambda: background image pull failed",
					zap.String("function", fn.Name), zap.Error(pullErr))
			}
			// The pull outlives the request that started it, so the function may
			// have been deleted or revised meanwhile. Persisting the create-time
			// snapshot would resurrect a deleted record (the compat
			// lambda-crud/DeleteFunction flake, #414) or clobber a concurrent
			// revision, so the transition is merged into a fresh read instead —
			// the same rule RDS applies when a container start outlives its
			// instance. Retried because the store may be contended during CDK
			// deploy (many concurrent S3 PutObject calls).
			const maxAttempts = 5
			for i := range maxAttempts {
				fresh, aerr := h.ls.getFunction(bgCtx, fn.Name)
				if aerr == nil {
					if fresh == nil || fresh.State != "Pending" {
						return // deleted mid-pull, or already transitioned
					}
					fresh.State = nextState
					fresh.StateReason = nextReason
					fresh.StateReasonCode = nextReasonCode
					if err := h.ls.putFunction(bgCtx, fresh); err == nil {
						if fresh.State == "Active" {
							h.proactive.NoteFunctionChanged(fresh)
						}
						return
					}
				}
				if i < maxAttempts-1 {
					time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
				}
			}
			h.log.Warn("lambda: failed to persist state transition after image pull",
				zap.String("function", fn.Name))
		})
	} else {
		fn.State = "Active"
		fn.StateReason = ""
		fn.StateReasonCode = ""
		_ = h.ls.putFunction(ctx, fn)
		h.proactive.NoteFunctionChanged(fn)
	}

	// Auto-create CloudWatch Logs log group (idempotent). The log stream is
	// created per-invocation with an AWS-style name, not here at create time.
	if h.logWriter != nil {
		if err := h.logWriter.EnsureLogGroup(ctx, fn.logGroupName()); err != nil {
			h.log.Warn("lambda: create function: failed to ensure CWL log group",
				zap.String("function", fn.Name),
				zap.Error(err),
			)
			// Non-fatal — continue.
		}
	}

	// Publish lifecycle event.
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.LambdaFunctionCreated,
			Time:    h.clk.Now(),
			Source:  "lambda",
			Payload: events.LambdaFunctionPayload{Name: fn.Name, ARN: fn.ARN},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(functionToConfig(fn))
}

// ─── Code signing config association ─────────────────────────────────────────
//
// Code signing is optional: a function has a configuration only if one was
// passed to CreateFunction or attached with PutFunctionCodeSigningConfig. All
// three operations are served on the 2020-06-30 API version.
//
// Overcast stores and echoes the association but never validates a signature —
// signing is a security-enforcement feature and Overcast is not a security
// boundary. It also cannot check that the ARN names a configuration that
// exists, because the code signing configuration resource itself
// (CreateCodeSigningConfig and friends, on 2020-04-22) is not implemented; so
// CodeSigningConfigNotFoundException is never returned and the ARN is taken at
// face value once it is well-formed.

// codeSigningConfigARNPattern is AWS's own pattern for the ARN, from the Lambda
// API model. Validating the shape catches a typo at the point of the call the
// way AWS does, without implying the configuration behind it exists.
var codeSigningConfigARNPattern = regexp.MustCompile(
	`^arn:(aws[a-zA-Z-]*)?:lambda:[a-z]{2}((-gov)|(-iso(b?)))?-[a-z]+-\d{1}:\d{12}:code-signing-config:csc-[a-z0-9]{17}$`)

// functionForCodeSigning resolves the named function, writing the AWS-shaped
// 404 and returning nil when it does not exist.
func (h *Handler) functionForCodeSigning(w http.ResponseWriter, r *http.Request) (*Function, string) {
	name := chi.URLParam(r, "name")
	fn, aerr := h.ls.getFunction(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return nil, name
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return nil, name
	}
	return fn, name
}

func writeCodeSigningConfig(w http.ResponseWriter, r *http.Request, arn, name string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(codeSigningConfigResponse{
		CodeSigningConfigArn: arn,
		FunctionName:         name,
	})
}

type codeSigningConfigResponse struct {
	CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
	FunctionName         string `json:"FunctionName"`
}

type putFunctionCodeSigningConfigRequest struct {
	CodeSigningConfigArn string `json:"CodeSigningConfigArn"`
}

// GetFunctionCodeSigningConfig handles GET /2020-06-30/functions/{name}/code-signing-config.
//
// A function with no configuration returns ResourceNotFoundException rather
// than an empty success: CodeSigningConfigArn is a required member of the 200
// response, so "no config" has no representation as a successful result, and
// ResourceNotFoundException is the only not-found error this operation
// declares (CodeSigningConfigNotFoundException is declared on the mutating
// operations, not this one).
func (h *Handler) GetFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	fn, name := h.functionForCodeSigning(w, r)
	if fn == nil {
		return
	}
	if fn.CodeSigningConfigArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "No code signing config associated with function: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	writeCodeSigningConfig(w, r, fn.CodeSigningConfigArn, name)
}

// PutFunctionCodeSigningConfig handles PUT /2020-06-30/functions/{name}/code-signing-config.
func (h *Handler) PutFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	fn, name := h.functionForCodeSigning(w, r)
	if fn == nil {
		return
	}

	var req putFunctionCodeSigningConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if !codeSigningConfigARNPattern.MatchString(req.CodeSigningConfigArn) {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "Invalid code signing config arn: " + req.CodeSigningConfigArn,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	exists, aerr := h.codeSigningConfigExists(r, req.CodeSigningConfigArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !exists {
		protocol.WriteJSONError(w, r, errCodeSigningConfigNotFoundForFunction(req.CodeSigningConfigArn))
		return
	}

	fn.CodeSigningConfigArn = req.CodeSigningConfigArn
	if aerr := h.ls.putFunction(r.Context(), fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeCodeSigningConfig(w, r, fn.CodeSigningConfigArn, name)
}

// DeleteFunctionCodeSigningConfig handles DELETE /2020-06-30/functions/{name}/code-signing-config.
// Returns 204 with an empty body, and is idempotent: removing an association
// that is not there is not an error.
func (h *Handler) DeleteFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	fn, _ := h.functionForCodeSigning(w, r)
	if fn == nil {
		return
	}
	if fn.CodeSigningConfigArn != "" {
		fn.CodeSigningConfigArn = ""
		if aerr := h.ls.putFunction(r.Context(), fn); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetFunction handles GET /2015-03-31/functions/{name}.
// Returns FunctionConfiguration + Code location block.
func (h *Handler) GetFunction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("get function", zap.String("function", name))
	fn, aerr := h.ls.getFunction(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	codeBlock := &getFunctionCode{
		Location:       "https://awslambda-overcast-placeholder.s3.amazonaws.com/" + fn.Name + ".zip",
		RepositoryType: "S3",
	}
	if fn.PackageType == "Image" {
		codeBlock.RepositoryType = "ECR"
		codeBlock.Location = fn.ImageUri
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(getFunctionResponse{
		Configuration: *functionToConfig(fn),
		Code:          codeBlock,
		Tags:          fn.Tags,
	})
}

// GetFunctionConfiguration handles GET /2015-03-31/functions/{name}/configuration.
// Returns FunctionConfiguration only (no Code block), matching AWS behaviour.
func (h *Handler) GetFunctionConfiguration(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("get function configuration", zap.String("function", name))
	fn, aerr := h.ls.getFunction(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(functionToConfig(fn))
}

// UpdateFunctionCode handles PUT /2015-03-31/functions/{name}/code.
func (h *Handler) UpdateFunctionCode(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("update function code", zap.String("function", name))
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	previousIdentity := functionInstanceIdentity(fn)

	var req updateFunctionCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if h.cfg.LambdaHotReload {
		if _, err := validateFunctionHotReloadConfig(fn); err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(err.Error()))
			return
		}
	}

	fn.setCode(req.ZipFile)
	if req.S3Bucket != "" {
		fn.CodeS3Bucket = req.S3Bucket
		fn.CodeS3Key = req.S3Key
	}
	if req.ImageUri != "" {
		fn.ImageUri = req.ImageUri
	}
	if len(req.Architectures) > 0 {
		if aerr := validateArchitectures(req.Architectures); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		fn.Architectures = req.Architectures
	}

	// Eagerly fetch from S3 when the caller passed only S3Bucket/S3Key (no
	// inline ZipFile). See CreateFunction for the same rationale.
	if len(fn.CodeZip) == 0 && fn.CodeS3Bucket != "" && fn.CodeS3Key != "" && h.s3Fetch != nil {
		if zip, err := h.s3Fetch(ctx, fn.CodeS3Bucket, fn.CodeS3Key); err == nil {
			fn.setCode(zip)
		} else {
			h.log.Warn("lambda: update function code: s3 fetch failed",
				zap.String("function", fn.Name),
				zap.String("bucket", fn.CodeS3Bucket),
				zap.String("key", fn.CodeS3Key),
				zap.Error(err),
			)
		}
	}
	fn.RevisionId = uuid.NewString()
	fn.LastModified = h.clk.Now().UTC().Format(time.RFC3339)

	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.retireExecutionEnvironment(fn, previousIdentity)

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.LambdaFunctionUpdated,
			Time:    h.clk.Now(),
			Source:  "lambda",
			Payload: events.LambdaFunctionPayload{Name: fn.Name, ARN: fn.ARN},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(functionToConfig(fn))
}

// UpdateFunctionConfiguration handles PUT /2015-03-31/functions/{name}/configuration.
func (h *Handler) UpdateFunctionConfiguration(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("update function configuration", zap.String("function", name))
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req updateFunctionConfigurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}

	if aerr := unsupportedFunctionConfigurationField(
		struct {
			name string
			raw  json.RawMessage
		}{"DeadLetterConfig", req.DeadLetterConfig},
		struct {
			name string
			raw  json.RawMessage
		}{"TracingConfig", req.TracingConfig},
		struct {
			name string
			raw  json.RawMessage
		}{"EphemeralStorage", req.EphemeralStorage},
		struct {
			name string
			raw  json.RawMessage
		}{"KMSKeyArn", req.KMSKeyArn},
		struct {
			name string
			raw  json.RawMessage
		}{"SnapStart", req.SnapStart},
		struct {
			name string
			raw  json.RawMessage
		}{"RuntimeManagementConfig", req.RuntimeManagementConfig},
		struct {
			name string
			raw  json.RawMessage
		}{"RecursiveLoop", req.RecursiveLoop},
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if req.Runtime != nil && deprecatedRuntimes[*req.Runtime] {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"The runtime "+*req.Runtime+" is no longer supported.",
		))
		return
	}
	if req.Timeout != nil && (*req.Timeout < 1 || *req.Timeout > 900) {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("Timeout must be between 1 and 900."))
		return
	}
	if req.MemorySize != nil && (*req.MemorySize < 128 || *req.MemorySize > 32768) {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("MemorySize must be between 128 and 32768."))
		return
	}
	if req.Handler != nil && *req.Handler == "" {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("Handler must not be empty."))
		return
	}
	if req.Role != nil && *req.Role == "" {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("Role must not be empty."))
		return
	}
	previousIdentity := functionInstanceIdentity(fn)

	// Pointer members distinguish omission (no change) from explicit zero values.
	if req.Description != nil {
		fn.Description = *req.Description
	}
	if req.Handler != nil {
		fn.Handler = *req.Handler
	}
	if req.Role != nil {
		fn.Role = *req.Role
	}
	if req.Timeout != nil {
		fn.Timeout = *req.Timeout
	}
	if req.MemorySize != nil {
		fn.MemorySize = *req.MemorySize
	}
	if req.Runtime != nil {
		fn.Runtime = *req.Runtime
	}
	if req.Environment != nil {
		fn.Environment = req.Environment.Variables
	}
	if req.LoggingConfig != nil {
		fn.LogGroup = req.LoggingConfig.LogGroup
		fn.LogFormat = req.LoggingConfig.LogFormat
		fn.ApplicationLogLevel = req.LoggingConfig.ApplicationLogLevel
		fn.SystemLogLevel = req.LoggingConfig.SystemLogLevel
	}
	if req.Layers != nil {
		links := make([]LayerVersionLink, 0, len(req.Layers))
		for _, arn := range req.Layers {
			// Real AWS requires layers to be in the same region as the function.
			if layerRegion := serviceutil.ARNRegion(arn); layerRegion != "" {
				if fnRegion := middleware.RegionFromContext(ctx, h.cfg.Region); layerRegion != fnRegion {
					protocol.WriteJSONError(w, r, &protocol.AWSError{
						Code:       "InvalidParameterValueException",
						Message:    "Layer ARN must be in the same region as the function.",
						HTTPStatus: http.StatusBadRequest,
					})
					return
				}
			}
			lv, aerr := h.ls.getLayerVersionByARN(ctx, arn)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			if lv != nil {
				links = append(links, LayerVersionLink{ARN: arn, CodeSize: lv.CodeSize})
			} else {
				links = append(links, LayerVersionLink{ARN: arn})
			}
		}
		fn.Layers = links
	}
	if req.VpcConfig != nil {
		if len(req.VpcConfig.SubnetIds) == 0 && len(req.VpcConfig.SecurityGroupIds) == 0 && !req.VpcConfig.Ipv6AllowedForDualStack {
			fn.VpcConfig = nil
		} else {
			fn.VpcConfig = &VpcConfig{
				SubnetIds:               req.VpcConfig.SubnetIds,
				SecurityGroupIds:        req.VpcConfig.SecurityGroupIds,
				Ipv6AllowedForDualStack: req.VpcConfig.Ipv6AllowedForDualStack,
			}
			if resolver := h.getVPCResolver(); resolver != nil && len(req.VpcConfig.SubnetIds) > 0 {
				fn.VpcConfig.VpcId = resolver.VpcIDForSubnet(ctx, req.VpcConfig.SubnetIds[0])
			}
		}
	}
	if req.FileSystemConfigs != nil {
		// An empty list clears the mounts; a populated one replaces them.
		if len(req.FileSystemConfigs) == 0 {
			fn.FileSystemConfigs = nil
		} else {
			if aerr := validateFileSystemConfigs(req.FileSystemConfigs); aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			fn.FileSystemConfigs = req.FileSystemConfigs
		}
	}
	if req.ImageConfig != nil {
		// An empty ImageConfig object clears all overrides.
		if len(req.ImageConfig.EntryPoint) == 0 && len(req.ImageConfig.Command) == 0 && req.ImageConfig.WorkingDirectory == "" {
			fn.ImageConfig = nil
		} else {
			fn.ImageConfig = &ImageConfig{
				EntryPoint:       req.ImageConfig.EntryPoint,
				Command:          req.ImageConfig.Command,
				WorkingDirectory: req.ImageConfig.WorkingDirectory,
			}
		}
	}
	fn.RevisionId = uuid.NewString()
	fn.LastModified = h.clk.Now().UTC().Format(time.RFC3339)

	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.retireExecutionEnvironment(fn, previousIdentity)

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.LambdaFunctionUpdated,
			Time:    h.clk.Now(),
			Source:  "lambda",
			Payload: events.LambdaFunctionPayload{Name: fn.Name, ARN: fn.ARN},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(functionToConfig(fn))
}

// retireExecutionEnvironment retires the warm execution environment for fn
// after its code or configuration changed, so the next invocation cold starts
// with the new values instead of reusing a container that was exec'd with the
// old ones.
//
// An idle instance is destroyed immediately; one that is serving an invocation
// is left to finish and destroyed on release, matching AWS, which never
// interrupts an in-flight invocation to apply an update. No on-demand
// replacement is started immediately — a function with provisioned concurrency
// is re-provisioned by the pool, and when proactive initialization is enabled
// a fresh environment is created once the configuration settles (see
// proactive.go), matching AWS's own behaviors.
//
// previousIdentity is fn's identity before the update was applied; when the
// update changes nothing the container can observe (e.g. only the description),
// the warm instance is left in place.
func (h *Handler) retireExecutionEnvironment(fn *Function, previousIdentity string) {
	if functionInstanceIdentity(fn) == previousIdentity {
		return
	}
	if pool := h.pool(); pool != nil {
		if retired := pool.InvalidateFunction(fn); retired > 0 {
			h.log.Debug("retired idle lambda instances after update",
				zap.String("function", fn.Name), zap.Int("count", retired))
		}
	}
	h.tracker.Invalidate(fn.Name)
	h.proactive.NoteFunctionChanged(fn)
}

// DeleteFunction handles DELETE /2015-03-31/functions/{name}.
func (h *Handler) DeleteFunction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("delete function", zap.String("function", name))
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if aerr := h.ls.deleteFunction(ctx, name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Evict the warm instances so the containers stop immediately.
	if pool := h.pool(); pool != nil {
		pool.EvictFunction(name)
	}
	h.tracker.Evict(name)
	h.proactive.NoteFunctionDeleted(name)

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.LambdaFunctionDeleted,
			Time:    h.clk.Now(),
			Source:  "lambda",
			Payload: events.LambdaFunctionPayload{Name: fn.Name, ARN: fn.ARN},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// ─── Invoke ───────────────────────────────────────────────────────────────────

// InvokeFunction handles POST /2015-03-31/functions/{name}/invocations.
// https://docs.aws.amazon.com/lambda/latest/api/API_Invoke.html
func (h *Handler) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if aerr := checkInvokableState(fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if badLayer := h.checkLayerVersionsExist(ctx, fn); badLayer != "" {
		writeInvokeError(w, badLayer, "Runtime.InitError")
		return
	}

	invocationType := r.Header.Get("X-Amz-Invocation-Type")
	if invocationType == "" {
		invocationType = "RequestResponse"
	}
	h.log.Debug("invoke function", zap.String("function", name),
		zap.String("invocation_type", invocationType))

	// DryRun: validate function exists and return 204 — no execution.
	if invocationType == "DryRun" {
		w.Header().Set("X-Amz-Executed-Version", "$LATEST")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Read the event payload (may be empty for no-payload invocations).
	payload, err := io.ReadAll(io.LimitReader(r.Body, 6*1024*1024)) // 6 MB sync limit
	if err != nil {
		h.log.Error("invoke: read payload", zap.String("function", name), zap.Error(err))
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidRequestContentException", Message: "Could not read request body.", HTTPStatus: http.StatusBadRequest})
		return
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	// Record invocation.
	if err := h.ls.addInvocation(ctx, fn, payload); err != nil {
		h.log.Warn("invoke: record invocation", zap.String("function", name), zap.Error(err))
	}

	// Find a runtime that can handle this function.
	var rt Runtime
	for _, r := range h.runtimes.get() {
		if r.CanHandle(fn.Runtime) {
			rt = r
			break
		}
	}
	if rt == nil {
		h.log.Warn("invoke: no runtime available", zap.String("function", name), zap.String("runtime", fn.Runtime))
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidRuntimeException",
			Message:    "No runtime available for " + fn.Runtime + ". Only Node.js runtimes with inline source code can be invoked in the emulator.",
			HTTPStatus: http.StatusBadRequest})
		return
	}

	// Event (async fire-and-forget): accept immediately with 202, execute in background.
	if invocationType == "Event" {
		w.Header().Set("X-Amz-Executed-Version", "$LATEST")
		w.WriteHeader(http.StatusAccepted)
		// The refusal startAsync can report is unreachable here — the HTTP
		// server stops before StopAsync runs — and the 202 is already written
		// either way, so there is nobody left to tell. startAsync logs it.
		_ = h.startAsync(fn, rt, payload)
		return
	}

	// RequestResponse (default): synchronous execution — acquire, invoke, release.
	// result.LogResult is filled from the tail buffer the streamLogs goroutine
	// maintains, but only when the caller explicitly requested a tail
	// (X-Amz-Log-Type: Tail), matching real AWS. The request travels into the
	// invoke rather than being filtered out of the result afterwards, because
	// producing a complete tail costs latency the other callers must not pay.
	tail := strings.EqualFold(r.Header.Get("X-Amz-Log-Type"), "Tail")

	result := h.invokeSync(ctx, fn, rt, payload, name, InvokeOptions{LogTail: tail})
	if result.throttle != nil {
		writeThrottleError(w, r, result.throttle)
		return
	}

	// Write AWS-style invoke response.
	w.Header().Set("Content-Type", "application/json")
	if result.FunctionError != "" {
		w.Header().Set("X-Amz-Function-Error", result.FunctionError)
	}
	if result.LogResult != "" {
		w.Header().Set("X-Amz-Log-Result", result.LogResult)
	}
	w.Header().Set("X-Amz-Executed-Version", "$LATEST")
	statusCode := result.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	if result.Payload != nil {
		_, _ = w.Write(result.Payload)
	}
}

// ─── Invoke helpers ───────────────────────────────────────────────────────────

// checkInvokableState returns a non-nil AWSError when fn is not in an invokable
// state. AWS Lambda rejects invocations for functions in Pending or Failed state
// with InvalidParameterValueException (HTTP 400).
func checkInvokableState(fn *Function) *protocol.AWSError {
	switch fn.State {
	case "Pending":
		// Real AWS answers state conflicts with 409 ResourceConflictException
		// (as the Inactive arm below already does); this arm used to return
		// InvalidParameterValueException, which mislabelled a retryable
		// state condition as a caller error.
		return &protocol.AWSError{
			Code:       "ResourceConflictException",
			Message:    "The operation cannot be performed at this time. The function is currently in the following state: Pending",
			HTTPStatus: http.StatusConflict,
		}
	case "Failed":
		return &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "The function is in a failed state.",
			HTTPStatus: http.StatusBadRequest,
		}
	case "Inactive":
		return &protocol.AWSError{
			Code:       "ResourceConflictException",
			Message:    "The operation cannot be performed at this time. The function is currently in an inactive state.",
			HTTPStatus: http.StatusConflict,
		}
	}
	return nil
}

// invokableStateMessage returns a concise, human-readable explanation of why
// a function in the given state cannot be invoked. Used in event bus payloads
// and log messages in preference to the raw AWS error message string.
func invokableStateMessage(state string) string {
	switch state {
	case "Pending":
		return "function is still initializing (container image pull in progress)"
	case "Failed":
		return "function creation failed; check the function's StateReason for details"
	case "Inactive":
		return "function is inactive and will be restored on the next direct invocation"
	default:
		return "function is not in an invokable state (" + state + ")"
	}
}

// checkLayerVersionsExist validates that every layer ARN attached to fn
// corresponds to a stored layer version. Returns the ARN of the first missing
// layer, or "" when all layers exist.
func (h *Handler) checkLayerVersionsExist(ctx context.Context, fn *Function) string {
	for _, layer := range fn.Layers {
		arn := strings.TrimSpace(layer.ARN)
		if arn == "" {
			continue
		}
		lv, aerr := h.getLayerVersionByARNOrCachedExternal(ctx, arn)
		if aerr != nil {
			h.log.Error("invoke: check layer version", zap.String("arn", arn), zap.Error(aerr))
			return arn
		}
		if lv == nil {
			return arn
		}
	}
	return ""
}

func (h *Handler) getLayerVersionByARNOrCachedExternal(ctx context.Context, arn string) (*LayerVersion, *protocol.AWSError) {
	lv, aerr := h.ls.getLayerVersionByARN(ctx, arn)
	if aerr != nil || lv != nil || !h.isForeignAccountLayerARN(arn) {
		return lv, aerr
	}
	return h.cachedExternalLayerVersion(ctx, arn), nil
}

func (h *Handler) isForeignAccountLayerARN(arn string) bool {
	parsed, err := parseLayerARN(arn)
	if err != nil {
		return false
	}
	return parsed.Account != "" && parsed.Account != h.accountID()
}

func (h *Handler) cachedExternalLayerVersion(ctx context.Context, arn string) *LayerVersion {
	parsed, err := parseLayerARN(arn)
	if err != nil {
		return nil
	}
	logger := zap.NewNop()
	if h.log != nil {
		logger = h.log.ZapLogger()
	}
	fetcher := NewRemoteLayerFetcher(h.cfg, logger, h.clk)
	codeSize, err := fetcher.ResolveLayerSize(ctx, arn)
	if err != nil {
		logger.Debug("foreign-account layer not available from cache or remote fetch", zap.String("arn", arn), zap.Error(err))
		return nil
	}
	version, err := strconv.ParseInt(parsed.Version, 10, 64)
	if err != nil {
		return nil
	}
	return &LayerVersion{
		LayerName:       parsed.LayerName,
		LayerARN:        strings.TrimSuffix(arn, ":"+parsed.Version),
		LayerVersionARN: arn,
		Version:         version,
		CodeSize:        codeSize,
	}
}

// writeInvokeError writes an invoke-style error response to w.
func writeInvokeError(w http.ResponseWriter, layerARN, errorType string) {
	msg := fmt.Sprintf("Failed to load Lambda layer %s: layer version not found", layerARN)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Amz-Function-Error", "Unhandled")
	w.Header().Set("X-Amz-Executed-Version", "$LATEST")
	w.WriteHeader(http.StatusOK)
	payload := fmt.Sprintf(`{"errorMessage":%q,"errorType":%q}`, msg, errorType)
	_, _ = w.Write([]byte(payload))
}

// invokeSync acquires a runtime instance, invokes the function, and returns the
// result, writing an error response and returning nil on failure.
func (h *Handler) invokeSync(ctx context.Context, fn *Function, rt Runtime, payload []byte, name string, opts InvokeOptions) *InvokeResult {
	inv := h.tracker.Begin(name, payload)
	releaseSuccess := false
	releaseReason := ""
	throttled := false
	defer func() {
		if throttled {
			// The invocation never got an execution environment, so there is no
			// instance to report as idle — drop the record instead.
			inv.Abandon(releaseReason)
			return
		}
		inv.Finish(releaseSuccess, releaseReason)
	}()

	// Cold starts can fail transiently due to Docker infrastructure issues
	// (image pull failure, container create/start failure, IP resolution
	// timeout). These are emulator-specific problems that wouldn't occur in
	// real AWS. Retry once on Acquire failure only — never retry errors from
	// inside a running container (timeouts, init errors, handler crashes).
	maxAttempts := 3
	if fn.Timeout > 0 && fn.Timeout <= 2 {
		// For very short function timeouts, a long retry loop can violate the
		// caller's wall-clock expectations even when the function timeout itself
		// is enforced correctly inside the runtime.
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result := h.invokeSyncOnce(ctx, fn, rt, payload, name, inv, opts)
		if result.throttle != nil {
			// A concurrency limit refused the invocation. Never retry — the
			// caller decides whether to back off, exactly as against AWS.
			throttled = true
			releaseReason = result.throttle.Error()
			return result
		}
		if result.FunctionError == "" {
			releaseSuccess = true
			releaseReason = ""
			return result
		}
		releaseSuccess = false
		releaseReason = invocationFailureReasonFromResult(result)
		if attempt == maxAttempts {
			return result
		}
		// Only retry when Docker failed to provision the container.
		if !result.acquireFailed {
			return result
		}
		h.log.Warn("invoke: Docker acquire failed, retrying",
			zap.String("function", name),
			zap.Int("attempt", attempt),
		)
	}
	// unreachable, but compiler needs it
	return &InvokeResult{StatusCode: 200, FunctionError: "Unhandled"}
}

// invocationFailureReasonFromResult extracts a high-signal failure reason for
// invocation outcome events. It prefers payload.errorMessage when present and
// falls back to FunctionError.
func invocationFailureReasonFromResult(result *InvokeResult) string {
	if result == nil {
		return "unknown invocation failure"
	}
	if result.FunctionError == "" {
		return ""
	}
	if len(result.Payload) == 0 {
		return result.FunctionError
	}
	var payload struct {
		ErrorMessage string `json:"errorMessage"`
	}
	if err := json.Unmarshal(result.Payload, &payload); err == nil {
		if msg := strings.TrimSpace(payload.ErrorMessage); msg != "" {
			return msg
		}
	}
	return result.FunctionError
}

type runtimeReadyInstance interface {
	AwaitReady(context.Context) error
}

// functionTimeout is fn's configured timeout, defaulting to AWS's 3 seconds.
// Bounds every invocation so an overrunning container is killed, and is also
// the budget an invocation may spend queued for capacity (see queueBudget).
func functionTimeout(fn *Function) time.Duration {
	if fn == nil || fn.Timeout <= 0 {
		return defaultFunctionTimeout
	}
	return time.Duration(fn.Timeout) * time.Second
}

func lambdaInitTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.LambdaInitTimeout > 0 {
		return cfg.LambdaInitTimeout
	}
	return 10 * time.Second
}

// invokeFailurePayload shapes the response body for an invocation that never
// produced a result. A function that ran past its timeout gets the payload
// real Lambda returns; anything else keeps the emulator's Runtime.ExitError
// shape so the underlying cause stays visible to whoever is debugging.
func invokeFailurePayload(err error) []byte {
	var timeout *invokeTimeoutError
	if errors.As(err, &timeout) {
		return timeout.Payload()
	}
	return []byte(fmt.Sprintf(`{"errorMessage":%q,"errorType":"Runtime.ExitError"}`, err.Error()))
}

func awaitRuntimeReady(ctx context.Context, cfg *config.Config, inst RuntimeInstance) error {
	ready, ok := inst.(runtimeReadyInstance)
	if !ok {
		return nil
	}
	initCtx, cancel := context.WithTimeout(ctx, lambdaInitTimeout(cfg))
	defer cancel()
	if err := ready.AwaitReady(initCtx); err != nil {
		return fmt.Errorf("lambda runtime did not initialize within %s: %w", lambdaInitTimeout(cfg), err)
	}
	return nil
}

func (h *Handler) awaitRuntimeReady(ctx context.Context, fn *Function, rt Runtime, inst RuntimeInstance) error {
	return awaitRuntimeReady(ctx, h.cfg, inst)
}

// invokeSyncOnce performs a single acquire → invoke → release cycle.
func (h *Handler) invokeSyncOnce(ctx context.Context, fn *Function, rt Runtime, payload []byte, name string, inv *trackedInvocation, opts InvokeOptions) *InvokeResult {
	acquireTimeout := 30 * time.Second
	// For very short function timeouts, keep acquire tighter.
	if fn.Timeout > 0 && fn.Timeout <= 5 {
		acquireTimeout = 20 * time.Second
	}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, acquireTimeout)
	defer acquireCancel()

	inst, err := rt.Acquire(acquireCtx, fn)
	if err != nil {
		if t, ok := asThrottle(err); ok {
			return &InvokeResult{StatusCode: 429, throttle: t}
		}
		h.log.Error("invoke: acquire instance", zap.String("function", name), zap.Error(err))
		return &InvokeResult{
			StatusCode:    200,
			Payload:       []byte(fmt.Sprintf(`{"errorMessage":%q,"errorType":"Runtime.InitError"}`, err.Error())),
			FunctionError: "Unhandled",
			acquireFailed: true,
		}
	}
	inv.Bind(inst)
	inv.Ready()
	if err := h.awaitRuntimeReady(ctx, fn, rt, inst); err != nil {
		rt.Release(ctx, inst, false)
		h.log.Error("invoke: runtime init", zap.String("function", name), zap.Error(err))
		return &InvokeResult{
			StatusCode:    200,
			Payload:       []byte(fmt.Sprintf(`{"errorMessage":%q,"errorType":"Runtime.InitError"}`, err.Error())),
			FunctionError: "Unhandled",
			acquireFailed: true,
		}
	}

	inv.Running()

	// Capture the log stream name before invoking so it can be attached to the result.
	logStreamName := inst.LogStreamName()
	inv.SetLogRefs(fn.logGroupName(), logStreamName)

	// Ensure the log stream exists (idempotent — cheap on warm reuse).
	// Use the function's own region (from its ARN) so the log stream is
	// created in the correct regional log group, regardless of where the
	// invoke request originated.
	if h.logWriter != nil {
		fnRegion := regionFromFunctionARN(fn.ARN)
		if fnRegion == "" {
			fnRegion = h.cfg.Region
		}
		fnCtx := middleware.ContextWithRegion(ctx, fnRegion)
		if lsErr := h.logWriter.EnsureLogStream(fnCtx, fn.logGroupName(), logStreamName); lsErr != nil {
			h.log.Debug("invoke: ensure log stream", zap.String("function", name), zap.Error(lsErr))
		}
	}

	// Bound the invocation by the function's configured timeout so that
	// context.getRemainingTimeInMillis() inside the function reflects the
	// real deadline, and so we kill the container if it overruns.
	invokeCtx, cancel := context.WithTimeout(ctx, functionTimeout(fn))
	defer cancel()

	h.log.Debug("invoke function: dispatching", zap.String("function", name), zap.Int("payload_bytes", len(payload)))
	result, invokeErr := inst.Invoke(invokeCtx, payload, opts)
	healthy := invokeErr == nil
	rt.Release(invokeCtx, inst, healthy)

	if invokeErr != nil {
		h.log.Error("invoke: execution error", zap.String("function", name), zap.Error(invokeErr))
		return &InvokeResult{
			StatusCode:    200,
			Payload:       invokeFailurePayload(invokeErr),
			FunctionError: "Unhandled",
			LogGroupName:  fn.logGroupName(),
			LogStreamName: logStreamName,
		}
	}
	result.LogGroupName = fn.logGroupName()
	result.LogStreamName = logStreamName
	return result
}

// asyncThrottleRetries and asyncThrottleBackoff shape how an Event-type
// invocation waits out a throttle. AWS never returns 429 to an asynchronous
// caller — it already answered 202 and queued the event — so it retries
// internally instead. Overcast does the same on a much shorter budget: an
// emulator holding events for AWS's six hours would just look broken.
const (
	asyncThrottleRetries = 5
	asyncThrottleBackoff = 2 * time.Second
)

// acquireForAsync acquires an instance for an Event-type invocation, retrying
// while the function is throttled rather than dropping the event on the first
// refusal.
func (h *Handler) acquireForAsync(ctx context.Context, fn *Function, rt Runtime) (RuntimeInstance, error) {
	var err error
	for attempt := 0; ; attempt++ {
		var inst RuntimeInstance
		inst, err = rt.Acquire(ctx, fn)
		if err == nil {
			return inst, nil
		}
		if _, throttled := asThrottle(err); !throttled || attempt >= asyncThrottleRetries {
			return nil, err
		}
		h.log.Debug("invokeAsync: throttled, retrying",
			zap.String("function", fn.Name),
			zap.Int("attempt", attempt+1),
		)
		select {
		case <-h.clk.After(asyncThrottleBackoff):
		case <-ctx.Done():
			return nil, err
		}
	}
}

// startAsync hands an accepted event to Lambda's async machinery and returns
// immediately. This is the only way an Event-type invocation should be started:
// it is what makes "accepted" mean the same thing to an HTTP caller holding a
// 202 and to an in-process caller of ServiceInvoker.InvokeEvent — the throttle
// retry, the instance tracking and the shutdown drain all come with it.
//
// The goroutine is bounded by asyncWg, which StopAsync waits on, so nothing
// started here outlives the server. It reports false once StopAsync has run,
// because after that there is no drain left to join; callers that can tell
// someone (InvokeEvent) report it rather than dropping the event silently.
func (h *Handler) startAsync(fn *Function, rt Runtime, payload []byte) bool {
	h.asyncMu.Lock()
	if h.asyncClosed {
		h.asyncMu.Unlock()
		h.log.Warn("invokeAsync: refusing an event, Lambda is shutting down",
			zap.String("function", fn.Name))
		return false
	}
	h.asyncWg.Add(1)
	h.asyncMu.Unlock()

	go func() {
		defer h.asyncWg.Done()
		h.invokeAsync(fn, rt, payload)
	}()
	return true
}

// invokeAsync fires a function invocation in a goroutine, discarding the result.
// Used for Event-type invocations (HTTP 202, fire-and-forget).
func (h *Handler) invokeAsync(fn *Function, rt Runtime, payload []byte) {
	// Use a background context — the HTTP request has already returned (202).
	// Still bound by the function timeout so the container is killed on overrun.
	// Inject the function's region so EnsureLogStream writes to the correct
	// regional log group (background contexts have no region otherwise).
	region := regionFromFunctionARN(fn.ARN)
	if region == "" {
		region = h.cfg.Region
	}
	bgCtx := middleware.ContextWithRegion(context.Background(), region)
	ctx := bgCtx

	inv := h.tracker.Begin(fn.Name, payload)
	inst, err := h.acquireForAsync(ctx, fn, rt)
	if err != nil {
		inv.Abandon(err.Error())
		h.log.Error("invokeAsync: acquire instance", zap.String("function", fn.Name), zap.Error(err))
		return
	}
	inv.Bind(inst)
	inv.Ready()
	if err := h.awaitRuntimeReady(ctx, fn, rt, inst); err != nil {
		rt.Release(ctx, inst, false)
		inv.Abandon(err.Error())
		h.log.Error("invokeAsync: runtime init", zap.String("function", fn.Name), zap.Error(err))
		return
	}
	inv.Running()

	// Ensure the log stream exists so container logs are captured.
	if h.logWriter != nil {
		inv.SetLogRefs(fn.logGroupName(), inst.LogStreamName())
		if lsErr := h.logWriter.EnsureLogStream(ctx, fn.logGroupName(), inst.LogStreamName()); lsErr != nil {
			h.log.Debug("invokeAsync: ensure log stream", zap.String("function", fn.Name), zap.Error(lsErr))
		}
	}

	invokeCtx, cancel := context.WithTimeout(ctx, functionTimeout(fn))
	defer cancel()
	// No tail: an Event invocation answered 202 long ago and has no caller left
	// to hand a LogResult to.
	result, invokeErr := inst.Invoke(invokeCtx, payload, InvokeOptions{})
	rt.Release(invokeCtx, inst, invokeErr == nil)
	inv.Finish(invocationOutcome(invokeErr, result))
	if invokeErr != nil {
		h.log.Error("invokeAsync: execution error", zap.String("function", fn.Name), zap.Error(invokeErr))
		return
	}
	if result != nil && result.FunctionError != "" {
		h.log.Debug("invokeAsync: function error", zap.String("function", fn.Name), zap.String("function_error", result.FunctionError))
	}
}

// ─── SSE Invoke (emulator-only) ───────────────────────────────────────────────

// sseKeepaliveInterval is how often the invoke progress stream emits an SSE
// comment frame while the function is running. Short enough to stay under the
// idle timeouts proxies commonly apply (nginx defaults to 60 s).
const sseKeepaliveInterval = 15 * time.Second

// InvokeFunctionSSE handles POST /2015-03-31/functions/{name}/invoke-with-progress.
// Emulator-only endpoint that streams lifecycle progress events as SSE, then
// sends the final invoke result. Used by the web UI Test tab.
func (h *Handler) InvokeFunctionSSE(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	// Read the request body BEFORE setting SSE headers — flushing the
	// response writer can close the request body in some middleware stacks.
	var reqBody struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 6*1024*1024)).Decode(&reqBody); err != nil {
		http.Error(w, "Could not read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	payload := []byte(reqBody.Payload)
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	// SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Guards w against the keepalive goroutine that runs while the handler is
	// executing; every write to the stream goes through one of these two.
	var writeMu sync.Mutex
	sendEvent := func(event, data string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}
	sendKeepalive := func() {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = io.WriteString(w, ": keepalive\n\n")
		flusher.Flush()
	}

	sendEvent("progress", "Looking up function")

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		sendEvent("error", aerr.Message)
		return
	}
	if fn == nil {
		sendEvent("error", "Function not found: "+name)
		return
	}

	// Record invocation.
	if err := h.ls.addInvocation(ctx, fn, payload); err != nil {
		h.log.Warn("invoke-sse: record invocation", zap.String("function", name), zap.Error(err))
	}

	// Find runtime.
	sendEvent("progress", "Selecting runtime for "+fn.Runtime)
	var rt Runtime
	for _, candidate := range h.runtimes.get() {
		if candidate.CanHandle(fn.Runtime) {
			rt = candidate
			break
		}
	}
	if rt == nil {
		sendEvent("error", "No runtime available for "+fn.Runtime)
		return
	}

	// Acquire instance — use progress-aware path if the runtime supports it.
	sendEvent("progress", "Acquiring runtime instance")

	inv := h.tracker.Begin(name, payload)

	var inst RuntimeInstance
	var err error
	if pool, ok := rt.(*InstancePool); ok {
		inst, err = pool.AcquireWithProgress(ctx, fn, func(step string) {
			sendEvent("progress", step)
		})
	} else if cr, ok := rt.(*ContainerRuntime); ok {
		inst, err = cr.AcquireWithProgress(ctx, fn, func(step string) {
			sendEvent("progress", step)
		})
	} else {
		inst, err = rt.Acquire(ctx, fn)
	}
	if err != nil {
		inv.Abandon(err.Error())
		h.log.Error("invoke-sse: acquire instance", zap.String("function", name), zap.Error(err))
		sendEvent("error", err.Error())
		return
	}
	inv.Bind(inst)
	inv.Ready()
	if err := h.awaitRuntimeReady(ctx, fn, rt, inst); err != nil {
		rt.Release(ctx, inst, false)
		inv.Abandon(err.Error())
		h.log.Error("invoke-sse: runtime init", zap.String("function", name), zap.Error(err))
		sendEvent("error", err.Error())
		return
	}
	inv.Running()

	// Capture the log stream name before invoking so it can be attached to the result.
	logStreamName := inst.LogStreamName()
	inv.SetLogRefs(fn.logGroupName(), logStreamName)

	// Ensure log stream using the function's own region.
	if h.logWriter != nil {
		fnRegion := regionFromFunctionARN(fn.ARN)
		if fnRegion == "" {
			fnRegion = h.cfg.Region
		}
		fnCtx := middleware.ContextWithRegion(ctx, fnRegion)
		if lsErr := h.logWriter.EnsureLogStream(fnCtx, fn.logGroupName(), logStreamName); lsErr != nil {
			h.log.Debug("invoke-sse: ensure log stream", zap.String("function", name), zap.Error(lsErr))
		}
	}

	// Invoke with timeout.
	invokeCtx, cancel := context.WithTimeout(ctx, functionTimeout(fn))
	defer cancel()

	sendEvent("progress", "Invoking function handler")

	// A handler may legitimately run for its full configured timeout — up to
	// AWS's 15 minutes — with nothing to report, and an idle connection is what
	// proxies and browsers drop. SSE comment frames keep it warm without adding
	// events the console has to understand.
	stopKeepalive, keepaliveStopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(keepaliveStopped)
		ticker := h.clk.Ticker(sseKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopKeepalive:
				return
			case <-ticker.C:
				sendKeepalive()
			}
		}
	}()

	// The console's Test tab always renders the log tail, so this path always
	// asks for one — there is no X-Amz-Log-Type to consult.
	result, invokeErr := inst.Invoke(invokeCtx, payload, InvokeOptions{LogTail: true})
	// Join before touching w again: writing to a ResponseWriter after the
	// handler returns is not allowed, so the goroutine must be gone first.
	close(stopKeepalive)
	<-keepaliveStopped
	healthy := invokeErr == nil
	rt.Release(invokeCtx, inst, healthy)
	inv.Finish(invocationOutcome(invokeErr, result))

	if invokeErr != nil {
		h.log.Error("invoke-sse: execution error", zap.String("function", name), zap.Error(invokeErr))
		errResult := InvokeResult{
			StatusCode:    200,
			Payload:       invokeFailurePayload(invokeErr),
			FunctionError: "Unhandled",
			LogGroupName:  fn.logGroupName(),
			LogStreamName: logStreamName,
		}
		data, _ := json.Marshal(invokeResultToJSON(&errResult))
		sendEvent("result", string(data))
		return
	}
	result.LogGroupName = fn.logGroupName()
	result.LogStreamName = logStreamName

	data, _ := json.Marshal(invokeResultToJSON(result))
	sendEvent("result", string(data))
}

// invokeResultToJSON converts an InvokeResult to a JSON-friendly map matching
// the shape the web UI expects.
func invokeResultToJSON(r *InvokeResult) map[string]interface{} {
	var payloadStr interface{}
	if r.Payload != nil {
		payloadStr = string(r.Payload)
	}
	return map[string]interface{}{
		"statusCode":      r.StatusCode,
		"payload":         payloadStr,
		"functionError":   r.FunctionError,
		"logResult":       r.LogResult,
		"executedVersion": "$LATEST",
		"logGroupName":    r.LogGroupName,
		"logStreamName":   r.LogStreamName,
	}
}

// ─── Saved test events (emulator-only) ────────────────────────────────────────

// ListTestEvents handles GET /2015-03-31/functions/{name}/test-events.
// Emulator-only endpoint for the web UI's Test tab.
func (h *Handler) ListTestEvents(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	events, err := h.ls.listTestEvents(ctx, name)
	if err != nil {
		h.log.Error("list test events", zap.String("function", name), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	type testEventResponse struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	out := make([]testEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, testEventResponse{Name: e.Name, Body: e.Body})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"events": out})
}

// PutTestEvent handles PUT /2015-03-31/functions/{name}/test-events/{eventName}.
// Emulator-only endpoint for creating or updating saved test events.
func (h *Handler) PutTestEvent(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	eventName := chi.URLParam(r, "eventName")
	ctx := r.Context()

	var req struct {
		Body string `json:"body"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	te := &TestEvent{
		Name:         eventName,
		FunctionName: name,
		Body:         req.Body,
	}
	if err := h.ls.putTestEvent(ctx, te); err != nil {
		h.log.Error("put test event", zap.String("function", name), zap.String("event", eventName), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"name": eventName, "body": req.Body})
}

// DeleteTestEvent handles DELETE /2015-03-31/functions/{name}/test-events/{eventName}.
// Emulator-only endpoint for removing saved test events.
func (h *Handler) DeleteTestEvent(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	eventName := chi.URLParam(r, "eventName")
	ctx := r.Context()

	if err := h.ls.deleteTestEvent(ctx, name, eventName); err != nil {
		h.log.Error("delete test event", zap.String("function", name), zap.String("event", eventName), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
