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
	"slices"
	"sort"
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
	FunctionName        string                   `json:"FunctionName"`
	FunctionArn         string                   `json:"FunctionArn"`
	Runtime             string                   `json:"Runtime,omitempty"`
	Handler             string                   `json:"Handler,omitempty"`
	Role                string                   `json:"Role,omitempty"`
	Description         string                   `json:"Description,omitempty"`
	Timeout             int                      `json:"Timeout,omitempty"`
	MemorySize          int                      `json:"MemorySize,omitempty"`
	CodeSize            int64                    `json:"CodeSize,omitempty"`
	LastModified        string                   `json:"LastModified,omitempty"`
	RevisionId          string                   `json:"RevisionId,omitempty"`
	PackageType         string                   `json:"PackageType,omitempty"`
	Architectures       []string                 `json:"Architectures,omitempty"`
	State               string                   `json:"State,omitempty"`
	StateReason         string                   `json:"StateReason,omitempty"`
	StateReasonCode     string                   `json:"StateReasonCode,omitempty"`
	CodeSha256          string                   `json:"CodeSha256,omitempty"`
	Environment         *functionEnvConf         `json:"Environment,omitempty"`
	LoggingConfig       *loggingConfig           `json:"LoggingConfig,omitempty"`
	Layers              []LayerVersionLink       `json:"Layers,omitempty"`
	ImageUri            string                   `json:"ImageUri,omitempty"`
	ImageConfigResponse *imageConfigResponseWire `json:"ImageConfigResponse,omitempty"`
	VpcConfig           *vpcConfigResponse       `json:"VpcConfig,omitempty"`
	FileSystemConfigs   []FileSystemConfig       `json:"FileSystemConfigs,omitempty"`
	// TracingConfig and EphemeralStorage are always populated by
	// functionToConfig, carrying AWS's defaults for a function that set
	// neither, because AWS always returns both.
	TracingConfig    *tracingConfigWire    `json:"TracingConfig,omitempty"`
	EphemeralStorage *ephemeralStorageWire `json:"EphemeralStorage,omitempty"`
	KMSKeyArn        string                `json:"KMSKeyArn,omitempty"`
}

// tracingConfigWire is the AWS wire format for TracingConfig, in both the
// request and the response.
// https://docs.aws.amazon.com/lambda/latest/api/API_TracingConfig.html
type tracingConfigWire struct {
	Mode string `json:"Mode,omitempty"`
}

// ephemeralStorageWire is the AWS response format for EphemeralStorage.
// https://docs.aws.amazon.com/lambda/latest/api/API_EphemeralStorage.html
type ephemeralStorageWire struct {
	Size int `json:"Size"`
}

// ephemeralStorageRequest is the same shape on the way in, with Size as a
// pointer: Size is a `required` member, so an omitted one and an explicit 0
// are two different validation errors rather than the same zero value.
type ephemeralStorageRequest struct {
	Size *int `json:"Size"`
}

// imageConfigWire is the AWS wire format for ImageConfig.
type imageConfigWire struct {
	EntryPoint       []string `json:"EntryPoint,omitempty"`
	Command          []string `json:"Command,omitempty"`
	WorkingDirectory string   `json:"WorkingDirectory,omitempty"`
}

// imageConfigResponseWire is the AWS response envelope for container-image
// overrides. Lambda reserves Error for image-configuration processing errors.
type imageConfigResponseWire struct {
	ImageConfig *imageConfigWire      `json:"ImageConfig,omitempty"`
	Error       *imageConfigErrorWire `json:"Error,omitempty"`
}

type imageConfigErrorWire struct {
	ErrorCode string `json:"ErrorCode,omitempty"`
	Message   string `json:"Message,omitempty"`
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

// loggingConfigRequest keeps nested member presence so an omitted member can
// be distinguished from an explicitly empty value. Exact empty-object update
// semantics still need an AWS capture and are tracked in #660.
type loggingConfigRequest struct {
	LogGroup            *string `json:"LogGroup"`
	LogFormat           *string `json:"LogFormat"`
	ApplicationLogLevel *string `json:"ApplicationLogLevel"`
	SystemLogLevel      *string `json:"SystemLogLevel"`
}

func (c *loggingConfigRequest) empty() bool {
	return c != nil && c.LogGroup == nil && c.LogFormat == nil && c.ApplicationLogLevel == nil && c.SystemLogLevel == nil
}

// createFunctionRequest matches the AWS CreateFunction request body.
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateFunction.html
type createFunctionRequest struct {
	FunctionName  string                `json:"FunctionName"`
	Runtime       string                `json:"Runtime"`
	Handler       string                `json:"Handler"`
	Role          string                `json:"Role"`
	Description   string                `json:"Description,omitempty"`
	Timeout       *int                  `json:"Timeout,omitempty"`
	MemorySize    *int                  `json:"MemorySize,omitempty"`
	Environment   *envVariables         `json:"Environment,omitempty"`
	Code          *functionCode         `json:"Code,omitempty"`
	PackageType   string                `json:"PackageType,omitempty"`
	Architectures []string              `json:"Architectures,omitempty"`
	Tags          map[string]string     `json:"Tags,omitempty"`
	LoggingConfig *loggingConfigRequest `json:"LoggingConfig,omitempty"`
	VpcConfig     *vpcConfigRequest     `json:"VpcConfig,omitempty"`
	ImageConfig   *imageConfigWire      `json:"ImageConfig,omitempty"`
	// FileSystemConfigs mount EFS access points into the execution environment.
	FileSystemConfigs []FileSystemConfig `json:"FileSystemConfigs,omitempty"`
	// Optional; AWS requires only FunctionName, Role and Code.
	CodeSigningConfigArn string `json:"CodeSigningConfigArn,omitempty"`
	// Layers is a list of layer version ARNs to attach to the function at creation.
	Layers           []string                 `json:"Layers,omitempty"`
	TracingConfig    *tracingConfigWire       `json:"TracingConfig,omitempty"`
	EphemeralStorage *ephemeralStorageRequest `json:"EphemeralStorage,omitempty"`
	KMSKeyArn        *string                  `json:"KMSKeyArn"`
	// Each of these still answers 501 rather than being stored and echoed like
	// the three above; the reason for each is at CreateFunction's gate.
	DeadLetterConfig       json.RawMessage `json:"DeadLetterConfig"`
	SnapStart              json.RawMessage `json:"SnapStart"`
	CapacityProviderConfig json.RawMessage `json:"CapacityProviderConfig"`
	DurableConfig          json.RawMessage `json:"DurableConfig"`
	Publish                json.RawMessage `json:"Publish"`
	PublishTo              json.RawMessage `json:"PublishTo"`
	TenancyConfig          json.RawMessage `json:"TenancyConfig"`
}

type envVariables struct {
	Variables map[string]string `json:"Variables"`
}

type functionCode struct {
	ZipFile             []byte          `json:"ZipFile,omitempty"`
	S3Bucket            string          `json:"S3Bucket,omitempty"`
	S3Key               string          `json:"S3Key,omitempty"`
	S3ObjectVersion     string          `json:"S3ObjectVersion,omitempty"`
	ImageUri            string          `json:"ImageUri,omitempty"`
	S3ObjectStorageMode json.RawMessage `json:"S3ObjectStorageMode"`
	SourceKMSKeyArn     json.RawMessage `json:"SourceKMSKeyArn"`
}

// updateFunctionCodeRequest matches AWS UpdateFunctionCode request body.
type updateFunctionCodeRequest struct {
	ZipFile             []byte          `json:"ZipFile,omitempty"`
	S3Bucket            string          `json:"S3Bucket,omitempty"`
	S3Key               string          `json:"S3Key,omitempty"`
	S3ObjectVersion     string          `json:"S3ObjectVersion,omitempty"`
	ImageUri            string          `json:"ImageUri,omitempty"`
	Architectures       []string        `json:"Architectures,omitempty"`
	DryRun              json.RawMessage `json:"DryRun"`
	Publish             json.RawMessage `json:"Publish"`
	PublishTo           json.RawMessage `json:"PublishTo"`
	RevisionId          json.RawMessage `json:"RevisionId"`
	S3ObjectStorageMode json.RawMessage `json:"S3ObjectStorageMode"`
	SourceKMSKeyArn     json.RawMessage `json:"SourceKMSKeyArn"`
}

// updateFunctionConfigurationRequest matches AWS UpdateFunctionConfiguration body.
type updateFunctionConfigurationRequest struct {
	Description   *string               `json:"Description"`
	Handler       *string               `json:"Handler"`
	Role          *string               `json:"Role"`
	Timeout       *int                  `json:"Timeout"`
	MemorySize    *int                  `json:"MemorySize"`
	Runtime       *string               `json:"Runtime"`
	Environment   *envVariables         `json:"Environment,omitempty"`
	LoggingConfig *loggingConfigRequest `json:"LoggingConfig"`
	// Layers is a list of layer version ARNs to attach. An empty slice clears all layers.
	// A nil field means "no change".
	Layers      []string          `json:"Layers,omitempty"`
	VpcConfig   *vpcConfigRequest `json:"VpcConfig,omitempty"`
	ImageConfig *imageConfigWire  `json:"ImageConfig,omitempty"`
	// FileSystemConfigs replaces the function's EFS mounts. An empty slice
	// clears them; a nil field means "no change".
	FileSystemConfigs []FileSystemConfig       `json:"FileSystemConfigs,omitempty"`
	TracingConfig     *tracingConfigWire       `json:"TracingConfig,omitempty"`
	EphemeralStorage  *ephemeralStorageRequest `json:"EphemeralStorage,omitempty"`
	KMSKeyArn         *string                  `json:"KMSKeyArn"`
	// Each of these still answers 501 rather than being stored and echoed like
	// the three above; the reason for each is at CreateFunction's gate.
	DeadLetterConfig       json.RawMessage `json:"DeadLetterConfig"`
	SnapStart              json.RawMessage `json:"SnapStart"`
	CapacityProviderConfig json.RawMessage `json:"CapacityProviderConfig"`
	DurableConfig          json.RawMessage `json:"DurableConfig"`
	RevisionId             json.RawMessage `json:"RevisionId"`
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
var (
	localMountPathRe = regexp.MustCompile(`^/mnt/[a-zA-Z0-9-_.]+$`)
	fileSystemARNRe  = regexp.MustCompile(`^(?:arn:aws[a-zA-Z-]*:elasticfilesystem:[a-z]{2}((-gov)|(-iso(b?)))?-[a-z]+-\d{1}:\d{12}:access-point/fsap-[a-f0-9]{17}|arn:aws[-a-z]*:s3files:[0-9a-z-:]+:file-system/fs-[0-9a-f]{17,40}/access-point/fsap-[0-9a-f]{17,40})$`)
)

const (
	fileSystemARNConstraint  = `arn:aws[a-zA-Z-]*:elasticfilesystem:[a-z]{2}((-gov)|(-iso(b?)))?-[a-z]+-\d{1}:\d{12}:access-point/fsap-[a-f0-9]{17}$|^arn:aws[-a-z]*:s3files:[0-9a-z-:]+:file-system/fs-[0-9a-f]{17,40}/access-point/fsap-[0-9a-f]{17,40}`
	localMountPathConstraint = `/mnt/[a-zA-Z0-9-_.]+`
)

// validateFileSystemConfigs enforces the AWS FileSystemConfig rules shared by
// CreateFunction and UpdateFunctionConfiguration: at most one config, an EFS
// or S3 Files access-point ARN, and a /mnt/<name> mount path.
// https://docs.aws.amazon.com/lambda/latest/api/API_FileSystemConfig.html
func validateFileSystemConfigs(configs []FileSystemConfig) *protocol.AWSError {
	if len(configs) == 0 {
		return nil
	}
	if len(configs) > 1 {
		return smithyLengthConstraint("fileSystemConfigs", len(configs), 1)
	}
	fsc := configs[0]
	if len(fsc.Arn) > 256 || !fileSystemARNRe.MatchString(fsc.Arn) {
		return smithyPatternConstraint("fileSystemConfigs.1.member.arn", fsc.Arn, fileSystemARNConstraint)
	}
	if len(fsc.LocalMountPath) > 160 || !localMountPathRe.MatchString(fsc.LocalMountPath) {
		return smithyPatternConstraint("fileSystemConfigs.1.member.localMountPath", fsc.LocalMountPath, localMountPathConstraint)
	}
	return nil
}

func smithyIntegerConstraint(member string, value, minimum, maximum int) *protocol.AWSError {
	constraint := "Member must have value greater than or equal to " + strconv.Itoa(minimum)
	if value > maximum {
		constraint = "Member must have value less than or equal to " + strconv.Itoa(maximum)
	}
	return lambdaInvalidParameter("1 validation error detected: Value '" + strconv.Itoa(value) + "' at '" + member + "' failed to satisfy constraint: " + constraint)
}

func smithyRequiredConstraint(member string) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value null at '" + member + "' failed to satisfy constraint: Member must not be null")
}

func smithyLengthConstraint(member string, length, maximum int) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value '" + strconv.Itoa(length) + "' at '" + member + "' failed to satisfy constraint: Member must have length less than or equal to " + strconv.Itoa(maximum))
}

func smithyStringLengthConstraint(member, value string, maximum int) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value '" + value + "' at '" + member + "' failed to satisfy constraint: Member must have length less than or equal to " + strconv.Itoa(maximum))
}

func smithyStringMinimumLengthConstraint(member, value string, minimum int) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value '" + value + "' at '" + member + "' failed to satisfy constraint: Member must have length greater than or equal to " + strconv.Itoa(minimum))
}

// smithyRequiredMember reports an omitted `required` member the way AWS's
// generated request validation does.
func smithyRequiredMember(member string) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value null at '" + member + "' failed to satisfy constraint: Member must not be null")
}

func smithyPatternConstraint(member, value, pattern string) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value '" + value + "' at '" + member + "' failed to satisfy constraint: Member must satisfy regular expression pattern: " + pattern)
}

var loggingConfigLogGroupPattern = regexp.MustCompile(`^[\.\-_/#A-Za-z0-9]+$`)

const loggingConfigLogGroupConstraint = `[\.\-_/#A-Za-z0-9]+`

func smithyEnumConstraint(member, value string, allowed ...string) *protocol.AWSError {
	return lambdaInvalidParameter("1 validation error detected: Value '" + value + "' at '" + member + "' failed to satisfy constraint: Member must satisfy enum value set: [" + strings.Join(allowed, ", ") + "]")
}

func normalizeLoggingConfig(req *loggingConfigRequest, functionName string) (*loggingConfig, *protocol.AWSError) {
	if req == nil {
		return nil, nil
	}
	config := &loggingConfig{LogGroup: "/aws/lambda/" + functionName, LogFormat: "Text"}
	if req.LogGroup != nil {
		if len(*req.LogGroup) < 1 {
			return nil, smithyStringMinimumLengthConstraint("loggingConfig.logGroup", *req.LogGroup, 1)
		}
		if len(*req.LogGroup) > 512 {
			return nil, smithyStringLengthConstraint("loggingConfig.logGroup", *req.LogGroup, 512)
		}
		if !loggingConfigLogGroupPattern.MatchString(*req.LogGroup) {
			return nil, smithyPatternConstraint("loggingConfig.logGroup", *req.LogGroup, loggingConfigLogGroupConstraint)
		}
		config.LogGroup = *req.LogGroup
	}
	if req.LogFormat != nil {
		if *req.LogFormat != "JSON" && *req.LogFormat != "Text" {
			return nil, smithyEnumConstraint("loggingConfig.logFormat", *req.LogFormat, "JSON", "Text")
		}
		config.LogFormat = *req.LogFormat
	}
	if req.ApplicationLogLevel != nil {
		switch *req.ApplicationLogLevel {
		case "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL":
		default:
			return nil, smithyEnumConstraint("loggingConfig.applicationLogLevel", *req.ApplicationLogLevel, "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL")
		}
		config.ApplicationLogLevel = *req.ApplicationLogLevel
	}
	if req.SystemLogLevel != nil {
		switch *req.SystemLogLevel {
		case "DEBUG", "INFO", "WARN":
		default:
			return nil, smithyEnumConstraint("loggingConfig.systemLogLevel", *req.SystemLogLevel, "DEBUG", "INFO", "WARN")
		}
		config.SystemLogLevel = *req.SystemLogLevel
	}
	if config.LogFormat != "JSON" && (req.ApplicationLogLevel != nil || req.SystemLogLevel != nil) {
		// AWS documents that level filtering requires JSON, but does not publish
		// the exact service-rendered cross-field message. Capture parity is #660.
		return nil, lambdaInvalidParameter("ApplicationLogLevel and SystemLogLevel can only be set for functions configured to use JSON log format.")
	}
	if config.LogFormat == "JSON" {
		if config.ApplicationLogLevel == "" {
			config.ApplicationLogLevel = "INFO"
		}
		if config.SystemLogLevel == "" {
			config.SystemLogLevel = "INFO"
		}
	}
	return config, nil
}

func applyLoggingConfig(fn *Function, config *loggingConfig) {
	if config == nil {
		return
	}
	if config.LogGroup == "/aws/lambda/"+fn.Name {
		fn.LogGroup = ""
	} else {
		fn.LogGroup = config.LogGroup
	}
	fn.LogFormat = config.LogFormat
	fn.ApplicationLogLevel = config.ApplicationLogLevel
	fn.SystemLogLevel = config.SystemLogLevel
}

func validateVpcConfig(config *vpcConfigRequest) *protocol.AWSError {
	if config == nil {
		return nil
	}
	if len(config.SubnetIds) > 16 {
		return smithyLengthConstraint("vpcConfig.subnetIds", len(config.SubnetIds), 16)
	}
	if len(config.SecurityGroupIds) > 5 {
		return smithyLengthConstraint("vpcConfig.securityGroupIds", len(config.SecurityGroupIds), 5)
	}
	return nil
}

func validateImageConfig(config *imageConfigWire) *protocol.AWSError {
	if config == nil {
		return nil
	}
	if len(config.EntryPoint) > 1500 {
		return smithyLengthConstraint("imageConfig.entryPoint", len(config.EntryPoint), 1500)
	}
	if len(config.Command) > 1500 {
		return smithyLengthConstraint("imageConfig.command", len(config.Command), 1500)
	}
	if len(config.WorkingDirectory) > 1000 {
		return smithyLengthConstraint("imageConfig.workingDirectory", len(config.WorkingDirectory), 1000)
	}
	return nil
}

func validateArchitectures(architectures []string) *protocol.AWSError {
	if len(architectures) != 1 || (architectures[0] != "x86_64" && architectures[0] != "arm64") {
		return lambdaInvalidParameter("1 validation error detected: Value '" + strings.Join(architectures, ",") + "' at 'architectures' failed to satisfy constraint: Member must satisfy enum value set: [x86_64, arm64]")
	}
	return nil
}

// ─── advanced configuration: tracing, ephemeral storage, KMS key ─────────────
//
// These three members are stored and echoed rather than refused, because
// accepting them claims nothing a caller can observe as false:
//
//   - TracingConfig — there is no X-Ray service in Overcast at all, so no trace
//     is produced or missing whichever mode is set. Rejecting it broke every
//     CDK deploy with `tracing` enabled, since CDK emits the member whenever
//     the construct sets it.
//   - EphemeralStorage — the size is recorded and echoed, not enforced on the
//     container's /tmp. A function cannot observe a wrong answer from the API;
//     it would only observe more space than it asked for.
//   - KMSKeyArn — recorded as an association. Environment variables are stored
//     in plaintext regardless, as everything in Overcast is; encryption at rest
//     is a security-boundary promise Overcast never makes anywhere.
//
// The members that remain 501 are listed at CreateFunction's gate, with the
// reason each one stays refused.

// tracingModes is AWS's TracingMode enum.
var tracingModes = []string{"Active", "PassThrough"}

// defaultTracingMode and defaultEphemeralStorageSize are what AWS reports for a
// function that never set either member.
const (
	defaultTracingMode          = "PassThrough"
	defaultEphemeralStorageSize = 512
)

// kmsKeyARNPattern is AWS's own pattern for the Lambda KMSKeyArn member. The
// empty alternative is AWS's: passing "" is how a key association is removed.
var kmsKeyARNPattern = regexp.MustCompile(`^((arn:(aws[a-zA-Z-]*)?:[a-z0-9-.]+:.*)|())$`)

const kmsKeyARNConstraint = `(arn:(aws[a-zA-Z-]*)?:[a-z0-9-.]+:.*)|()`

// validateTracingConfig checks Mode against the modeled enum. Mode is optional
// in AWS's request shape, so an empty object is accepted and means "default".
func validateTracingConfig(config *tracingConfigWire) *protocol.AWSError {
	if config == nil || config.Mode == "" {
		return nil
	}
	if !slices.Contains(tracingModes, config.Mode) {
		return smithyEnumConstraint("tracingConfig.mode", config.Mode, tracingModes...)
	}
	return nil
}

// validateEphemeralStorage checks Size against AWS's modeled range. Size is a
// required member of EphemeralStorage, so an empty object is an error.
// https://docs.aws.amazon.com/lambda/latest/api/API_EphemeralStorage.html
func validateEphemeralStorage(storage *ephemeralStorageRequest) *protocol.AWSError {
	if storage == nil {
		return nil
	}
	if storage.Size == nil {
		return smithyRequiredMember("ephemeralStorage.size")
	}
	if *storage.Size < 512 || *storage.Size > 10240 {
		return smithyIntegerConstraint("ephemeralStorage.size", *storage.Size, 512, 10240)
	}
	return nil
}

func validateKMSKeyArn(arn *string) *protocol.AWSError {
	if arn == nil {
		return nil
	}
	if !kmsKeyARNPattern.MatchString(*arn) {
		return smithyPatternConstraint("kMSKeyArn", *arn, kmsKeyARNConstraint)
	}
	return nil
}

// validateAdvancedConfiguration runs the three members' modeled constraints in
// the order AWS declares them, so CreateFunction and
// UpdateFunctionConfiguration cannot drift on which error a request gets.
func validateAdvancedConfiguration(tracing *tracingConfigWire, storage *ephemeralStorageRequest, kmsKeyArn *string) *protocol.AWSError {
	if aerr := validateTracingConfig(tracing); aerr != nil {
		return aerr
	}
	if aerr := validateEphemeralStorage(storage); aerr != nil {
		return aerr
	}
	return validateKMSKeyArn(kmsKeyArn)
}

// applyAdvancedConfiguration writes the three members onto the function. Each
// is presence-aware: a nil member leaves the stored value alone, which is what
// UpdateFunctionConfiguration needs and what CreateFunction gets for free.
func applyAdvancedConfiguration(fn *Function, tracing *tracingConfigWire, storage *ephemeralStorageRequest, kmsKeyArn *string) {
	if tracing != nil && tracing.Mode != "" {
		fn.TracingMode = tracing.Mode
	}
	if storage != nil && storage.Size != nil {
		fn.EphemeralStorageSize = *storage.Size
	}
	if kmsKeyArn != nil {
		fn.KMSKeyArn = *kmsKeyArn
	}
}

type unsupportedRequestField struct {
	present bool
}

// rawRequestField reports whether a modeled member Overcast does not implement
// was actually *asked for*. Serialised presence is not the same question: AWS's
// default for each of these members is its falsy value, and SDKs,
// CloudFormation clear-values and hand-written clients all send that value
// explicitly. `"Publish": false` asks for nothing, so answering it with a 501
// fails an ordinary no-op call.
//
// A JSON value means "do nothing" when it is null, false, "", [] or {}.
// Anything else — true, a number, a non-empty string, a populated list or
// object — is a real request and still returns 501.
func rawRequestField(raw json.RawMessage) unsupportedRequestField {
	return unsupportedRequestField{present: rawJSONRequestsFeature(raw)}
}

func rawJSONRequestsFeature(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		// Undecodable JSON cannot be shown to be a no-op. The surrounding
		// decode has already accepted the body, so this is unreachable in
		// practice; never treat it as absent.
		return true
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		// Numbers: any number the caller bothered to send names a value, and
		// none of the gated numeric members treats zero as "leave it alone".
		return true
	}
}

// unsupportedRequestMembers maps each modeled request member an operation
// refuses with a 501 to the value this caller sent for it. It is the single
// definition of that operation's gate: the handler asks it whether anything was
// requested, and UnsupportedRequestMembers reads the same table for its names,
// so the two cannot drift apart.
//
// A nested member is keyed by its path — "Code.SourceKMSKeyArn".
type unsupportedRequestMembers map[string]unsupportedRequestField

// requested reports whether the caller asked for any member in the table.
func (m unsupportedRequestMembers) requested() bool {
	for _, field := range m {
		if field.present {
			return true
		}
	}
	return false
}

// Lambda operation names used as keys by UnsupportedRequestMembers. They are
// the AWS operation names, so a caller can line them up with the API it is
// dispatching to.
const (
	OpCreateFunction              = "CreateFunction"
	OpUpdateFunctionCode          = "UpdateFunctionCode"
	OpUpdateFunctionConfiguration = "UpdateFunctionConfiguration"
	OpCreateEventSourceMapping    = "CreateEventSourceMapping"
	OpUpdateEventSourceMapping    = "UpdateEventSourceMapping"
)

// UnsupportedRequestMembers reports the modeled request members each Lambda
// operation refuses with a 501, keyed by AWS operation name and sorted.
//
// It exists so that another package can check itself against Lambda's real
// gate rather than a copy of it: a caller that forwards one of these members
// produces a request that can only ever fail. CloudFormation's provisioner is
// the caller that matters — see
// TestLambdaProvisionerForwardsOnlyReviewedGatedMembers.
func UnsupportedRequestMembers() map[string][]string {
	gates := map[string]unsupportedRequestMembers{
		OpCreateFunction:              (&createFunctionRequest{Code: &functionCode{}}).unsupportedMembers(),
		OpUpdateFunctionCode:          (&updateFunctionCodeRequest{}).unsupportedMembers(),
		OpUpdateFunctionConfiguration: (&updateFunctionConfigurationRequest{}).unsupportedMembers(),
		OpCreateEventSourceMapping:    (&createESMRequest{}).unsupportedMembers(),
		OpUpdateEventSourceMapping:    (&updateESMRequest{}).unsupportedMembers(),
	}
	out := make(map[string][]string, len(gates))
	for operation, members := range gates {
		names := make([]string, 0, len(members))
		for name := range members {
			names = append(names, name)
		}
		sort.Strings(names)
		out[operation] = names
	}
	return out
}

// unsupportedMembers is CreateFunction's 501 gate. Each member here stays
// refused because accepting one would promise behaviour a caller can observe
// Overcast not delivering — unlike TracingConfig, EphemeralStorage and
// KMSKeyArn, which are stored and echoed (see "advanced configuration" above):
//
//   - DeadLetterConfig — accepting it says failed async invocations reach the
//     target queue or topic. They do not: async retry and on-failure delivery
//     are not emulated outside event source mappings, so the DLQ would stay
//     silently empty and the caller would never learn why.
//   - SnapStart — promises a restored snapshot and the init-phase semantics
//     that come with it; execution environments here always cold start.
//   - CapacityProviderConfig, DurableConfig, TenancyConfig — each selects an
//     execution substrate Overcast has no equivalent of.
//   - Publish and PublishTo — publishing on create must report the resulting
//     version, and functionConfiguration has no Version member to report it
//     in. PublishVersion exists, so this is implementable; adding Version to
//     the shared response shape is the work.
//   - Code.S3ObjectStorageMode and Code.SourceKMSKeyArn — the storage mode and
//     encryption of the deployment package are not emulated. Code.S3ObjectVersion
//     is *not* here: it selects a real object version and is honoured.
func (req *createFunctionRequest) unsupportedMembers() unsupportedRequestMembers {
	members := unsupportedRequestMembers{
		"DeadLetterConfig":       rawRequestField(req.DeadLetterConfig),
		"SnapStart":              rawRequestField(req.SnapStart),
		"CapacityProviderConfig": rawRequestField(req.CapacityProviderConfig),
		"DurableConfig":          rawRequestField(req.DurableConfig),
		"Publish":                rawRequestField(req.Publish),
		"PublishTo":              rawRequestField(req.PublishTo),
		"TenancyConfig":          rawRequestField(req.TenancyConfig),
	}
	code := req.Code
	if code == nil {
		code = &functionCode{}
	}
	members["Code.S3ObjectStorageMode"] = rawRequestField(code.S3ObjectStorageMode)
	members["Code.SourceKMSKeyArn"] = rawRequestField(code.SourceKMSKeyArn)
	return members
}

// unsupportedMembers is UpdateFunctionCode's 501 gate. DryRun, Publish/PublishTo
// and RevisionId refuse for the same reasons as CreateFunction's; S3ObjectStorageMode
// and SourceKMSKeyArn are the same two package members CreateFunction refuses
// under Code. S3ObjectVersion is absent from both: it is honoured.
func (req *updateFunctionCodeRequest) unsupportedMembers() unsupportedRequestMembers {
	return unsupportedRequestMembers{
		"DryRun":              rawRequestField(req.DryRun),
		"Publish":             rawRequestField(req.Publish),
		"PublishTo":           rawRequestField(req.PublishTo),
		"RevisionId":          rawRequestField(req.RevisionId),
		"S3ObjectStorageMode": rawRequestField(req.S3ObjectStorageMode),
		"SourceKMSKeyArn":     rawRequestField(req.SourceKMSKeyArn),
	}
}

// unsupportedMembers is UpdateFunctionConfiguration's 501 gate. Same rationale
// as CreateFunction's, plus one member only this operation takes: RevisionId is
// an optimistic-concurrency precondition, so accepting it would silently skip
// the very check the caller asked for.
func (req *updateFunctionConfigurationRequest) unsupportedMembers() unsupportedRequestMembers {
	return unsupportedRequestMembers{
		"DeadLetterConfig":       rawRequestField(req.DeadLetterConfig),
		"SnapStart":              rawRequestField(req.SnapStart),
		"CapacityProviderConfig": rawRequestField(req.CapacityProviderConfig),
		"DurableConfig":          rawRequestField(req.DurableConfig),
		"RevisionId":             rawRequestField(req.RevisionId),
	}
}

// ─── runtime metadata ────────────────────────────────────────────────────────

// RuntimeInfo describes a Lambda runtime with its metadata. Built from
// lambdaRuntimeCatalog — see runtime_catalog.go.
type RuntimeInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Family         string `json:"family"`
	DefaultHandler string `json:"defaultHandler"`
	ImageURI       string `json:"imageUri,omitempty"`
	// Deprecated reports that AWS has ended support for this runtime. AWS
	// still accepts it until CreateBlocked.
	Deprecated bool `json:"deprecated"`
	// CreateBlocked and UpdateBlocked report AWS's two later deprecation
	// phases: no new functions, then no updates to existing ones.
	CreateBlocked bool `json:"createBlocked"`
	UpdateBlocked bool `json:"updateBlocked"`
	// Supported indicates the emulator can actually execute this runtime.
	Supported bool `json:"supported"`
}

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
	if cfg.LoggingConfig.LogFormat == "JSON" {
		if cfg.LoggingConfig.ApplicationLogLevel == "" {
			cfg.LoggingConfig.ApplicationLogLevel = "INFO"
		}
		if cfg.LoggingConfig.SystemLogLevel == "" {
			cfg.LoggingConfig.SystemLogLevel = "INFO"
		}
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
		cfg.ImageConfigResponse = &imageConfigResponseWire{ImageConfig: &imageConfigWire{
			EntryPoint:       fn.ImageConfig.EntryPoint,
			Command:          fn.ImageConfig.Command,
			WorkingDirectory: fn.ImageConfig.WorkingDirectory,
		}}
	}
	if len(fn.FileSystemConfigs) > 0 {
		cfg.FileSystemConfigs = fn.FileSystemConfigs
	}
	// AWS reports both of these on every function, defaulted when unset, so a
	// function created before these members were stored reads as an AWS default
	// rather than as an absent field.
	tracingMode := fn.TracingMode
	if tracingMode == "" {
		tracingMode = defaultTracingMode
	}
	cfg.TracingConfig = &tracingConfigWire{Mode: tracingMode}
	ephemeralStorageSize := fn.EphemeralStorageSize
	if ephemeralStorageSize == 0 {
		ephemeralStorageSize = defaultEphemeralStorageSize
	}
	cfg.EphemeralStorage = &ephemeralStorageWire{Size: ephemeralStorageSize}
	cfg.KMSKeyArn = fn.KMSKeyArn
	return cfg
}

// ─── Handler methods ──────────────────────────────────────────────────────────

// ListFunctions handles GET /2015-03-31/functions.
func (h *Handler) ListFunctions(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	log.Debug("list functions")
	fns, aerr := h.ls.listFunctions(r.Context())
	if aerr != nil {
		log.Error("list functions: store error", zap.Error(aerr.Unwrap()))
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

// ListRuntimes handles GET /_lambda/runtimes (emulator-only). The catalog is
// the same table CreateFunction validates against, so what the web UI offers
// and what the API accepts cannot disagree.
func (h *Handler) ListRuntimes(w http.ResponseWriter, _ *http.Request) {
	catalog := runtimeCatalog(h.clk.Now())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Runtimes []RuntimeInfo `json:"runtimes"`
	}{Runtimes: catalog})
}

// CreateFunction handles POST /2015-03-31/functions.
func (h *Handler) CreateFunction(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
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
	// unsupportedMembers lists what stays 501 here, and why each one does.
	if req.unsupportedMembers().requested() {
		protocol.NotImplementedJSON(w, r)
		return
	}
	if aerr := validateAdvancedConfiguration(req.TracingConfig, req.EphemeralStorage, req.KMSKeyArn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateVpcConfig(req.VpcConfig); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateImageConfig(req.ImageConfig); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateFileSystemConfigs(req.FileSystemConfigs); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	loggingConfig, aerr := normalizeLoggingConfig(req.LoggingConfig, req.FunctionName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()

	log.Debug("create function", zap.String("function", req.FunctionName), zap.String("runtime", req.Runtime))

	// Apply AWS defaults.
	timeout := 3
	if req.Timeout != nil {
		timeout = *req.Timeout
	}
	memorySize := 128
	if req.MemorySize != nil {
		memorySize = *req.MemorySize
	}
	packageType := req.PackageType
	if packageType == "" {
		packageType = "Zip"
	}
	if packageType != "Zip" && packageType != "Image" {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("1 validation error detected: Value '"+packageType+"' at 'packageType' failed to satisfy constraint: Member must satisfy enum value set: [Zip, Image]"))
		return
	}
	if req.Timeout != nil && (*req.Timeout < 1 || *req.Timeout > 900) {
		protocol.WriteJSONError(w, r, smithyIntegerConstraint("timeout", *req.Timeout, 1, 900))
		return
	}
	if req.MemorySize != nil && (*req.MemorySize < 128 || *req.MemorySize > 32768) {
		protocol.WriteJSONError(w, r, smithyIntegerConstraint("memorySize", *req.MemorySize, 128, 32768))
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
	if req.Runtime != "" {
		if aerr := validateLambdaRuntime(req.Runtime); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}
	// Reject runtimes AWS has blocked from CreateFunction, after modeled
	// request-shape validation. This is AWS's second deprecation phase: a
	// runtime past its deprecation date but before its block-create date is
	// still accepted, so this is a date comparison and not a "deprecated" flag.
	// The check applies only when a runtime is specified (image functions omit it).
	if req.Runtime != "" && runtimeCreateBlocked(req.Runtime, h.clk.Now()) {
		protocol.WriteJSONError(w, r, lambdaDeprecatedRuntimeError(req.Runtime))
		return
	}
	if req.CodeSigningConfigArn != "" && !codeSigningConfigARNPattern.MatchString(req.CodeSigningConfigArn) {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "Invalid code signing config arn: " + req.CodeSigningConfigArn,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
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
	}
	// Modeled request validation runs before state-dependent service checks.
	existing, aerr := h.ls.getFunction(ctx, req.FunctionName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, lambdaFunctionAlreadyExists(req.FunctionName))
		return
	}
	if packageType == "Zip" {
		if !lambdaRuntimeExecutionSupported(req.Runtime) {
			// The runtime is valid in AWS's model, but Overcast cannot execute it
			// yet. Report an honest emulator gap without persisting a function.
			protocol.NotImplementedJSON(w, r)
			return
		}
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
		CreationID:      uuid.NewString(),
		LastModified:    h.clk.Now().UTC().Format(time.RFC3339),
		Tags:            copyTags(req.Tags),
	}
	applyLoggingConfig(fn, loggingConfig)
	applyAdvancedConfiguration(fn, req.TracingConfig, req.EphemeralStorage, req.KMSKeyArn)
	if req.CodeSigningConfigArn != "" {
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
		fn.CodeS3ObjectVersion = req.Code.S3ObjectVersion
		fn.ImageUri = req.Code.ImageUri

		// Fetch the code from S3 when the caller provided S3Bucket/S3Key but
		// no inline ZipFile. CDK deploys upload the zip to S3 *before*
		// CreateFunction, so the s3SyncWatcher (which only fires on
		// subsequent PutObjects) wouldn't otherwise see this code and the
		// function would invoke with an empty zip.
		//
		// A GetObject failure fails the whole create, as on AWS. Nothing is
		// persisted before this point, so the caller is left with no function
		// at all rather than one whose code is missing.
		if len(fn.CodeZip) == 0 && fn.CodeS3Bucket != "" && fn.CodeS3Key != "" {
			if h.s3Fetch == nil {
				protocol.NotImplementedJSON(w, r)
				return
			}
			zip, fetchErr := h.s3Fetch(ctx, fn.CodeS3Bucket, fn.CodeS3Key, fn.CodeS3ObjectVersion)
			if fetchErr != nil {
				log.Debug("lambda: create function: s3 fetch failed",
					zap.String("function", fn.Name),
					zap.String("bucket", fn.CodeS3Bucket),
					zap.String("key", fn.CodeS3Key),
					zap.String("version", fn.CodeS3ObjectVersion),
					zap.Error(fetchErr),
				)
				protocol.WriteJSONError(w, r, lambdaS3CodeFetchError(fetchErr))
				return
			}
			fn.setCode(zip)
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

	pullGeneration, err := imagePullGenerationForFunction(fn)
	if err != nil {
		log.Error("lambda: create function: resolve prewarm generation", zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	created, aerr := h.ls.createFunction(ctx, fn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !created {
		protocol.WriteJSONError(w, r, lambdaFunctionAlreadyExists(req.FunctionName))
		return
	}

	// Kick off the Docker image pull in the background if the runtime is
	// ready. The function stays in "Pending" until the pull completes, then
	// flips to "Active" — matching AWS semantics and keeping the cold-pull
	// cost off the first Invoke. If the runtime isn't wired (tests, or
	// Docker still initialising), mark Active immediately.
	if !h.startFunctionPrewarm(fn) {
		active, changed, _ := h.ls.mutateFunction(ctx, fn.Name, func(current *Function) (bool, *protocol.AWSError) {
			if !pullGeneration.matches(current) {
				return false, nil
			}
			current.State = "Active"
			current.StateReason = ""
			current.StateReasonCode = ""
			return true, nil
		})
		if changed {
			fn = active
			h.proactive.NoteFunctionChanged(active)
		}
	}

	// Auto-create CloudWatch Logs log group (idempotent). The log stream is
	// created per-invocation with an AWS-style name, not here at create time.
	if h.logWriter != nil {
		if err := h.logWriter.EnsureLogGroup(ctx, fn.logGroupName()); err != nil {
			log.Warn("lambda: create function: failed to ensure CWL log group",
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

func (h *Handler) startFunctionPrewarm(fn *Function) bool {
	if fn == nil {
		return false
	}
	h.prewarmMu.Lock()
	prewarmer := h.prewarmer
	h.prewarmMu.Unlock()
	if prewarmer == nil {
		return false
	}
	generation, err := imagePullGenerationForFunction(fn)
	if err != nil {
		h.log.Warn("lambda: cannot resolve function image for prewarm",
			zap.String("function", fn.Name), zap.Error(err))
		return true
	}
	key := generation.arn
	h.prewarmMu.Lock()
	if h.prewarming == nil {
		h.prewarming = make(map[string]functionImagePullGeneration)
	}
	if active, ok := h.prewarming[key]; ok && active == generation {
		h.prewarmMu.Unlock()
		return true
	}
	h.prewarming[key] = generation
	h.prewarmMu.Unlock()

	prewarmer(fn, func(pullErr error) {
		h.completeFunctionPrewarm(fn.Name, serviceutil.ARNRegion(fn.ARN), generation, pullErr)
	})
	return true
}

func (h *Handler) completeFunctionPrewarm(name, region string, generation functionImagePullGeneration, pullErr error) {
	key := generation.arn
	defer func() {
		h.prewarmMu.Lock()
		if active, ok := h.prewarming[key]; ok && active == generation {
			delete(h.prewarming, key)
		}
		h.prewarmMu.Unlock()
	}()

	nextState, nextReason, nextReasonCode := "Active", "", ""
	if pullErr != nil {
		nextState = "Failed"
		nextReason = "Failed to pull container image: " + pullErr.Error()
		nextReasonCode = "ImagePullError"
		h.log.Warn("lambda: background image pull failed", zap.String("function", name), zap.Error(pullErr))
	}
	ctx := middleware.ContextWithRegion(context.Background(), region)
	const maxAttempts = 5
	for i := range maxAttempts {
		fresh, changed, aerr := h.ls.mutateFunction(ctx, name, func(current *Function) (bool, *protocol.AWSError) {
			if !generation.matches(current) {
				return false, nil
			}
			current.State = nextState
			current.StateReason = nextReason
			current.StateReasonCode = nextReasonCode
			return true, nil
		})
		if aerr == nil {
			if changed {
				if fresh.State == "Active" {
					h.proactive.NoteFunctionChanged(fresh)
				}
				return
			}
			// The pulled generation was replaced. Arrange the current Pending
			// generation's pull only after mutateFunction released its mutex.
			if fresh != nil && fresh.State == "Pending" {
				h.startFunctionPrewarm(fresh)
			}
			return
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
		}
	}
	h.log.Warn("lambda: failed to persist state transition after image pull", zap.String("function", name))
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

	fn, changed, aerr := h.ls.mutateFunction(r.Context(), name, func(current *Function) (bool, *protocol.AWSError) {
		current.CodeSigningConfigArn = req.CodeSigningConfigArn
		return true, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !changed || fn == nil {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(name))
		return
	}
	writeCodeSigningConfig(w, r, fn.CodeSigningConfigArn, name)
}

// DeleteFunctionCodeSigningConfig handles DELETE /2020-06-30/functions/{name}/code-signing-config.
// Returns 204 with an empty body, and is idempotent: removing an association
// that is not there is not an error.
func (h *Handler) DeleteFunctionCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	fn, name := h.functionForCodeSigning(w, r)
	if fn == nil {
		return
	}
	fn, _, aerr := h.ls.mutateFunction(r.Context(), name, func(current *Function) (bool, *protocol.AWSError) {
		if current.CodeSigningConfigArn == "" {
			return false, nil
		}
		current.CodeSigningConfigArn = ""
		return true, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(name))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetFunction handles GET /2015-03-31/functions/{name}.
// Returns FunctionConfiguration + Code location block.
func (h *Handler) GetFunction(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("get function", zap.String("function", name))
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
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("get function configuration", zap.String("function", name))
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
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("update function code", zap.String("function", name))
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
	if req.unsupportedMembers().requested() {
		protocol.NotImplementedJSON(w, r)
		return
	}
	if h.cfg.LambdaHotReload {
		if _, err := validateFunctionHotReloadConfig(fn); err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(err.Error()))
			return
		}
	}
	if len(req.Architectures) > 0 {
		if aerr := validateArchitectures(req.Architectures); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}
	if aerr, unsupported := validateUpdateFunctionCodeSource(fn.PackageType, req); unsupported {
		protocol.NotImplementedJSON(w, r)
		return
	} else if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Download S3 code before entering the store mutation. A failed GetObject
	// must leave the package, source metadata, and RevisionId untouched.
	var s3Code []byte
	if req.S3Bucket != "" {
		if h.s3Fetch == nil {
			protocol.NotImplementedJSON(w, r)
			return
		}
		zip, err := h.s3Fetch(ctx, req.S3Bucket, req.S3Key, req.S3ObjectVersion)
		if err != nil {
			protocol.WriteJSONError(w, r, lambdaS3CodeFetchError(err))
			return
		}
		s3Code = zip
	}

	var previousIdentity string
	fn, changed, aerr := h.ls.mutateFunction(ctx, name, func(current *Function) (bool, *protocol.AWSError) {
		previousIdentity = functionInstanceIdentity(current)
		// PackageType is immutable. Within that type, a successful update
		// replaces the previous source instead of layering fields onto it.
		switch current.PackageType {
		case "Image":
			current.setCode(nil)
			current.CodeS3Bucket = ""
			current.CodeS3Key = ""
			current.CodeS3ObjectVersion = ""
			current.ImageUri = req.ImageUri
		default:
			current.ImageUri = ""
			if len(req.ZipFile) > 0 {
				current.setCode(req.ZipFile)
				current.CodeS3Bucket = ""
				current.CodeS3Key = ""
				current.CodeS3ObjectVersion = ""
			} else {
				current.setCode(s3Code)
				current.CodeS3Bucket = req.S3Bucket
				current.CodeS3Key = req.S3Key
				current.CodeS3ObjectVersion = req.S3ObjectVersion
			}
		}
		current.SourceCode = ""
		current.SourceFilename = ""
		if len(req.Architectures) > 0 {
			current.Architectures = req.Architectures
		}
		current.RevisionId = uuid.NewString()
		current.LastModified = h.clk.Now().UTC().Format(time.RFC3339)
		return true, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !changed || fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
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

func validateUpdateFunctionCodeSource(packageType string, req updateFunctionCodeRequest) (*protocol.AWSError, bool) {
	hasZip := len(req.ZipFile) > 0
	hasS3Bucket := req.S3Bucket != ""
	hasS3Key := req.S3Key != ""
	hasImage := req.ImageUri != ""

	if packageType == "Image" {
		if hasImage && (hasZip || hasS3Bucket || hasS3Key) {
			return nil, true
		}
		if !hasImage {
			return lambdaInvalidParameter("Please provide ImageUri when PackageType is Image."), false
		}
		return nil, false
	}

	if hasImage {
		return nil, true
	}
	if hasZip && (hasS3Bucket || hasS3Key) {
		return nil, true
	}
	if hasS3Bucket != hasS3Key {
		return nil, true
	}
	if !hasZip && !hasS3Bucket {
		return lambdaInvalidParameter("Please provide a source for function code."), false
	}
	return nil, false
}

func lambdaS3CodeFetchError(s3Err *protocol.AWSError) *protocol.AWSError {
	if s3Err.Code == protocol.ErrInternalError.Code {
		return protocol.Wrap(protocol.ErrInternalError, s3Err)
	}
	return &protocol.AWSError{
		Code:       "InvalidParameterValueException",
		Message:    "Error occurred while GetObject. S3 Error Code: " + s3Err.Code + ". S3 Error Message: " + s3Err.Message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// UpdateFunctionConfiguration handles PUT /2015-03-31/functions/{name}/configuration.
func (h *Handler) UpdateFunctionConfiguration(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("update function configuration", zap.String("function", name))
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

	// unsupportedMembers lists what stays 501 here, and why each one does.
	if req.unsupportedMembers().requested() {
		protocol.NotImplementedJSON(w, r)
		return
	}
	if aerr := validateAdvancedConfiguration(req.TracingConfig, req.EphemeralStorage, req.KMSKeyArn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	loggingConfig, aerr := normalizeLoggingConfig(req.LoggingConfig, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// An explicitly present empty object still has uncaptured service semantics
	// — AWS's behaviour for it has never been observed on the wire here, and
	// guessing between "no-op" and "reset to defaults" would silently mutate a
	// function either way. JSON logging itself is supported; see logging_json.go.
	loggingUnsupported := req.LoggingConfig.empty()
	if req.Runtime != nil {
		if aerr := validateLambdaRuntime(*req.Runtime); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		// AWS's third deprecation phase blocks updates. It lands after
		// block-create by design: between the two, functions on a runtime that
		// can no longer be created can still be reconfigured.
		if runtimeUpdateBlocked(*req.Runtime, h.clk.Now()) {
			protocol.WriteJSONError(w, r, lambdaDeprecatedRuntimeError(*req.Runtime))
			return
		}
		if !lambdaRuntimeExecutionSupported(*req.Runtime) {
			protocol.NotImplementedJSON(w, r)
			return
		}
	}
	if req.Timeout != nil && (*req.Timeout < 1 || *req.Timeout > 900) {
		protocol.WriteJSONError(w, r, smithyIntegerConstraint("timeout", *req.Timeout, 1, 900))
		return
	}
	if req.MemorySize != nil && (*req.MemorySize < 128 || *req.MemorySize > 32768) {
		protocol.WriteJSONError(w, r, smithyIntegerConstraint("memorySize", *req.MemorySize, 128, 32768))
		return
	}
	if aerr := validateVpcConfig(req.VpcConfig); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateImageConfig(req.ImageConfig); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateFileSystemConfigs(req.FileSystemConfigs); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if req.Handler != nil && *req.Handler == "" {
		protocol.WriteJSONError(w, r, smithyPatternConstraint("handler", *req.Handler, `[^\s]+`))
		return
	}
	if req.Role != nil && *req.Role == "" {
		protocol.WriteJSONError(w, r, smithyPatternConstraint("role", *req.Role, `arn:(aws[a-zA-Z-]*)?:iam::\d{12}:role/?[a-zA-Z_0-9+=,.@\-_/]+`))
		return
	}
	if req.Layers != nil {
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
		}
	}
	if loggingUnsupported {
		// Defer the honest emulator gap until all modeled validation has
		// passed, then reject before any state mutation. Tracked in #660.
		protocol.NotImplementedJSON(w, r)
		return
	}
	// Resolve referenced resources before taking the function mutation lock.
	// The callback below is deliberately store-only and cannot re-enter lambdaStore.
	var layerLinks []LayerVersionLink
	if req.Layers != nil {
		layerLinks = make([]LayerVersionLink, 0, len(req.Layers))
		for _, arn := range req.Layers {
			lv, aerr := h.ls.getLayerVersionByARN(ctx, arn)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			if lv != nil {
				layerLinks = append(layerLinks, LayerVersionLink{ARN: arn, CodeSize: lv.CodeSize})
			} else {
				layerLinks = append(layerLinks, LayerVersionLink{ARN: arn})
			}
		}
	}
	var resolvedVpcID string
	if req.VpcConfig != nil {
		if resolver := h.getVPCResolver(); resolver != nil && len(req.VpcConfig.SubnetIds) > 0 {
			resolvedVpcID = resolver.VpcIDForSubnet(ctx, req.VpcConfig.SubnetIds[0])
		}
	}

	var previousIdentity string
	fn, changed, aerr := h.ls.mutateFunction(ctx, name, func(current *Function) (bool, *protocol.AWSError) {
		previousIdentity = functionInstanceIdentity(current)
		// Pointer members distinguish omission (no change) from explicit zero values.
		if req.Description != nil {
			current.Description = *req.Description
		}
		if req.Handler != nil {
			current.Handler = *req.Handler
		}
		if req.Role != nil {
			current.Role = *req.Role
		}
		if req.Timeout != nil {
			current.Timeout = *req.Timeout
		}
		if req.MemorySize != nil {
			current.MemorySize = *req.MemorySize
		}
		if req.Runtime != nil {
			current.Runtime = *req.Runtime
		}
		if req.Environment != nil {
			current.Environment = req.Environment.Variables
		}
		if req.LoggingConfig != nil {
			applyLoggingConfig(current, loggingConfig)
		}
		applyAdvancedConfiguration(current, req.TracingConfig, req.EphemeralStorage, req.KMSKeyArn)
		if req.Layers != nil {
			current.Layers = layerLinks
		}
		if req.VpcConfig != nil {
			if len(req.VpcConfig.SubnetIds) == 0 && len(req.VpcConfig.SecurityGroupIds) == 0 && !req.VpcConfig.Ipv6AllowedForDualStack {
				current.VpcConfig = nil
			} else {
				current.VpcConfig = &VpcConfig{
					SubnetIds:               req.VpcConfig.SubnetIds,
					SecurityGroupIds:        req.VpcConfig.SecurityGroupIds,
					Ipv6AllowedForDualStack: req.VpcConfig.Ipv6AllowedForDualStack,
					VpcId:                   resolvedVpcID,
				}
			}
		}
		if req.FileSystemConfigs != nil {
			// An empty list clears the mounts; a populated one replaces them.
			if len(req.FileSystemConfigs) == 0 {
				current.FileSystemConfigs = nil
			} else {
				current.FileSystemConfigs = req.FileSystemConfigs
			}
		}
		if req.ImageConfig != nil {
			// An empty ImageConfig object clears all overrides.
			if len(req.ImageConfig.EntryPoint) == 0 && len(req.ImageConfig.Command) == 0 && req.ImageConfig.WorkingDirectory == "" {
				current.ImageConfig = nil
			} else {
				current.ImageConfig = &ImageConfig{
					EntryPoint:       req.ImageConfig.EntryPoint,
					Command:          req.ImageConfig.Command,
					WorkingDirectory: req.ImageConfig.WorkingDirectory,
				}
			}
		}
		current.RevisionId = uuid.NewString()
		current.LastModified = h.clk.Now().UTC().Format(time.RFC3339)
		return true, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !changed || fn == nil {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(name))
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

// DeleteFunction handles DELETE /2015-03-31/functions/{name}[?Qualifier=].
//
// AWS: "Deletes a Lambda function. To delete a specific function version, use
// the Qualifier parameter. Otherwise, all versions and aliases are deleted."
// (API_DeleteFunction.html). The qualifier may equally arrive as part of the
// NamespacedFunctionName path label — "my-function:1".
func (h *Handler) DeleteFunction(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	identifier := chi.URLParam(r, "name")
	name, qualifier := splitFunctionIdentifier(identifier, r.URL.Query().Get("Qualifier"))
	log.Debug("delete function", zap.String("function", name), zap.String("qualifier", qualifier))
	ctx := r.Context()

	// NumericLatestPublishedOrAliasQualifier, the shape DeleteFunctionRequest's
	// Qualifier targets in the pinned Lambda model.
	if qualifier != "" {
		if len(qualifier) > 128 {
			protocol.WriteJSONError(w, r, smithyStringLengthConstraint("qualifier", qualifier, 128))
			return
		}
		if !qualifierPattern.MatchString(qualifier) {
			protocol.WriteJSONError(w, r, smithyPatternConstraint("qualifier", qualifier, qualifierConstraint))
			return
		}
	}

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(qualifiedResourceName(name, qualifier)))
		return
	}

	if qualifier != "" {
		h.deleteFunctionVersion(w, r, fn, qualifier)
		return
	}

	if aerr := h.ls.deleteFunction(ctx, name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.forgetS3SyncFunction(fn)

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

// deleteFunctionVersion removes a single published version, leaving $LATEST,
// the function record, its other versions, its aliases and every unrelated
// policy in place.
func (h *Handler) deleteFunctionVersion(w http.ResponseWriter, r *http.Request, fn *Function, qualifier string) {
	ctx := r.Context()

	// AWS's Qualifier "can specify a function version, but not the $LATEST
	// version" — the unpublished version only goes when the function does.
	if qualifier == "$LATEST" || qualifier == "$LATEST.PUBLISHED" {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("$LATEST version cannot be deleted without deleting the function."))
		return
	}

	version, err := strconv.Atoi(qualifier)
	if err != nil {
		// Not a version number. DeleteFunction's Qualifier names a version to
		// delete, and an alias only ever names a version it references — which
		// AWS refuses to delete. So a live alias is a conflict, and a name that
		// is neither a version nor an alias is simply absent.
		alias, aerr := h.ls.getAlias(ctx, fn.Name, qualifier)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if alias == nil {
			protocol.WriteJSONError(w, r, lambdaFunctionNotFound(qualifiedResourceName(fn.Name, qualifier)))
			return
		}
		protocol.WriteJSONError(w, r, lambdaVersionReferencedByAliases([]string{alias.Name}))
		return
	}

	versions, aerr := h.ls.listVersions(ctx, fn.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	published := false
	for _, candidate := range versions {
		if candidate.Version == version {
			published = true
			break
		}
	}
	if !published {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(qualifiedResourceName(fn.Name, qualifier)))
		return
	}

	// AWS: "You can't delete a version that an alias references."
	aliases, aerr := h.ls.listAliases(ctx, fn.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	var referencing []string
	for _, alias := range aliases {
		if alias.FunctionVersion == strconv.Itoa(version) {
			referencing = append(referencing, alias.Name)
		}
	}
	if len(referencing) > 0 {
		sort.Strings(referencing)
		protocol.WriteJSONError(w, r, lambdaVersionReferencedByAliases(referencing))
		return
	}

	if aerr := h.ls.deleteVersion(ctx, fn.Name, version); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// qualifiedResourceName renders a function reference the way AWS's messages do:
// "my-function" unqualified, "my-function:1" with a qualifier.
func qualifiedResourceName(name, qualifier string) string {
	if qualifier == "" {
		return name
	}
	return name + ":" + qualifier
}

func lambdaVersionReferencedByAliases(aliases []string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceConflictException",
		Message:    "Unable to delete version because the following aliases reference it: [" + strings.Join(aliases, ", ") + "]",
		HTTPStatus: http.StatusConflict,
	}
}

// ─── Invoke ───────────────────────────────────────────────────────────────────

// InvokeFunction handles POST /2015-03-31/functions/{name}/invocations.
// https://docs.aws.amazon.com/lambda/latest/api/API_Invoke.html
func (h *Handler) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
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
	log.Debug("invoke function", zap.String("function", name),
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
		log.Error("invoke: read payload", zap.String("function", name), zap.Error(err))
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidRequestContentException", Message: "Could not read request body.", HTTPStatus: http.StatusBadRequest})
		return
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}

	// Record invocation.
	if err := h.ls.addInvocation(ctx, fn, payload); err != nil {
		log.Warn("invoke: record invocation", zap.String("function", name), zap.Error(err))
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
		log.Warn("invoke: no runtime available", zap.String("function", name), zap.String("runtime", fn.Runtime))
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
	log := h.log.WithRecorder(ctx)
	for _, layer := range fn.Layers {
		arn := strings.TrimSpace(layer.ARN)
		if arn == "" {
			continue
		}
		lv, aerr := h.getLayerVersionByARNOrCachedExternal(ctx, arn)
		if aerr != nil {
			log.Error("invoke: check layer version", zap.String("arn", arn), zap.Error(aerr))
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
	log := h.log.WithRecorder(ctx)
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
		log.Warn("invoke: Docker acquire failed, retrying",
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

// lambdaInitTimeout is how long an invocation waits for a cold start to reach
// its first GET /next. The Runtime API's registration wait is derived from it
// and must stay shorter — see registrationWaitFor.
func lambdaInitTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.LambdaInitTimeout > 0 {
		return cfg.LambdaInitTimeout
	}
	return defaultLambdaInitTimeout
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
	log := h.log.WithRecorder(ctx)
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
		log.Error("invoke: acquire instance", zap.String("function", name), zap.Error(err))
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
		log.Error("invoke: runtime init", zap.String("function", name), zap.Error(err))
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
			log.Debug("invoke: ensure log stream", zap.String("function", name), zap.Error(lsErr))
		}
	}

	// Bound the invocation by the function's configured timeout so that
	// context.getRemainingTimeInMillis() inside the function reflects the
	// real deadline, and so we kill the container if it overruns.
	invokeCtx, cancel := context.WithTimeout(ctx, functionTimeout(fn))
	defer cancel()

	log.Debug("invoke function: dispatching", zap.String("function", name), zap.Int("payload_bytes", len(payload)))
	result, invokeErr := inst.Invoke(invokeCtx, payload, opts)
	healthy := invokeErr == nil
	rt.Release(invokeCtx, inst, healthy)

	if invokeErr != nil {
		log.Error("invoke: execution error", zap.String("function", name), zap.Error(invokeErr))
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
	log := h.log.WithRecorder(ctx)
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
		log.Debug("invokeAsync: throttled, retrying",
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
		log := h.log.WithRecorder(context.Background())
		_ = log
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
	log := h.log.WithRecorder(r.Context())
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
		log.Warn("invoke-sse: record invocation", zap.String("function", name), zap.Error(err))
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
		log.Error("invoke-sse: acquire instance", zap.String("function", name), zap.Error(err))
		sendEvent("error", err.Error())
		return
	}
	inv.Bind(inst)
	inv.Ready()
	if err := h.awaitRuntimeReady(ctx, fn, rt, inst); err != nil {
		rt.Release(ctx, inst, false)
		inv.Abandon(err.Error())
		log.Error("invoke-sse: runtime init", zap.String("function", name), zap.Error(err))
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
			log.Debug("invoke-sse: ensure log stream", zap.String("function", name), zap.Error(lsErr))
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
		log.Error("invoke-sse: execution error", zap.String("function", name), zap.Error(invokeErr))
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
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	events, err := h.ls.listTestEvents(ctx, name)
	if err != nil {
		log.Error("list test events", zap.String("function", name), zap.Error(err))
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
	log := h.log.WithRecorder(r.Context())
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
		log.Error("put test event", zap.String("function", name), zap.String("event", eventName), zap.Error(err))
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
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	eventName := chi.URLParam(r, "eventName")
	ctx := r.Context()

	if err := h.ls.deleteTestEvent(ctx, name, eventName); err != nil {
		log.Error("delete test event", zap.String("function", name), zap.String("event", eventName), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
