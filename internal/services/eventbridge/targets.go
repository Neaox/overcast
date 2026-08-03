package eventbridge

// Target validation and payload shaping for rule targets.
//
// PutTargets validates every target synchronously, the way AWS does: a
// malformed ARN fails the whole call with a ValidationException, and a target
// this emulator cannot deliver to is returned in FailedEntries rather than
// stored. Accepting a target that would never fire is the failure mode issue
// #467 exists to remove, and docs/plans/full-emulation-priority.md §2.1
// forbids reintroducing it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/protocol"
)

// failedTargetEntry is AWS's PutTargets/RemoveTargets FailedEntries member.
type failedTargetEntry struct {
	TargetID     string `json:"TargetId" cbor:"TargetId"`
	ErrorCode    string `json:"ErrorCode" cbor:"ErrorCode"`
	ErrorMessage string `json:"ErrorMessage" cbor:"ErrorMessage"`
}

// unsupportedTargetCode is the error code reported for a well-formed target
// ARN naming a service Overcast has no sink for. It is deliberately not one of
// AWS's own codes: on real AWS every one of those targets works, so there is
// no AWS behaviour to imitate — only an honest refusal to accept a target that
// would silently never fire.
const unsupportedTargetCode = "UnsupportedTargetType"

// validateTargets checks every target in a PutTargets call.
//
// It returns the targets that may be stored and the entries AWS's response
// reports as failed. A non-nil *protocol.AWSError means the whole call is
// rejected, matching AWS's synchronous parameter validation.
func validateTargets(targets []ebTarget) (accepted []ebTarget, failed []failedTargetEntry, aerr *protocol.AWSError) {
	accepted = make([]ebTarget, 0, len(targets))
	for _, target := range targets {
		if target.ID == "" {
			return nil, nil, targetValidationError("Parameter(s) Target.Id is not valid.")
		}
		if err := validateTargetInput(target); err != nil {
			return nil, nil, targetValidationError(err.Error())
		}
		kind, err := eventtarget.Classify(target.ARN)
		if err == nil {
			target.kind = kind
			accepted = append(accepted, target)
			continue
		}
		var unsupported *eventtarget.UnsupportedKindError
		if errors.As(err, &unsupported) {
			failed = append(failed, failedTargetEntry{
				TargetID:  target.ID,
				ErrorCode: unsupportedTargetCode,
				ErrorMessage: fmt.Sprintf(
					"Overcast does not emulate EventBridge delivery to %s targets. "+
						"Supported target types: Lambda, SQS, SNS, Step Functions, Kinesis, Firehose, ECS.",
					unsupported.Service),
			})
			continue
		}
		// Malformed ARN — AWS rejects the whole request rather than the entry.
		return nil, nil, targetValidationError("Parameter(s) Target.Arn is not valid.")
	}
	return accepted, failed, nil
}

// validateTargetInput enforces AWS's rule that a target carries at most one of
// Input, InputPath and InputTransformer.
func validateTargetInput(target ebTarget) error {
	set := 0
	if target.Input != "" {
		set++
	}
	if target.InputPath != "" {
		set++
	}
	if target.InputTransformer != nil {
		set++
	}
	if set > 1 {
		return errors.New("Only one of Input, InputPath or InputTransformer may be specified for a target.")
	}
	if target.InputTransformer != nil && target.InputTransformer.InputTemplate == "" {
		return errors.New("InputTransformer requires InputTemplate.")
	}
	return nil
}

func targetValidationError(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// mergeTargets applies a PutTargets call to the rule's existing target list,
// replacing by target ID and preserving the order targets were added in so
// ListTargetsByRule is stable across calls.
func mergeTargets(existing, incoming []ebTarget) []ebTarget {
	merged := make([]ebTarget, len(existing))
	copy(merged, existing)
	for _, target := range incoming {
		replaced := false
		for i := range merged {
			if merged[i].ID == target.ID {
				merged[i] = target
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, target)
		}
	}
	return merged
}

// targetPayload renders the bytes a target receives, applying the target's
// input transformation to the event envelope first.
//
// Precedence matches AWS: a static Input wins, then InputPath selects a single
// node, then InputTransformer renders a template. With none set the target
// receives the whole event.
func targetPayload(rule ebRule, target ebTarget, event map[string]any) ([]byte, error) {
	switch {
	case target.Input != "":
		return []byte(target.Input), nil
	case target.InputPath != "":
		selected, ok := eventtarget.SelectPath(event, target.InputPath)
		if !ok {
			return nil, fmt.Errorf("InputPath %q selected nothing in the event", target.InputPath)
		}
		return json.Marshal(selected)
	case target.InputTransformer != nil:
		it := eventtarget.InputTransformer{
			InputPathsMap: target.InputTransformer.InputPathsMap,
			InputTemplate: target.InputTransformer.InputTemplate,
		}
		return it.Render(event, reservedTemplateVars(rule, event))
	default:
		return json.Marshal(event)
	}
}

// reservedTemplateVars supplies EventBridge's built-in InputTransformer
// variables. `aws.events.event.json` is the whole event; the rule variables
// name the rule that matched.
func reservedTemplateVars(rule ebRule, event map[string]any) map[string]string {
	vars := map[string]string{
		"aws.events.rule-name": rule.Name,
		"aws.events.rule-arn":  rule.ARN,
	}
	if raw, err := json.Marshal(event); err == nil {
		vars["aws.events.event.json"] = string(raw)
	}
	if t, ok := event["time"].(string); ok {
		vars["aws.events.event.ingestion-time"] = t
	}
	return vars
}

// partitionKey resolves the Kinesis record partition key for a target.
// KinesisParameters.PartitionKeyPath selects it from the event; without one,
// EventBridge falls back to the event ID.
func partitionKey(target ebTarget, event map[string]any) string {
	if target.KinesisParams != nil {
		if path := stringValue(target.KinesisParams, "PartitionKeyPath"); path != "" {
			if v, ok := eventtarget.SelectPath(event, path); ok {
				if s, ok := v.(string); ok {
					return s
				}
				return fmt.Sprint(v)
			}
		}
	}
	return stringValue(event, "id")
}
