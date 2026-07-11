package serviceutil

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// ---- S3 validation ---------------------------------------------------------

var (
	// validBucketName matches a valid S3 bucket name.
	// Rules: 3–63 chars, lowercase letters/numbers/hyphens, no consecutive hyphens,
	// must start and end with letter or number, must not look like an IP address.
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	validBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,61}[a-z0-9]$`)
	ipAddress       = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

	// GraphQLIdentifierPattern matches AppSync GraphQL identifiers documented for
	// data source, function, type, and field names.
	// https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateDataSource.html
	GraphQLIdentifierPattern = regexp.MustCompile(`^[_A-Za-z][_0-9A-Za-z]*$`)
)

// NameRule describes one AWS resource-name validation rule. It intentionally
// carries service-specific error details because AWS does not use one global
// validation error shape across services.
type NameRule struct {
	MinLength      int
	MaxLength      int
	Allowed        func(rune) bool
	Pattern        *regexp.Regexp
	ErrorCode      string
	LengthMessage  string
	AllowedMessage string
	PatternMessage string
	HTTPStatus     int
}

// ResourceName validates name with a reusable rule and returns the configured
// AWS-style error. Service-specific validators should wrap this helper rather
// than exposing generic resource-name policy from handlers.
func ResourceName(name string, rule NameRule) *protocol.AWSError {
	if (rule.MinLength > 0 && len(name) < rule.MinLength) || (rule.MaxLength > 0 && len(name) > rule.MaxLength) {
		return nameError(rule, rule.LengthMessage)
	}
	if rule.Allowed != nil {
		for _, c := range name {
			if !rule.Allowed(c) {
				return nameError(rule, rule.AllowedMessage)
			}
		}
	}
	if rule.Pattern != nil && !rule.Pattern.MatchString(name) {
		return nameError(rule, rule.PatternMessage)
	}
	return nil
}

// AlphaNumericHyphenUnderscorePeriod matches the common AWS identifier alphabet
// used by SQS queue names and DynamoDB table names.
func AlphaNumericHyphenUnderscorePeriod(c rune) bool {
	return isAlphanumeric(c) || c == '-' || c == '_' || c == '.'
}

// BucketName validates an S3 bucket name against AWS naming rules.
// Returns nil if valid, or a *protocol.AWSError with code "InvalidBucketName".
func BucketName(name string) *protocol.AWSError {
	if len(name) < 3 || len(name) > 63 {
		return &protocol.AWSError{
			Code:       "InvalidBucketName",
			Message:    "The specified bucket name is not valid. Bucket names must be between 3 and 63 characters.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if !validBucketName.MatchString(name) {
		return &protocol.AWSError{
			Code:       "InvalidBucketName",
			Message:    "The specified bucket name is not valid. Bucket names can contain only lowercase letters, numbers, and hyphens.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if strings.Contains(name, "--") {
		return &protocol.AWSError{
			Code:       "InvalidBucketName",
			Message:    "The specified bucket name is not valid. Bucket names must not contain consecutive hyphens.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if ipAddress.MatchString(name) {
		return &protocol.AWSError{
			Code:       "InvalidBucketName",
			Message:    "The specified bucket name is not valid. Bucket names must not be formatted as an IP address.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

// ---- SQS validation --------------------------------------------------------

// QueueName validates an SQS queue name.
// Standard queues: alphanumeric + hyphens + underscores, 1–80 chars.
// FIFO queues: same rules + must end in .fifo.
// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-queue-message-identifiers.html
func QueueName(name string) *protocol.AWSError {
	return ResourceName(name, NameRule{
		MinLength:      1,
		MaxLength:      80,
		Allowed:        AlphaNumericHyphenUnderscorePeriod,
		ErrorCode:      "InvalidParameterValue",
		LengthMessage:  "Queue name must be between 1 and 80 characters.",
		AllowedMessage: "Queue name can only contain alphanumeric characters, hyphens, underscores, and periods.",
	})
}

// ---- DynamoDB validation ---------------------------------------------------

// TableName validates a DynamoDB table name.
// Rules: 3–255 chars, alphanumeric + hyphens + underscores + periods.
// https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/HowItWorks.NamingRulesDataTypes.html
func TableName(name string) *protocol.AWSError {
	return ResourceName(name, NameRule{
		MinLength:      3,
		MaxLength:      255,
		Allowed:        AlphaNumericHyphenUnderscorePeriod,
		ErrorCode:      "ValidationException",
		LengthMessage:  "Table name must be between 3 and 255 characters.",
		AllowedMessage: "Table name can only contain alphanumeric characters, hyphens, underscores, and periods.",
	})
}

// ---- AppSync validation -----------------------------------------------------

// AppSyncGraphQLAPIName validates the required GraphQL API name. AWS documents
// this field as required without publishing a stricter length or pattern.
func AppSyncGraphQLAPIName(name string) *protocol.AWSError {
	if name == "" {
		return &protocol.AWSError{Code: "BadRequestException", Message: "name is required", HTTPStatus: http.StatusBadRequest}
	}
	return nil
}

// AppSyncIdentifierName validates AppSync names documented with the
// [_A-Za-z][_0-9A-Za-z]* pattern and 1..65536 length constraint.
func AppSyncIdentifierName(name, field string) *protocol.AWSError {
	return ResourceName(name, NameRule{
		MinLength:      1,
		MaxLength:      65536,
		Pattern:        GraphQLIdentifierPattern,
		ErrorCode:      "BadRequestException",
		LengthMessage:  field + " must be between 1 and 65536 characters",
		PatternMessage: field + " must match pattern [_A-Za-z][_0-9A-Za-z]*",
	})
}

func AppSyncDataSourceName(name string) *protocol.AWSError {
	return AppSyncIdentifierName(name, "dataSourceName")
}

func AppSyncFunctionName(name string) *protocol.AWSError {
	return AppSyncIdentifierName(name, "name")
}

func AppSyncTypeName(name string) *protocol.AWSError {
	return AppSyncIdentifierName(name, "typeName")
}

func AppSyncFieldName(name string) *protocol.AWSError {
	return AppSyncIdentifierName(name, "fieldName")
}

// ---- Helpers ----------------------------------------------------------------

func nameError(rule NameRule, message string) *protocol.AWSError {
	status := rule.HTTPStatus
	if status == 0 {
		status = http.StatusBadRequest
	}
	return &protocol.AWSError{
		Code:       rule.ErrorCode,
		Message:    message,
		HTTPStatus: status,
	}
}

// ValidateAndRespond is a convenience helper that writes the error and returns
// false if aerr is non-nil, otherwise returns true. Reduces boilerplate in
// handlers that call multiple validators in sequence.
//
//	if !serviceutil.ValidateAndRespond(w, r, serviceutil.BucketName(bucket)) {
//	    return
//	}
func ValidateAndRespond(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) bool {
	if aerr != nil {
		// Use the appropriate format based on what content type the client expects.
		// S3 uses XML; most others use JSON. The caller should use the service-
		// appropriate helper directly when the format is known — this helper is for
		// cases where the service format is ambiguous (e.g. shared validators).
		// Default to JSON since it's used by more services.
		protocol.WriteJSONError(w, r, aerr)
		return false
	}
	return true
}

func isAlphanumeric(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
