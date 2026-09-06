package sqs

// queueattrname.go holds the set of attribute names GetQueueAttributes
// accepts. AWS models it as the QueueAttributeName enum and rejects anything
// outside it with InvalidAttributeName, so a typo reads as "no such
// attribute" rather than silently as "that attribute is empty".
//
// The set is written out here rather than derived from the model at runtime —
// internal/awsmodel's snapshot is a generator input, not something service
// code reads (see queryerror.go for the same constraint) — and
// queueattrname_model_test.go checks it against the pinned model so the two
// cannot drift.

import (
	"net/http"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// queueAttributeNames is the QueueAttributeName enum, "All" included: AWS
// accepts the wildcard in the same list position as any other name.
var queueAttributeNames = map[string]struct{}{
	"All":                                   {},
	"ApproximateNumberOfMessages":           {},
	"ApproximateNumberOfMessagesDelayed":    {},
	"ApproximateNumberOfMessagesNotVisible": {},
	"ContentBasedDeduplication":             {},
	"CreatedTimestamp":                      {},
	"DeduplicationScope":                    {},
	"DelaySeconds":                          {},
	"FifoQueue":                             {},
	"FifoThroughputLimit":                   {},
	"KmsDataKeyReusePeriodSeconds":          {},
	"KmsMasterKeyId":                        {},
	"LastModifiedTimestamp":                 {},
	"MaximumMessageSize":                    {},
	"MessageRetentionPeriod":                {},
	"Policy":                                {},
	"QueueArn":                              {},
	"ReceiveMessageWaitTimeSeconds":         {},
	"RedriveAllowPolicy":                    {},
	"RedrivePolicy":                         {},
	"SqsManagedSseEnabled":                  {},
	"VisibilityTimeout":                     {},
}

// validateQueueAttributeNames rejects the first requested name AWS does not
// model. An empty list is fine: it means "the default set", not "no names".
func validateQueueAttributeNames(names []string) *protocol.AWSError {
	for _, name := range names {
		if _, ok := queueAttributeNames[name]; !ok {
			return errInvalidAttributeName(name)
		}
	}
	return nil
}

func errInvalidAttributeName(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidAttributeName",
		Message:    "Unknown Attribute " + name + ".",
		HTTPStatus: http.StatusBadRequest,
	}
}
