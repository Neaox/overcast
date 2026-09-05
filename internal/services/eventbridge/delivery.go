package eventbridge

// Rule matching and target fan-out for PutEvents and the schedule engine.
//
// Delivery goes through internal/eventtarget, which dispatches to each sink
// over the emulator's own root router. Every target type EventBridge accepts
// is delivered here — a target that cannot be delivered to never reaches this
// file, because PutTargets rejects it (see targets.go).
//
// Hot-path notes: PutEvents walks every rule on the bus for every entry, so
// rules and their targets are read once per call rather than once per entry,
// and compiled event patterns are cached across calls. Delivery itself is
// synchronous — the emulator has no queue behind PutEvents, and spawning a
// goroutine per target would leak work past shutdown for no fidelity gain.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/eventtarget"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// maxDeliveryAttempts caps how many times one target delivery is attempted,
// however large a RetryPolicy asks for. Real EventBridge retries for up to 24
// hours with exponential backoff; an emulator serving a synchronous PutEvents
// has nowhere to put that wait, so the configured attempt count is honoured up
// to this ceiling and the event is dead-lettered or dropped after it.
const maxDeliveryAttempts = 6

// deliverEntries fans a batch of PutEvents entries out to matching rules.
//
// Rules are loaded once per distinct bus in the batch and targets once per
// matched rule, so a batch of N entries against a bus with R rules costs one
// scan rather than N.
func (s *Service) deliverEntries(ctx context.Context, eventIDs []string, entries []map[string]any) {
	log := s.log.WithRecorder(ctx)

	if len(entries) == 0 {
		return
	}
	rulesByBus := make(map[string][]ebRule, 1)
	targetsByRule := make(map[string][]ebTarget, 1)

	for i, entry := range entries {
		busName := stringValue(entry, "EventBusName")
		if busName == "" {
			busName = "default"
		}
		rules, loaded := rulesByBus[busName]
		if !loaded {
			var err error
			rules, err = s.loadRules(ctx, busName)
			if err != nil {
				log.Error("eventbridge: deliver: scan rules", zap.String("bus", busName), zap.Error(err))
				rules = nil
			}
			rulesByBus[busName] = rules
		}
		if len(rules) == 0 {
			continue
		}

		event := s.putEventsEnvelope(ctx, eventIDs[i], entry)
		for _, rule := range rules {
			if rule.State == "DISABLED" || rule.EventPattern == "" {
				continue
			}
			if !s.patterns.matches(rule.EventPattern, event) {
				continue
			}
			cacheKey := busName + "/" + rule.Name
			targets, loaded := targetsByRule[cacheKey]
			if !loaded {
				var err error
				targets, err = s.loadTargets(ctx, busName, rule.Name)
				if err != nil {
					log.Error("eventbridge: deliver: get targets", zap.String("rule", rule.Name), zap.Error(err))
					targets = nil
				}
				targetsByRule[cacheKey] = targets
			}
			s.deliverToTargets(ctx, rule, targets, event)
		}
	}
}

func (s *Service) putEventsEnvelope(ctx context.Context, eventID string, entry map[string]any) map[string]any {
	detail := map[string]any{}
	if raw := stringValue(entry, "Detail"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &detail)
	}
	resources := []any{}
	if raw, ok := entry["Resources"].([]any); ok {
		resources = raw
	}
	return map[string]any{
		"version":     "0",
		"id":          eventID,
		"detail-type": stringValue(entry, "DetailType"),
		"source":      stringValue(entry, "Source"),
		"account":     s.cfg.AccountID,
		"time":        s.clk.Now().UTC().Format(time.RFC3339),
		"region":      s.region(ctx),
		"resources":   resources,
		"detail":      detail,
	}
}

// deliverTargets loads a rule's targets and delivers one event to each. The
// schedule engine uses it directly; PutEvents goes through deliverEntries,
// which reuses the loaded target list across a batch.
func (s *Service) deliverTargets(ctx context.Context, rule ebRule, event map[string]any) {
	log := s.log.WithRecorder(ctx)

	targets, err := s.loadTargets(ctx, rule.EventBusName, rule.Name)
	if err != nil {
		log.Error("eventbridge: deliver: get targets", zap.String("rule", rule.Name), zap.Error(err))
		return
	}
	s.deliverToTargets(ctx, rule, targets, event)
}

func (s *Service) deliverToTargets(ctx context.Context, rule ebRule, targets []ebTarget, event map[string]any) {
	for _, target := range targets {
		s.deliverTarget(ctx, rule, target, event)
	}
}

// deliverTarget renders the target's payload, attempts delivery (retrying as
// the target's RetryPolicy allows), and records the outcome.
func (s *Service) deliverTarget(ctx context.Context, rule ebRule, target ebTarget, event map[string]any) {
	log := s.log.WithRecorder(ctx)

	rec := deliveryRecord{
		Region:     s.region(ctx),
		Bus:        rule.EventBusName,
		Rule:       rule.Name,
		TargetID:   target.ID,
		TargetARN:  target.ARN,
		TargetType: target.displayType(),
		EventID:    stringValue(event, "id"),
	}

	payload, err := targetPayload(rule, target, event)
	if err != nil {
		rec.Outcome = outcomeDropped
		rec.Error = err.Error()
		log.Warn("eventbridge: target input transformation failed",
			zap.String("rule", rule.Name), zap.String("target", target.ID), zap.Error(err))
		s.recordDelivery(rec)
		return
	}

	attempts := deliveryAttempts(target)
	maxAge := maximumEventAge(target)
	started := s.clk.Now()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 && maxAge > 0 {
			if age := s.eventAge(event, started); age >= maxAge {
				lastErr = fmt.Errorf("stopped retrying after %s: the event exceeded the target's MaximumEventAgeInSeconds of %d: %w",
					age.Round(time.Second), int(maxAge.Seconds()), lastErr)
				break
			}
		}
		rec.Attempts = attempt
		lastErr = s.dispatchTarget(ctx, rule, target, event, payload)
		if lastErr == nil {
			rec.Outcome = outcomeDelivered
			if attempt > 1 {
				rec.Outcome = outcomeRetried
			}
			s.recordDelivery(rec)
			return
		}
	}

	rec.Error = lastErr.Error()
	if dlq := deadLetterARN(target); dlq != "" {
		if dlqErr := s.deadLetter(ctx, dlq, payload); dlqErr != nil {
			rec.Outcome = outcomeDropped
			rec.Error = fmt.Sprintf("%v; dead-letter delivery also failed: %v", lastErr, dlqErr)
			log.Warn("eventbridge: dead-letter delivery failed",
				zap.String("rule", rule.Name), zap.String("target", target.ID),
				zap.String("dlq", dlq), zap.Error(dlqErr))
		} else {
			rec.Outcome = outcomeDLQ
			log.Warn("eventbridge: target delivery dead-lettered",
				zap.String("rule", rule.Name), zap.String("target", target.ID),
				zap.String("arn", target.ARN), zap.Int("attempts", rec.Attempts), zap.Error(lastErr))
		}
		s.recordDelivery(rec)
		return
	}

	rec.Outcome = outcomeDropped
	log.Warn("eventbridge: target delivery failed",
		zap.String("rule", rule.Name), zap.String("target", target.ID),
		zap.String("arn", target.ARN), zap.Int("attempts", rec.Attempts), zap.Error(lastErr))
	s.recordDelivery(rec)
}

// dispatchTarget routes one attempt to the right sink. ECS targets keep their
// own RunTask body, built from the target's EcsParameters; everything else
// goes through the shared dispatcher.
func (s *Service) dispatchTarget(ctx context.Context, rule ebRule, target ebTarget, event map[string]any, payload []byte) error {
	dispatcher := s.dispatcher()
	if dispatcher == nil {
		return fmt.Errorf("target router is not configured")
	}
	kind := target.kind
	if kind == "" {
		resolved, err := eventtarget.Classify(target.ARN)
		if err != nil {
			return err
		}
		kind = resolved
	}
	if kind == eventtarget.KindECS {
		if len(target.ECSParams) == 0 {
			return fmt.Errorf("ECS target requires EcsParameters")
		}
		return dispatcher.InvokeJSONTarget(ctx, "AmazonEC2ContainerServiceV20141113.RunTask", ecsRunTaskBody(rule, target))
	}
	if kind == eventtarget.KindEventBus {
		return dispatcher.Deliver(ctx, busTargetRequest(target, event, payload))
	}
	if kind == eventtarget.KindLambda {
		if err := s.authorizeLambdaTarget(ctx, rule, target); err != nil {
			return err
		}
	}
	return dispatcher.Deliver(ctx, eventtarget.Request{
		ARN:            target.ARN,
		Kind:           kind,
		Payload:        payload,
		PartitionKey:   partitionKey(target, event),
		MessageGroupID: stringValue(target.SQSParams, "MessageGroupId"),
	})
}

// authorizeLambdaTarget checks the function's resource-based policy before a
// Lambda target is invoked, contributing the rule ARN as aws:SourceArn — the
// condition AWS's own documented statement for an EventBridge target carries.
//
// A target configured with a RoleARN is skipped: "For Lambda, Amazon SNS, and
// Amazon SQS targets, you can use either an IAM execution role or a
// resource-based policy… If no execution role is configured, EventBridge uses
// resource-based policies on the target resource." Overcast does not evaluate
// the role's own policies here, so a target that names one is taken at its word.
//
// Returning an error puts the delivery on EventBridge's existing failure path:
// it is retried per the target's RetryPolicy, then dead-lettered if the target
// has a DeadLetterConfig and dropped if it does not, and either way the outcome
// is recorded against the rule.
//
// https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-use-resource-based.html
func (s *Service) authorizeLambdaTarget(ctx context.Context, rule ebRule, target ebTarget) error {
	if s.lambdaAuth == nil || target.RoleARN != "" {
		return nil
	}
	if aerr := s.lambdaAuth.AuthorizeServiceInvoke(ctx, target.ARN, eventbridgeServicePrincipal, rule.ARN, s.cfg.AccountID); aerr != nil {
		return fmt.Errorf("%s: %s", aerr.Code, aerr.Message)
	}
	return nil
}

// eventbridgeServicePrincipal is the principal an EventBridge rule target
// invokes a Lambda function as, and the value `add-permission --principal` is
// given for one.
const eventbridgeServicePrincipal = "events.amazonaws.com"

// busTargetRequest builds the delivery for an event-bus target.
//
// AWS republishes the event on the target bus rather than wrapping it: the
// PutEvents entry carries the original source, detail-type, resources and
// detail. Leaving Source and DetailType empty and putting the whole upstream
// envelope in Detail as a JSON string makes the target bus reachable only by a
// catch-all pattern — a downstream rule filtering on source or detail-type,
// which is virtually every real rule, could never match.
//
// A target that configures Input, InputPath or InputTransformer replaces the
// forwarded detail with the transformed payload, as AWS does; the routing
// fields still come from the event so the downstream rule can still match.
func busTargetRequest(target ebTarget, event map[string]any, payload []byte) eventtarget.Request {
	req := eventtarget.Request{
		ARN:        target.ARN,
		Kind:       eventtarget.KindEventBus,
		Payload:    payload,
		Source:     stringValue(event, "source"),
		DetailType: stringValue(event, "detail-type"),
		Resources:  stringSlice(event["resources"]),
	}
	if target.Input == "" && target.InputPath == "" && target.InputTransformer == nil {
		if detail, err := json.Marshal(event["detail"]); err == nil {
			req.Payload = detail
		}
	}
	return req
}

// stringSlice renders an event envelope's resources list as the []string a
// PutEvents entry needs.
func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

// deadLetter forwards an undeliverable payload to the target's dead-letter
// queue. EventBridge only supports SQS dead-letter queues.
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

// deliveryAttempts returns how many times a delivery is attempted in total:
// one, plus the target's MaximumRetryAttempts, capped at maxDeliveryAttempts.
func deliveryAttempts(target ebTarget) int {
	retries := intValue(target.RetryPolicy, "MaximumRetryAttempts")
	if retries < 0 {
		retries = 0
	}
	attempts := retries + 1
	if attempts > maxDeliveryAttempts {
		attempts = maxDeliveryAttempts
	}
	return attempts
}

// maximumEventAge returns how long the target keeps retrying an event before
// giving up on it, or 0 when the target sets no limit. AWS's field minimum is
// 60 seconds and its default is 24 hours.
func maximumEventAge(target ebTarget) time.Duration {
	seconds := intValue(target.RetryPolicy, "MaximumEventAgeInSeconds")
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// eventAge returns how long the event has been in flight, measured from the
// envelope's own timestamp — the same clock AWS ages an event against. An
// envelope with no parseable time falls back to when this delivery started.
func (s *Service) eventAge(event map[string]any, started time.Time) time.Duration {
	from := started
	if raw := stringValue(event, "time"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			from = t
		}
	}
	return s.clk.Now().Sub(from)
}

// deadLetterARN returns the target's configured dead-letter queue ARN, if any.
func deadLetterARN(target ebTarget) string {
	return stringValue(target.DLQConfig, "Arn")
}

func ecsRunTaskBody(rule ebRule, target ebTarget) map[string]any {
	params := target.ECSParams
	body := map[string]any{
		"cluster":    target.ARN,
		"launchType": stringValue(params, "LaunchType"),
		"group":      "eventbridge:" + rule.Name,
	}
	if taskDef := stringValue(params, "TaskDefinitionArn"); taskDef != "" {
		body["taskDefinition"] = taskDef
	}
	if platform := stringValue(params, "PlatformVersion"); platform != "" {
		body["platformVersion"] = platform
	}
	if count := intValue(params, "TaskCount"); count > 0 {
		body["count"] = count
	}
	if netCfg, ok := params["NetworkConfiguration"].(map[string]any); ok {
		body["networkConfiguration"] = lowerEventBridgeKeys(netCfg)
	}
	return body
}

func lowerEventBridgeKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			if k == "" {
				out[k] = lowerEventBridgeKeys(value)
				continue
			}
			lk := strings.ToLower(k[:1]) + k[1:]
			if k == "AwsVpcConfiguration" {
				lk = "awsvpcConfiguration"
			}
			out[lk] = lowerEventBridgeKeys(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = lowerEventBridgeKeys(value)
		}
		return out
	default:
		return v
	}
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func intValue(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(v)
		return i
	default:
		return 0
	}
}

// ruleKey builds the region-scoped store key for a rule's targets.
func ruleKey(region, bus, rule string) string {
	return serviceutil.RegionKey(region, bus+"/"+rule)
}
