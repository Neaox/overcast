package serviceutil

import (
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/your-org/overcast/internal/protocol"
)

// ---- S3 validation ---------------------------------------------------------

var (
	// validBucketName matches a valid S3 bucket name.
	// Rules: 3–63 chars, lowercase letters/numbers/hyphens, no consecutive hyphens,
	// must start and end with letter or number, must not look like an IP address.
	// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
	validBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,61}[a-z0-9]$`)
	ipAddress       = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
)

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
	if len(name) == 0 || len(name) > 80 {
		return &protocol.AWSError{
			Code:       "InvalidParameterValue",
			Message:    "Queue name must be between 1 and 80 characters.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	for _, c := range name {
		if !isAlphanumeric(c) && c != '-' && c != '_' && c != '.' {
			return &protocol.AWSError{
				Code:       "InvalidParameterValue",
				Message:    "Queue name can only contain alphanumeric characters, hyphens, underscores, and periods.",
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}
	return nil
}

// ---- DynamoDB validation ---------------------------------------------------

// TableName validates a DynamoDB table name.
// Rules: 3–255 chars, alphanumeric + hyphens + underscores + periods.
// https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/HowItWorks.NamingRulesDataTypes.html
func TableName(name string) *protocol.AWSError {
	if len(name) < 3 || utf8.RuneCountInString(name) > 255 {
		return &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Table name must be between 3 and 255 characters.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	for _, c := range name {
		if !isAlphanumeric(c) && c != '-' && c != '_' && c != '.' {
			return &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "Table name can only contain alphanumeric characters, hyphens, underscores, and periods.",
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}
	return nil
}

// ---- Helpers ----------------------------------------------------------------

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
