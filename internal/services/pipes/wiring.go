package pipes

// Source / enrichment / target classification, and the synchronous validation
// CreatePipe and UpdatePipe run before a pipe is stored.
//
// Until issue #470 a pipe accepted any pair of ARNs and only the DynamoDB
// Streams → SQS path did anything; everything else was stored and silently
// inert — the exact failure mode docs/plans/full-emulation-priority.md §2.1
// forbids. Every combination this emulator cannot run is now refused at
// CreatePipe/UpdatePipe time with a ValidationException naming the field, the
// same treatment EventBridge's PutTargets gives an undeliverable rule target
// (issue #467).

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/protocol"
)

// sourceKind is the kind of source a pipe polls or subscribes to.
type sourceKind string

const (
	sourceDynamoDBStream sourceKind = "dynamodb"
	sourceKinesisStream  sourceKind = "kinesis"
	sourceSQSQueue       sourceKind = "sqs"
)

// sourceDisplayNames map each supported source to the label AWS uses for it.
var sourceDisplayNames = map[sourceKind]string{
	sourceDynamoDBStream: "DynamoDB Streams",
	sourceKinesisStream:  "Kinesis",
	sourceSQSQueue:       "SQS",
}

// supportedSourceList and supportedTargetList are quoted verbatim in rejection
// messages, so a user is told what they can use rather than only what they
// cannot.
const (
	supportedSourceList = "DynamoDB Streams, Kinesis streams and SQS queues"
	supportedTargetList = "Lambda, SQS, SNS, Step Functions, Kinesis, Firehose and EventBridge event buses"
)

// classifySource resolves a source ARN to the kind that can read it.
func classifySource(arn string) (sourceKind, error) {
	kind, err := eventtarget.Classify(arn)
	if err != nil {
		var unsupported *eventtarget.UnsupportedKindError
		if errors.As(err, &unsupported) {
			return "", unsupportedSourceError(unsupported.Service)
		}
		return "", err
	}
	switch kind {
	case eventtarget.KindKinesis:
		return sourceKinesisStream, nil
	case eventtarget.KindSQS:
		return sourceSQSQueue, nil
	case eventtarget.KindLambda, eventtarget.KindSNS, eventtarget.KindStepFunctions,
		eventtarget.KindFirehose, eventtarget.KindECS, eventtarget.KindEventBus:
		return "", unsupportedSourceError(string(kind))
	}
	return "", unsupportedSourceError(string(kind))
}

// classifySourceARN resolves a source ARN including the DynamoDB stream form,
// which eventtarget does not classify because nothing delivers *to* a stream.
func classifySourceARN(arn string) (sourceKind, error) {
	if svc := arnService(arn); svc == "dynamodb" {
		if tableNameFromStreamARN(arn) == "" {
			return "", fmt.Errorf("a DynamoDB source must be a stream ARN (arn:aws:dynamodb:…:table/NAME/stream/LABEL)")
		}
		return sourceDynamoDBStream, nil
	}
	return classifySource(arn)
}

// arnService returns an ARN's service segment, or "" when the value is not
// ARN-shaped.
func arnService(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return ""
	}
	return parts[2]
}

func unsupportedSourceError(service string) error {
	return fmt.Errorf("Overcast does not emulate EventBridge Pipes sources of type %s. Supported sources: %s.", service, supportedSourceList)
}

// classifyTarget resolves a target ARN to the sink that can receive a batch.
//
// It reuses eventtarget.Classify — the seam issue #467 built — and narrows it
// to what Pipes itself can drive: an ECS task target needs EcsTaskParameters
// this emulator does not build, so it is refused rather than accepted and
// dropped.
func classifyTarget(arn string) (eventtarget.Kind, error) {
	kind, err := eventtarget.Classify(arn)
	if err != nil {
		var unsupported *eventtarget.UnsupportedKindError
		if errors.As(err, &unsupported) {
			return "", unsupportedTargetError(unsupported.Service)
		}
		return "", err
	}
	if kind == eventtarget.KindECS {
		return "", unsupportedTargetError(string(kind))
	}
	return kind, nil
}

func unsupportedTargetError(service string) error {
	return fmt.Errorf("Overcast does not emulate EventBridge Pipes delivery to %s targets. Supported targets: %s.", service, supportedTargetList)
}

// classifyEnrichment resolves an enrichment ARN. AWS also allows API
// destinations, API Gateway and Express state machines; this emulator can only
// invoke Lambda synchronously and return the result, so the rest are refused.
func classifyEnrichment(arn string) (eventtarget.Kind, error) {
	kind, err := eventtarget.Classify(arn)
	if err != nil {
		var unsupported *eventtarget.UnsupportedKindError
		if errors.As(err, &unsupported) {
			return "", unsupportedEnrichmentError(unsupported.Service)
		}
		return "", err
	}
	if kind != eventtarget.KindLambda {
		return "", unsupportedEnrichmentError(string(kind))
	}
	return kind, nil
}

func unsupportedEnrichmentError(service string) error {
	return fmt.Errorf("Overcast does not emulate EventBridge Pipes enrichment through %s. Only Lambda function enrichments are supported.", service)
}

// validate checks everything about a pipe that AWS checks synchronously, plus
// everything this emulator would have to silently ignore.
//
// The returned error is AWS's ValidationException. Validation order matches
// AWS's: required members first, then each ARN, then the parameter blocks that
// belong to them.
func validatePipe(p *Pipe) *protocol.AWSError {
	if strings.TrimSpace(p.RoleArn) == "" {
		return validationError("RoleArn", "RoleArn is required.")
	}
	if _, err := classifySourceARN(p.SourceArn); err != nil {
		return validationError("Source", err.Error())
	}
	if p.Enrichment != "" {
		if _, err := classifyEnrichment(p.Enrichment); err != nil {
			return validationError("Enrichment", err.Error())
		}
	}
	if _, err := classifyTarget(p.TargetArn); err != nil {
		return validationError("Target", err.Error())
	}
	if aerr := validateSourceParameters(p); aerr != nil {
		return aerr
	}
	if p.EnrichmentParameters != nil && len(p.EnrichmentParameters.HttpParameters) > 0 {
		return validationError("EnrichmentParameters.HttpParameters",
			"HttpParameters only applies to API destination and API Gateway enrichments, which Overcast does not emulate.")
	}
	return validateTargetParameters(p)
}

// validateSourceParameters refuses the source configuration this emulator would
// otherwise store and ignore.
func validateSourceParameters(p *Pipe) *protocol.AWSError {
	sp := p.SourceParameters
	if sp == nil {
		return nil
	}
	if sp.FilterCriteria != nil && len(sp.FilterCriteria.Filters) > 0 {
		return validationError("SourceParameters.FilterCriteria",
			"Overcast does not evaluate EventBridge Pipes FilterCriteria. Remove the filter, or filter inside a Lambda enrichment instead.")
	}
	for field, raw := range map[string][]byte{
		"SourceParameters.ActiveMQBrokerParameters":        sp.ActiveMQBrokerParameters,
		"SourceParameters.RabbitMQBrokerParameters":        sp.RabbitMQBrokerParameters,
		"SourceParameters.ManagedStreamingKafkaParameters": sp.ManagedStreamingKafkaParameters,
		"SourceParameters.SelfManagedKafkaParameters":      sp.SelfManagedKafkaParameters,
	} {
		if len(raw) > 0 {
			return validationError(field, fmt.Sprintf("Overcast does not emulate that source type. Supported sources: %s.", supportedSourceList))
		}
	}
	return nil
}

// validateTargetParameters refuses target configuration with no implementation.
func validateTargetParameters(p *Pipe) *protocol.AWSError {
	tp := p.TargetParameters
	if tp == nil {
		return nil
	}
	for field, raw := range map[string][]byte{
		"TargetParameters.HttpParameters":              tp.HttpParameters,
		"TargetParameters.EcsTaskParameters":           tp.EcsTaskParameters,
		"TargetParameters.BatchJobParameters":          tp.BatchJobParameters,
		"TargetParameters.CloudWatchLogsParameters":    tp.CloudWatchLogsParameters,
		"TargetParameters.RedshiftDataParameters":      tp.RedshiftDataParameters,
		"TargetParameters.SageMakerPipelineParameters": tp.SageMakerPipelineParameters,
		"TargetParameters.TimestreamParameters":        tp.TimestreamParameters,
	} {
		if len(raw) > 0 {
			return validationError(field, fmt.Sprintf("Overcast does not emulate that target type. Supported targets: %s.", supportedTargetList))
		}
	}
	for field, params := range map[string]*InvocationTypeParameters{
		"TargetParameters.LambdaFunctionParameters.InvocationType":           tp.LambdaFunctionParameters,
		"TargetParameters.StepFunctionStateMachineParameters.InvocationType": tp.StepFunctionStateMachineParameters,
	} {
		if params == nil || params.InvocationType == "" {
			continue
		}
		switch params.InvocationType {
		case invocationRequestResponse, invocationFireAndForget:
		default:
			return validationError(field, "InvocationType must be REQUEST_RESPONSE or FIRE_AND_FORGET.")
		}
	}
	return nil
}

// AWS's PipeTargetInvocationType members.
const (
	invocationRequestResponse = "REQUEST_RESPONSE"
	invocationFireAndForget   = "FIRE_AND_FORGET"
)

// validationError builds AWS's Pipes ValidationException. The offending field
// is named in the message because Overcast's JSON error writer carries a code
// and a message only, not AWS's fieldList member.
func validationError(field, message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    fmt.Sprintf("%s: %s", field, message),
		HTTPStatus: http.StatusBadRequest,
	}
}

// sourceDisplayName names a resolved source for the console.
func sourceDisplayName(k sourceKind) string {
	if name, ok := sourceDisplayNames[k]; ok {
		return name
	}
	return "Unknown"
}
