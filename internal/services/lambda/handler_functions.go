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

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	FunctionName    string             `json:"FunctionName"`
	FunctionArn     string             `json:"FunctionArn"`
	Runtime         string             `json:"Runtime,omitempty"`
	Handler         string             `json:"Handler,omitempty"`
	Role            string             `json:"Role,omitempty"`
	Description     string             `json:"Description,omitempty"`
	Timeout         int                `json:"Timeout,omitempty"`
	MemorySize      int                `json:"MemorySize,omitempty"`
	CodeSize        int64              `json:"CodeSize,omitempty"`
	LastModified    string             `json:"LastModified,omitempty"`
	RevisionId      string             `json:"RevisionId,omitempty"`
	PackageType     string             `json:"PackageType,omitempty"`
	Architectures   []string           `json:"Architectures,omitempty"`
	State           string             `json:"State,omitempty"`
	StateReason     string             `json:"StateReason,omitempty"`
	StateReasonCode string             `json:"StateReasonCode,omitempty"`
	CodeSha256      string             `json:"CodeSha256,omitempty"`
	Environment     *functionEnvConf   `json:"Environment,omitempty"`
	LoggingConfig   *loggingConfig     `json:"LoggingConfig,omitempty"`
	Layers          []LayerVersionLink `json:"Layers,omitempty"`
	ImageUri        string             `json:"ImageUri,omitempty"`
	ImageConfig     *imageConfigWire   `json:"ImageConfig,omitempty"`
	VpcConfig       *vpcConfigResponse `json:"VpcConfig,omitempty"`
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
	LogGroup  string `json:"LogGroup,omitempty"`
	LogFormat string `json:"LogFormat,omitempty"`
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
	// Layers is a list of layer version ARNs to attach to the function at creation.
	Layers []string `json:"Layers,omitempty"`
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
	ZipFile  []byte `json:"ZipFile,omitempty"`
	S3Bucket string `json:"S3Bucket,omitempty"`
	S3Key    string `json:"S3Key,omitempty"`
	ImageUri string `json:"ImageUri,omitempty"`
}

// updateFunctionConfigurationRequest matches AWS UpdateFunctionConfiguration body.
type updateFunctionConfigurationRequest struct {
	Description string        `json:"Description,omitempty"`
	Handler     string        `json:"Handler,omitempty"`
	Role        string        `json:"Role,omitempty"`
	Timeout     int           `json:"Timeout,omitempty"`
	MemorySize  int           `json:"MemorySize,omitempty"`
	Runtime     string        `json:"Runtime,omitempty"`
	Environment *envVariables `json:"Environment,omitempty"`
	// Layers is a list of layer version ARNs to attach. An empty slice clears all layers.
	// A nil field means "no change".
	Layers      []string          `json:"Layers,omitempty"`
	VpcConfig   *vpcConfigRequest `json:"VpcConfig,omitempty"`
	ImageConfig *imageConfigWire  `json:"ImageConfig,omitempty"`
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
	SubnetIds        []string `json:"SubnetIds,omitempty"`
	SecurityGroupIds []string `json:"SecurityGroupIds,omitempty"`
}

// vpcConfigResponse is the VpcConfig block returned in function configuration.
type vpcConfigResponse struct {
	SubnetIds        []string `json:"SubnetIds"`
	SecurityGroupIds []string `json:"SecurityGroupIds"`
	VpcId            string   `json:"VpcId"`
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
			LogGroup:  fn.logGroupName(),
			LogFormat: "Text",
		},
	}
	if len(fn.Environment) > 0 {
		cfg.Environment = &functionEnvConf{Variables: fn.Environment}
	}
	if len(fn.Layers) > 0 {
		cfg.Layers = fn.Layers
	}
	if fn.VpcConfig != nil {
		cfg.VpcConfig = &vpcConfigResponse{
			SubnetIds:        fn.VpcConfig.SubnetIds,
			SecurityGroupIds: fn.VpcConfig.SecurityGroupIds,
			VpcId:            fn.VpcConfig.VpcId,
		}
	}
	if fn.ImageConfig != nil {
		cfg.ImageConfig = &imageConfigWire{
			EntryPoint:       fn.ImageConfig.EntryPoint,
			Command:          fn.ImageConfig.Command,
			WorkingDirectory: fn.ImageConfig.WorkingDirectory,
		}
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
	if len(architectures) != 1 || (architectures[0] != "x86_64" && architectures[0] != "arm64") {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("1 validation error detected: Value '"+strings.Join(architectures, ",")+"' at 'architectures' failed to satisfy constraint: Member must satisfy enum value set: [x86_64, arm64]"))
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
	if req.LoggingConfig != nil && req.LoggingConfig.LogGroup != "" {
		fn.LogGroup = req.LoggingConfig.LogGroup
	}
	if req.Environment != nil {
		fn.Environment = req.Environment.Variables
	}
	if req.Code != nil {
		fn.CodeZip = req.Code.ZipFile
		fn.CodeSize = int64(len(req.Code.ZipFile))
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
				fn.CodeZip = zip
				fn.CodeSize = int64(len(zip))
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
			SubnetIds:        req.VpcConfig.SubnetIds,
			SecurityGroupIds: req.VpcConfig.SecurityGroupIds,
		}
		// Resolve VpcId from the first subnet, if a resolver is available.
		if h.vpcResolver != nil && len(req.VpcConfig.SubnetIds) > 0 {
			fn.VpcConfig.VpcId = h.vpcResolver.VpcIDForSubnet(ctx, req.VpcConfig.SubnetIds[0])
		}
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
			updated := *fn
			if pullErr != nil {
				updated.State = "Failed"
				updated.StateReason = "Failed to pull container image: " + pullErr.Error()
				updated.StateReasonCode = "ImagePullError"
				h.log.Warn("lambda: background image pull failed",
					zap.String("function", updated.Name), zap.Error(pullErr))
			} else {
				updated.State = "Active"
				updated.StateReason = ""
				updated.StateReasonCode = ""
			}
			// Retry putFunction since the store may be contended during CDK deploy
			// (many concurrent S3 PutObject calls). Using the captured function avoids a
			// re-fetch that would itself fail under the same contention.
			const maxAttempts = 5
			for i := range maxAttempts {
				if err := h.ls.putFunction(bgCtx, &updated); err == nil {
					return
				}
				if i < maxAttempts-1 {
					time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
				}
			}
			h.log.Warn("lambda: failed to persist state transition after image pull",
				zap.String("function", updated.Name))
		})
	} else {
		fn.State = "Active"
		fn.StateReason = ""
		fn.StateReasonCode = ""
		_ = h.ls.putFunction(ctx, fn)
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

// GetFunctionCodeSigningConfig handles GET /2015-03-31/functions/{name}/code-signing-config.
// Code signing is not enforced by the emulator; functions never have a config associated,
// so this always returns ResourceNotFoundException once the function is confirmed to exist.
func (h *Handler) GetFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
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
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "No code signing config associated with function: " + name,
		HTTPStatus: http.StatusNotFound,
	})
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

	fn.CodeZip = req.ZipFile
	fn.CodeSize = int64(len(req.ZipFile))
	if req.S3Bucket != "" {
		fn.CodeS3Bucket = req.S3Bucket
		fn.CodeS3Key = req.S3Key
	}
	if req.ImageUri != "" {
		fn.ImageUri = req.ImageUri
	}

	// Eagerly fetch from S3 when the caller passed only S3Bucket/S3Key (no
	// inline ZipFile). See CreateFunction for the same rationale.
	if len(fn.CodeZip) == 0 && fn.CodeS3Bucket != "" && fn.CodeS3Key != "" && h.s3Fetch != nil {
		if zip, err := h.s3Fetch(ctx, fn.CodeS3Bucket, fn.CodeS3Key); err == nil {
			fn.CodeZip = zip
			fn.CodeSize = int64(len(zip))
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

	if req.Runtime != "" && deprecatedRuntimes[req.Runtime] {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"The runtime "+req.Runtime+" is no longer supported.",
		))
		return
	}

	// Patch only non-zero fields.
	if req.Description != "" {
		fn.Description = req.Description
	}
	if req.Handler != "" {
		fn.Handler = req.Handler
	}
	if req.Role != "" {
		fn.Role = req.Role
	}
	if req.Timeout > 0 {
		fn.Timeout = req.Timeout
	}
	if req.MemorySize > 0 {
		fn.MemorySize = req.MemorySize
	}
	if req.Runtime != "" {
		fn.Runtime = req.Runtime
	}
	if req.Environment != nil {
		fn.Environment = req.Environment.Variables
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
		fn.VpcConfig = &VpcConfig{
			SubnetIds:        req.VpcConfig.SubnetIds,
			SecurityGroupIds: req.VpcConfig.SecurityGroupIds,
		}
		if h.vpcResolver != nil && len(req.VpcConfig.SubnetIds) > 0 {
			fn.VpcConfig.VpcId = h.vpcResolver.VpcIDForSubnet(ctx, req.VpcConfig.SubnetIds[0])
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

	// Evict the warm instance so the container is stopped immediately.
	for _, rt := range h.runtimes.get() {
		if pool, ok := rt.(*InstancePool); ok {
			pool.EvictFunction(name)
		}
	}

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
		h.asyncWg.Add(1)
		go func() {
			defer h.asyncWg.Done()
			h.invokeAsync(fn, rt, payload)
		}()
		return
	}

	// RequestResponse (default): synchronous execution — acquire, invoke, release.
	logType := r.Header.Get("X-Amz-Log-Type") // "Tail" or ""

	result := h.invokeSync(ctx, fn, rt, payload, name)

	// result.LogResult is populated by containerInstance.Invoke from the tail
	// buffer maintained by the streamLogs goroutine. Only expose it when the
	// caller explicitly requested a tail (X-Amz-Log-Type: Tail), matching
	// real AWS behaviour.
	if !strings.EqualFold(logType, "Tail") {
		result.LogResult = ""
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
		return &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "The function could not be created. Function is still being created.",
			HTTPStatus: http.StatusBadRequest,
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
		lv, aerr := h.ls.getLayerVersionByARN(ctx, arn)
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
func (h *Handler) invokeSync(ctx context.Context, fn *Function, rt Runtime, payload []byte, name string) *InvokeResult {
	if h.tracker != nil {
		h.tracker.Acquire(name, payload)
	}
	releaseSuccess := false
	releaseReason := ""
	defer func() {
		if h.tracker != nil {
			h.tracker.Release(name, releaseSuccess, releaseReason)
		}
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
		result := h.invokeSyncOnce(ctx, fn, rt, payload, name)
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

func lambdaInitTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.LambdaInitTimeout > 0 {
		return cfg.LambdaInitTimeout
	}
	return 10 * time.Second
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
func (h *Handler) invokeSyncOnce(ctx context.Context, fn *Function, rt Runtime, payload []byte, name string) *InvokeResult {
	acquireTimeout := 30 * time.Second
	// For very short function timeouts, keep acquire tighter.
	if fn.Timeout > 0 && fn.Timeout <= 5 {
		acquireTimeout = 20 * time.Second
	}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, acquireTimeout)
	defer acquireCancel()

	inst, err := rt.Acquire(acquireCtx, fn)
	if err != nil {
		h.log.Error("invoke: acquire instance", zap.String("function", name), zap.Error(err))
		return &InvokeResult{
			StatusCode:    200,
			Payload:       []byte(fmt.Sprintf(`{"errorMessage":%q,"errorType":"Runtime.InitError"}`, err.Error())),
			FunctionError: "Unhandled",
			acquireFailed: true,
		}
	}
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

	if h.tracker != nil {
		h.tracker.Ready(name)
	}

	// Capture the log stream name before invoking so it can be attached to the result.
	logStreamName := inst.LogStreamName()
	if h.tracker != nil {
		h.tracker.SetLogRefs(name, fn.logGroupName(), logStreamName)
	}

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
	timeoutSec := fn.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 3 // sensible default matching AWS
	}
	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	h.log.Debug("invoke function: dispatching", zap.String("function", name), zap.Int("payload_bytes", len(payload)))
	result, invokeErr := inst.Invoke(invokeCtx, payload)
	healthy := invokeErr == nil
	rt.Release(invokeCtx, inst, healthy)

	if invokeErr != nil {
		h.log.Error("invoke: execution error", zap.String("function", name), zap.Error(invokeErr))
		return &InvokeResult{
			StatusCode:    200,
			Payload:       []byte(fmt.Sprintf(`{"errorMessage":%q,"errorType":"Runtime.ExitError"}`, invokeErr.Error())),
			FunctionError: "Unhandled",
			LogGroupName:  fn.logGroupName(),
			LogStreamName: logStreamName,
		}
	}
	result.LogGroupName = fn.logGroupName()
	result.LogStreamName = logStreamName
	return result
}

// invokeAsync fires a function invocation in a goroutine, discarding the result.
// Used for Event-type invocations (HTTP 202, fire-and-forget).
func (h *Handler) invokeAsync(fn *Function, rt Runtime, payload []byte) {
	// Use a background context — the HTTP request has already returned (202).
	// Still bound by the function timeout so the container is killed on overrun.
	// Inject the function's region so EnsureLogStream writes to the correct
	// regional log group (background contexts have no region otherwise).
	timeoutSec := fn.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 3
	}
	region := regionFromFunctionARN(fn.ARN)
	if region == "" {
		region = h.cfg.Region
	}
	bgCtx := middleware.ContextWithRegion(context.Background(), region)
	ctx := bgCtx

	if h.tracker != nil {
		h.tracker.Acquire(fn.Name, payload)
	}
	inst, err := rt.Acquire(ctx, fn)
	if err != nil {
		if h.tracker != nil {
			h.tracker.Release(fn.Name, false, err.Error())
		}
		h.log.Error("invokeAsync: acquire instance", zap.String("function", fn.Name), zap.Error(err))
		return
	}
	if err := h.awaitRuntimeReady(ctx, fn, rt, inst); err != nil {
		rt.Release(ctx, inst, false)
		if h.tracker != nil {
			h.tracker.Release(fn.Name, false, err.Error())
		}
		h.log.Error("invokeAsync: runtime init", zap.String("function", fn.Name), zap.Error(err))
		return
	}

	// Ensure the log stream exists so container logs are captured.
	if h.logWriter != nil {
		if h.tracker != nil {
			h.tracker.SetLogRefs(fn.Name, fn.logGroupName(), inst.LogStreamName())
		}
		if lsErr := h.logWriter.EnsureLogStream(ctx, fn.logGroupName(), inst.LogStreamName()); lsErr != nil {
			h.log.Debug("invokeAsync: ensure log stream", zap.String("function", fn.Name), zap.Error(lsErr))
		}
	}

	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	result, invokeErr := inst.Invoke(invokeCtx, payload)
	rt.Release(invokeCtx, inst, invokeErr == nil)
	if h.tracker != nil {
		success := invokeErr == nil && result != nil && result.FunctionError == ""
		reason := ""
		if invokeErr != nil {
			reason = invokeErr.Error()
		} else if result != nil && result.FunctionError != "" {
			reason = result.FunctionError
		}
		h.tracker.Release(fn.Name, success, reason)
	}
	if invokeErr != nil {
		h.log.Error("invokeAsync: execution error", zap.String("function", fn.Name), zap.Error(invokeErr))
		return
	}
	if result != nil && result.FunctionError != "" {
		h.log.Debug("invokeAsync: function error", zap.String("function", fn.Name), zap.String("function_error", result.FunctionError))
	}
}

// ─── SSE Invoke (emulator-only) ───────────────────────────────────────────────

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

	sendEvent := func(event, data string) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
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

	if h.tracker != nil {
		h.tracker.Acquire(name, payload)
	}

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
		if h.tracker != nil {
			h.tracker.Release(name, false, err.Error())
		}
		h.log.Error("invoke-sse: acquire instance", zap.String("function", name), zap.Error(err))
		sendEvent("error", err.Error())
		return
	}
	if err := h.awaitRuntimeReady(ctx, fn, rt, inst); err != nil {
		rt.Release(ctx, inst, false)
		if h.tracker != nil {
			h.tracker.Release(name, false, err.Error())
		}
		h.log.Error("invoke-sse: runtime init", zap.String("function", name), zap.Error(err))
		sendEvent("error", err.Error())
		return
	}

	// Capture the log stream name before invoking so it can be attached to the result.
	logStreamName := inst.LogStreamName()
	if h.tracker != nil {
		h.tracker.SetLogRefs(name, fn.logGroupName(), logStreamName)
	}

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
	timeoutSec := fn.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 3
	}
	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	sendEvent("progress", "Invoking function handler")

	result, invokeErr := inst.Invoke(invokeCtx, payload)
	healthy := invokeErr == nil
	rt.Release(invokeCtx, inst, healthy)
	if h.tracker != nil {
		success := invokeErr == nil && result != nil && result.FunctionError == ""
		reason := ""
		if invokeErr != nil {
			reason = invokeErr.Error()
		} else if result != nil && result.FunctionError != "" {
			reason = result.FunctionError
		}
		h.tracker.Release(name, success, reason)
	}

	if invokeErr != nil {
		h.log.Error("invoke-sse: execution error", zap.String("function", name), zap.Error(invokeErr))
		errResult := InvokeResult{
			StatusCode:    200,
			Payload:       []byte(fmt.Sprintf(`{"errorMessage":%q,"errorType":"Runtime.ExitError"}`, invokeErr.Error())),
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
