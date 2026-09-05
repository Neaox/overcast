package cloudformation

// provisioner_properties.go — the shared answer to the property-drop class of
// bug catalogued in https://github.com/overcast-sh/overcast/issues/540.
//
// # The shape of the bug
//
// Almost every resource handler builds its request one property at a time:
//
//	if v, ok := props["X"].(string); ok && v != "" { body["x"] = v }
//
// That is an allow-list, and an allow-list drops everything nobody thought of
// — silently, with the stack still reporting CREATE_COMPLETE. #540 found the
// same drop in twenty-odd services, including three where the dropped property
// selected a Docker image, so the emulator started the wrong engine and looked
// like it had succeeded.
//
// # The decision, and why not a blind pass-through
//
// The tempting fix is to forward the whole property map and let the service
// reject what it does not know. CloudFront's DistributionConfig survives
// intact precisely because it does that. But it only works there because the
// CloudFormation property shape and the API's input shape are the same shape.
// In general they are not:
//
//   - Tags are `[{Key,Value}]` in most CloudFormation resources and a
//     `{key: value}` map in most APIs — and a map in a few CloudFormation
//     resources too (AWS::EKS::Cluster, AWS::MSK::Cluster), so neither
//     direction is safe to assume.
//   - Property names are PascalCase in the template and lowerCamel in every
//     restJson1 service, and the two do not always differ by only the first
//     letter.
//   - CloudFormation resources carry properties with no API member at all
//     (AWS::EKS::Nodegroup's `ForceUpdateEnabled`), which a pass-through would
//     hand to a service that has to ignore them.
//
// So the allow-list stays: it is explicit, it sits next to the handler where a
// reviewer can check it against the AWS docs, and CONTRIBUTING.md § Thin
// orchestration layer asks for exactly that. What changes is that the
// allow-list becomes *data* rather than a run of if-statements, and that the
// **leftovers stop being invisible**:
//
//   - forwardProperties declares the allow-list in one line, so adding a
//     property is one word rather than four lines of type assertion, and
//     nested key conversion is not something each handler re-derives.
//   - unconsumedProperties / noteUnconsumedProperties turn everything the
//     handler did *not* claim into an emulation limitation on the resource,
//     which CloudFormation already surfaces as ResourceStatusReason (see
//     limitation.go). A dropped property now shows up in `cdk deploy` output
//     beside the resource it was dropped from.
//
// A handler that adopts both cannot drop a property silently: either it is in
// the allow-list, or the user is told it was ignored. That is the structural
// half; the per-property judgement stays where it belongs.

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// forwardProperties copies the named CloudFormation properties onto an AWS
// request body, converting each name — and, recursively, the keys of any
// nested object — from the template's PascalCase to the lowerCamel every
// restJson1 and JSON-target service models. It is the declarative form of the
// `if v, ok := props["X"]; ok { body["x"] = convertCFKeysToAPI(v) }` run that
// every handler in this package used to spell out by hand.
//
// A property the template did not set is skipped, so an absent property never
// becomes an explicit null the service has to defend against. A property set
// to the JSON literal `null` is skipped for the same reason.
//
// It is deliberately not a validator: a property whose value has the wrong
// type goes to the service, which owns that error, exactly as
// CONTRIBUTING.md § Thin orchestration layer asks.
//
// Use forwardPropertiesAs for a service whose member names are not the
// template's names lower-cased (Query and REST-XML services, chiefly).
func forwardProperties(props, body map[string]any, names ...string) {
	for _, name := range names {
		v, ok := props[name]
		if !ok || v == nil {
			continue
		}
		body[toLowerCamelCase(name)] = convertCFKeysToAPI(v)
	}
}

// forwardPropertiesAs copies properties whose API member name is not the
// template name lower-cased, given as template-name → member-name pairs. The
// value is passed through untouched, since a service that renames its top-level
// members is unlikely to share the template's nested spelling either.
func forwardPropertiesAs(props, body map[string]any, names map[string]string) {
	for templateName, memberName := range names {
		v, ok := props[templateName]
		if !ok || v == nil {
			continue
		}
		body[memberName] = v
	}
}

// unconsumedProperties returns, sorted, the template properties the handler
// neither forwarded nor listed as deliberately handled. `consumed` is every
// name the handler acted on by any route — forwarded, read into a physical ID,
// dispatched as a separate call, or knowingly rejected.
//
// The empty result is the one a fully-covering handler produces, so a caller
// can branch on len() without a nil check.
func unconsumedProperties(props map[string]any, consumed ...string) []string {
	if len(props) == 0 {
		return nil
	}
	claimed := make(map[string]struct{}, len(consumed))
	for _, name := range consumed {
		claimed[name] = struct{}{}
	}
	var left []string
	for name := range props {
		if _, ok := claimed[name]; ok {
			continue
		}
		left = append(left, name)
	}
	sort.Strings(left)
	return left
}

// noteUnconsumedProperties records the properties resType's handler did not act
// on as an emulation limitation on the resource being provisioned, so they
// reach the user as its ResourceStatusReason instead of vanishing. See
// limitation.go for the channel and protocol/limitation.go for why a sentence
// beats a 501 here: the resource does exist, and failing the stack over a
// property Overcast will not act on trades a partial environment for none.
//
// A handler that covers every property it was given records nothing.
func noteUnconsumedProperties(ctx context.Context, resType string, props map[string]any, consumed ...string) {
	left := unconsumedProperties(props, consumed...)
	if len(left) == 0 {
		return
	}
	noteLimitation(ctx, fmt.Sprintf(
		"Overcast: %s properties not applied: %s. They were accepted and ignored.",
		resType, strings.Join(left, ", ")))
}

// cfnTagMap reads a resource's Tags property into the `{key: value}` map most
// APIs model, accepting both CloudFormation tag shapes: the usual
// `[{"Key":…,"Value":…}]` list, and the `{key: value}` object AWS::EKS::* and
// AWS::MSK::Cluster use instead. Returns nil for an absent or unrecognised
// value, so a caller can hand the result straight to mergeStackTags.
//
// Values are rendered with %v rather than asserted to string: a template may
// legitimately carry a number or a boolean there, and CloudFormation stringifies
// it rather than refusing.
func cfnTagMap(raw any) map[string]string {
	switch tags := raw.(type) {
	case map[string]any:
		out := make(map[string]string, len(tags))
		for key, value := range tags {
			if strings.TrimSpace(key) == "" {
				continue
			}
			out[key] = fmt.Sprintf("%v", value)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make(map[string]string, len(tags))
		for _, item := range tags {
			kv, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key, _ := kv["Key"].(string)
			if strings.TrimSpace(key) == "" {
				continue
			}
			out[key] = fmt.Sprintf("%v", kv["Value"])
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
