package scheduler

// Target dispatch for the cron engine.
//
// Delivery goes through internal/eventtarget, the same seam EventBridge rules
// and Pipes deliver through, so a schedule and a rule pointed at one ARN reach
// the sink by exactly the same path — and a missing function, queue or stream
// produces that service's own AWS error rather than a silent no-op. A target
// kind the dispatcher cannot reach never gets here: CreateSchedule and
// UpdateSchedule refuse it (see validateTarget).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/protocol"
)

// maxDeliveryAttempts caps how many times one firing is attempted, however
// large a RetryPolicy asks for. Real EventBridge Scheduler retries up to 185
// times with backoff spread over MaximumEventAgeInSeconds; the emulator retries
// back to back on the delivery worker that owns the firing, with nowhere to put
// that wait, so the configured attempt count is honoured up to this ceiling and
// the payload is dead-lettered or dropped after it. EventBridge rules use the
// same ceiling.
const maxDeliveryAttempts = 6

// supportedTargetList is quoted verbatim in the rejection message, so a user is
// told what they can use rather than only what they cannot.
const supportedTargetList = "Lambda, SQS, SNS, Step Functions, Kinesis, Firehose, ECS and EventBridge event buses"

// validateTarget checks a schedule's target at create/update time.
//
// AWS accepts a target for any of the ~270 services its templated and universal
// targets cover, so a well-formed ARN naming a service this emulator has no
// sink for has no AWS behaviour to imitate — only an honest refusal, rather
// than accepting a schedule that would fire into nothing. That is the same
// choice EventBridge's PutTargets makes (see internal/services/eventbridge:
// unsupportedTargetCode) reported through the error shape Scheduler has:
// CreateSchedule has no per-entry failure list, so the whole call is rejected.
func validateTarget(target scheduleTarget) *protocol.AWSError {
	kind, err := eventtarget.Classify(target.Arn)
	if err != nil {
		var unsupported *eventtarget.UnsupportedKindError
		if errors.As(err, &unsupported) {
			return validationError(fmt.Sprintf(
				"Overcast does not emulate EventBridge Scheduler delivery to %s targets. Supported target types: %s.",
				unsupported.Service, supportedTargetList))
		}
		return validationError(fmt.Sprintf("Invalid Target.Arn: %q is not a valid ARN.", target.Arn))
	}
	// The two kinds whose delivery is built entirely from the target's own
	// parameters cannot fire without them, so they are refused here rather than
	// failing on every tick.
	if kind == eventtarget.KindECS && (target.EcsParameters == nil || strings.TrimSpace(target.EcsParameters.TaskDefinitionArn) == "") {
		return validationError("Invalid Target: an Amazon ECS target requires EcsParameters.TaskDefinitionArn.")
	}
	if kind == eventtarget.KindEventBus &&
		(target.EventBridgeParameters == nil || target.EventBridgeParameters.Source == "" || target.EventBridgeParameters.DetailType == "") {
		return validationError("Invalid Target: an EventBridge event bus target requires EventBridgeParameters.Source and EventBridgeParameters.DetailType.")
	}
	return nil
}

func validationError(message string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ValidationException", Message: message, HTTPStatus: http.StatusBadRequest}
}

// fire dispatches a schedule to its configured target, retrying and
// dead-lettering as the target asks. A firing that cannot be delivered is
// reported at ERROR level carrying the sink's own message — never dropped
// silently.
func (s *Service) fire(ctx context.Context, sc *Schedule, now time.Time) {
	target := sc.Target
	payload := []byte(target.Input)
	if target.Input == "" {
		// Default event payload.
		payload = fmt.Appendf(nil, `{"source":"aws.scheduler","time":%q,"id":%q}`,
			now.UTC().Format(time.RFC3339), uuid.NewString())
	}

	err := s.deliver(ctx, target, payload, now)
	if err == nil {
		return
	}

	if dlq := deadLetterARN(target); dlq != "" {
		if dlqErr := s.deadLetter(ctx, dlq, payload); dlqErr != nil {
			s.log.Error("scheduler: fire: dead-letter delivery failed",
				zap.String("schedule", sc.Name), zap.String("group", sc.GroupName),
				zap.String("target_arn", target.Arn), zap.String("dlq", dlq),
				zap.NamedError("delivery_error", err), zap.Error(dlqErr))
			return
		}
		s.log.Warn("scheduler: fire: target delivery dead-lettered",
			zap.String("schedule", sc.Name), zap.String("group", sc.GroupName),
			zap.String("target_arn", target.Arn), zap.String("dlq", dlq), zap.Error(err))
		return
	}

	s.log.Error("scheduler: fire: target delivery failed",
		zap.String("schedule", sc.Name), zap.String("group", sc.GroupName),
		zap.String("target_arn", target.Arn), zap.Error(err))
}

// deliver attempts one firing, retrying while the target's RetryPolicy allows
// it, and returns the last failure.
func (s *Service) deliver(ctx context.Context, target scheduleTarget, payload []byte, firedAt time.Time) error {
	dispatcher := s.dispatcher()
	if dispatcher == nil {
		return errors.New("target router is not configured")
	}
	kind, err := eventtarget.Classify(target.Arn)
	if err != nil {
		return err
	}

	attempts := deliveryAttempts(target)
	maxAge := maximumEventAge(target)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 && maxAge > 0 {
			if age := s.clk.Now().Sub(firedAt); age >= maxAge {
				return fmt.Errorf("stopped retrying after %s: the payload exceeded the target's MaximumEventAgeInSeconds of %d: %w",
					age.Round(time.Second), int(maxAge.Seconds()), lastErr)
			}
		}
		lastErr = s.dispatch(ctx, dispatcher, target, kind, payload)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// dispatch routes one attempt to the right sink. An ECS target keeps its own
// RunTask body, built from the schedule's EcsParameters; everything else goes
// through the shared dispatcher.
func (s *Service) dispatch(ctx context.Context, dispatcher *eventtarget.Dispatcher, target scheduleTarget, kind eventtarget.Kind, payload []byte) error {
	if kind == eventtarget.KindECS {
		if target.EcsParameters == nil {
			return errors.New("ECS target requires EcsParameters")
		}
		return dispatcher.InvokeJSONTarget(ctx, "AmazonEC2ContainerServiceV20141113.RunTask", ecsRunTaskBody(target))
	}
	req := eventtarget.Request{ARN: target.Arn, Kind: kind, Payload: payload}
	if p := target.SqsParameters; p != nil {
		req.MessageGroupID = p.MessageGroupId
	}
	if p := target.KinesisParameters; p != nil {
		req.PartitionKey = p.PartitionKey
	}
	if p := target.EventBridgeParameters; p != nil {
		req.Source = p.Source
		req.DetailType = p.DetailType
	}
	return dispatcher.Deliver(ctx, req)
}

// deadLetter forwards an undeliverable payload to the schedule's dead-letter
// queue. EventBridge Scheduler only supports SQS dead-letter queues.
func (s *Service) deadLetter(ctx context.Context, dlqARN string, payload []byte) error {
	kind, err := eventtarget.Classify(dlqARN)
	if err != nil {
		return err
	}
	if kind != eventtarget.KindSQS {
		return fmt.Errorf("dead-letter target %s is not an SQS queue", dlqARN)
	}
	return s.dispatcher().Deliver(ctx, eventtarget.Request{ARN: dlqARN, Kind: kind, Payload: payload})
}

// deliveryAttempts returns how many times a firing is attempted in total: one,
// plus the target's MaximumRetryAttempts, capped at maxDeliveryAttempts.
//
// AWS defaults MaximumRetryAttempts to 185 when the field is absent. The
// emulator treats an absent RetryPolicy as a single attempt instead: retries
// here run back to back with no backoff, so replaying AWS's default would hold
// a delivery worker on every undeliverable schedule to no fidelity gain.
// EventBridge rule targets read the field the same way.
func deliveryAttempts(target scheduleTarget) int {
	retries := 0
	if target.RetryPolicy != nil && target.RetryPolicy.MaximumRetryAttempts > 0 {
		retries = target.RetryPolicy.MaximumRetryAttempts
	}
	attempts := retries + 1
	if attempts > maxDeliveryAttempts {
		attempts = maxDeliveryAttempts
	}
	return attempts
}

// maximumEventAge returns how long the schedule keeps retrying a payload before
// giving up on it, or 0 when the target sets no limit. AWS's field minimum is
// 60 seconds and its default is 24 hours.
func maximumEventAge(target scheduleTarget) time.Duration {
	if target.RetryPolicy == nil || target.RetryPolicy.MaximumEventAgeInSeconds <= 0 {
		return 0
	}
	return time.Duration(target.RetryPolicy.MaximumEventAgeInSeconds) * time.Second
}

// deadLetterARN returns the schedule's configured dead-letter queue ARN, if any.
func deadLetterARN(target scheduleTarget) string {
	if target.DeadLetterConfig == nil {
		return ""
	}
	return strings.TrimSpace(target.DeadLetterConfig.Arn)
}

// ecsRunTaskBody renders a schedule's EcsParameters as an ECS RunTask request.
// The target ARN is the cluster, as it is for an EventBridge ECS target.
//
// The key case is ECS's own, not Scheduler's: the body is replayed against the
// RunTask handler, which reads the lowerCamelCase members of the ECS API.
func ecsRunTaskBody(target scheduleTarget) map[string]any {
	params := target.EcsParameters
	body := map[string]any{
		"cluster":        target.Arn,
		"taskDefinition": params.TaskDefinitionArn,
	}
	if params.LaunchType != "" {
		body["launchType"] = params.LaunchType
	}
	if params.PlatformVersion != "" {
		body["platformVersion"] = params.PlatformVersion
	}
	if params.Group != "" {
		body["group"] = params.Group
	}
	if params.TaskCount > 0 {
		body["count"] = params.TaskCount
	}
	if net := params.NetworkConfiguration; net != nil && net.AwsvpcConfiguration != nil {
		vpc := map[string]any{}
		if subnets := net.AwsvpcConfiguration.Subnets; len(subnets) > 0 {
			vpc["subnets"] = subnets
		}
		if groups := net.AwsvpcConfiguration.SecurityGroups; len(groups) > 0 {
			vpc["securityGroups"] = groups
		}
		if assign := net.AwsvpcConfiguration.AssignPublicIp; assign != "" {
			vpc["assignPublicIp"] = assign
		}
		body["networkConfiguration"] = map[string]any{"awsvpcConfiguration": vpc}
	}
	return body
}
