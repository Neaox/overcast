package cloudformation

// provisioner_lambda_gate_test.go — the forward-vs-gate guard.
//
// Two lists decide whether a Lambda-bearing template can deploy at all:
//
//   - the request members this provisioner forwards into each Lambda API call,
//   - the request members those Lambda handlers refuse with a 501.
//
// Their intersection is, by construction, a set of templates that can only ever
// fail. Nothing used to compare them, and that is how `Tags` on
// CreateEventSourceMapping shipped: every `cdk deploy --tags …` of an
// SQS→Lambda stack failed at CREATE, with no unusual template property in
// sight.
//
// The test derives both sides from the real code — the forwarded side by
// running the provisioner's own body builders, the gated side from
// lambda.UnsupportedRequestMembers, which is the table the handlers themselves
// test. Neither side is a hand-copied list that can drift.

import (
	"sort"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/services/lambda"
)

// reviewedGatedForwards is the intersection as it is *meant* to be: members the
// provisioner deliberately forwards to a handler that will refuse them, because
// an honest 501 naming an unimplemented feature beats silently dropping the
// property and deploying something the template did not describe.
//
// Every entry needs a reason, and the reason has to survive the question "would
// a real template hit this?". An entry whose answer is "yes, routinely" is a
// bug, not a policy — that is exactly what Tags was.
//
// The test fails in both directions: an unreviewed forward has to be added
// here (or removed from the provisioner), and an entry that is no longer in the
// intersection has to be deleted, so implementing a member cleans this list up
// rather than leaving a stale excuse behind.
var reviewedGatedForwards = map[string]map[string]string{
	lambda.OpCreateFunction: {
		"SnapStart":                "no snapshot lifecycle; accepting it would claim init-time savings that do not exist",
		"CapacityProviderConfig":   "Lambda-managed instances are not emulated",
		"DurableConfig":            "durable functions are not emulated",
		"TenancyConfig":            "tenant isolation is not emulated",
		"PublishTo":                "publishing a version as part of create is not emulated",
		"Code.SourceKMSKeyArn":     "deployment-package encryption is not emulated",
		"Code.S3ObjectStorageMode": "the storage mode of the deployment package is not emulated",
	},
	lambda.OpUpdateFunctionConfiguration: {
		"SnapStart":              "as CreateFunction",
		"CapacityProviderConfig": "as CreateFunction",
	},
	lambda.OpUpdateFunctionCode: {
		"PublishTo":           "as CreateFunction",
		"SourceKMSKeyArn":     "as CreateFunction's Code.SourceKMSKeyArn",
		"S3ObjectStorageMode": "as CreateFunction's Code.S3ObjectStorageMode",
	},
	lambda.OpCreateEventSourceMapping: {
		"FunctionResponseTypes":               "partial-batch failure reporting is behavioural: accepting it would delete messages that should be retried",
		"ParallelizationFactor":               "stream shard parallelism is not emulated",
		"StartingPositionTimestamp":           "AT_TIMESTAMP stream positioning is not emulated",
		"SourceAccessConfigurations":          "self-managed source authentication is not emulated",
		"SelfManagedEventSource":              "self-managed Kafka is not an emulated event source",
		"Topics":                              "Kafka topics are not an emulated event source",
		"Queues":                              "Amazon MQ queues are not an emulated event source",
		"KMSKeyArn":                           "filter-criteria encryption is not emulated",
		"MetricsConfig":                       "ESM CloudWatch metrics are not emitted",
		"ProvisionedPollerConfig":             "provisioned pollers are not emulated",
		"AmazonManagedKafkaEventSourceConfig": "MSK is not an emulated event source",
		"DocumentDBEventSourceConfig":         "DocumentDB is not an emulated event source",
		"LoggingConfig":                       "ESM logging configuration is not emulated",
		"SelfManagedKafkaEventSourceConfig":   "self-managed Kafka is not an emulated event source",
	},
	lambda.OpUpdateEventSourceMapping: {
		"FunctionResponseTypes":               "as CreateEventSourceMapping",
		"ParallelizationFactor":               "as CreateEventSourceMapping",
		"SourceAccessConfigurations":          "as CreateEventSourceMapping",
		"KMSKeyArn":                           "as CreateEventSourceMapping",
		"MetricsConfig":                       "as CreateEventSourceMapping",
		"ProvisionedPollerConfig":             "as CreateEventSourceMapping",
		"Topics":                              "as CreateEventSourceMapping",
		"Queues":                              "as CreateEventSourceMapping",
		"AmazonManagedKafkaEventSourceConfig": "as CreateEventSourceMapping",
		"DocumentDBEventSourceConfig":         "as CreateEventSourceMapping",
		"LoggingConfig":                       "as CreateEventSourceMapping",
		"SelfManagedKafkaEventSourceConfig":   "as CreateEventSourceMapping",
	},
}

// templatePropertyAliases are the template property names that reach a Lambda
// request member under a *different* name. Every plain copy is discovered by
// probing with the member's own name; these are the ones that are not plain.
var templatePropertyAliases = map[string][]string{
	"KMSKeyArn": {"KmsKeyArn"},
	"PublishTo": {"PublishToLatestPublished"},
}

func TestLambdaProvisionerForwardsOnlyReviewedGatedMembers(t *testing.T) {
	// Given: Lambda's own 501 gate, and the members the provisioner forwards
	// for each operation, both read from the shipping code.
	gated := lambda.UnsupportedRequestMembers()
	forwarded := lambdaForwardedRequestMembers(gated)

	for operation, gatedMembers := range gated {
		t.Run(operation, func(t *testing.T) {
			reviewed := reviewedGatedForwards[operation]

			// When: the two lists are intersected.
			var unreviewed []string
			for _, member := range gatedMembers {
				if !forwarded[operation][member] {
					continue
				}
				if _, ok := reviewed[member]; !ok {
					unreviewed = append(unreviewed, member)
				}
			}
			sort.Strings(unreviewed)

			// Then: nothing in it is a surprise. A member here is a template
			// property CloudFormation sends into a request that always 501s,
			// so every stack using it fails at CREATE or UPDATE.
			if len(unreviewed) > 0 {
				t.Errorf("provisioner forwards %s member(s) %s into a request Lambda always refuses with 501.\n"+
					"Either implement the member, stop forwarding it, or add it to reviewedGatedForwards with the reason a 501 is the right answer.",
					operation, strings.Join(unreviewed, ", "))
			}

			// And: no reason outlives the problem it describes. An entry that
			// is no longer forwarded, or no longer gated, is a stale excuse.
			var stale []string
			gatedSet := make(map[string]bool, len(gatedMembers))
			for _, member := range gatedMembers {
				gatedSet[member] = true
			}
			for member := range reviewed {
				if !gatedSet[member] {
					stale = append(stale, member+" (no longer refused by Lambda)")
					continue
				}
				if !forwarded[operation][member] {
					stale = append(stale, member+" (no longer forwarded)")
				}
			}
			sort.Strings(stale)
			if len(stale) > 0 {
				t.Errorf("reviewedGatedForwards[%s] has entries that no longer describe anything: %s",
					operation, strings.Join(stale, ", "))
			}
		})
	}

	// And: the review list names no operation the gate does not have, which
	// would otherwise silently stop being checked.
	for operation := range reviewedGatedForwards {
		if _, ok := gated[operation]; !ok {
			t.Errorf("reviewedGatedForwards names unknown Lambda operation %q", operation)
		}
	}
}

// lambdaForwardedRequestMembers reports, per Lambda operation, which of the
// gated members the provisioner is capable of forwarding. It probes the real
// body builders: every gated member name (and every alias it can arrive under)
// is set in the template properties, and whatever comes out the other side as a
// request member is, by definition, forwarded.
//
// What this does not catch: a forward whose template property name is neither
// the member name nor listed in templatePropertyAliases, and a member injected
// by a code path that is not one of these body builders.
func lambdaForwardedRequestMembers(gated map[string][]string) map[string]map[string]bool {
	const sentinel = "overcast-forward-probe"

	probeProps := func(members []string) map[string]any {
		props := map[string]any{}
		for _, member := range members {
			name, nested, isNested := strings.Cut(member, ".")
			if isNested {
				// Code.X arrives inside the Code property.
				code, _ := props[name].(map[string]any)
				if code == nil {
					code = map[string]any{}
					props[name] = code
				}
				code[nested] = sentinel
				continue
			}
			props[name] = sentinel
			for _, alias := range templatePropertyAliases[member] {
				// PublishToLatestPublished is read as a bool, so a string
				// sentinel would be ignored.
				props[alias] = true
			}
		}
		return props
	}

	out := map[string]map[string]bool{}

	// CreateFunction. Code sub-members are forwarded inside the Code map, so
	// they are read back out of it.
	createProps := probeProps(gated[lambda.OpCreateFunction])
	code, _ := createProps["Code"].(map[string]any)
	createBody := lambdaCreateFunctionBody("probe", code, createProps, nil)
	out[lambda.OpCreateFunction] = bodyMembers(createBody, gated[lambda.OpCreateFunction])

	// UpdateFunctionConfiguration. An empty prior makes every property differ,
	// which is the maximal forward.
	configProps := probeProps(gated[lambda.OpUpdateFunctionConfiguration])
	out[lambda.OpUpdateFunctionConfiguration] = bodyMembers(
		lambdaUpdateFunctionConfigurationBody(configProps, map[string]any{}),
		gated[lambda.OpUpdateFunctionConfiguration],
	)

	// UpdateFunctionCode reads its members out of the Code property.
	codeMembers := gated[lambda.OpUpdateFunctionCode]
	codeProps := map[string]any{}
	for _, member := range codeMembers {
		codeProps[member] = sentinel
	}
	updateCodeProps := map[string]any{"Code": codeProps, "PublishToLatestPublished": true}
	updateCodeBody, err := lambdaUpdateFunctionCodeBody(codeProps, updateCodeProps, map[string]any{})
	if err != nil {
		// ZipFile is not among the gated members, so packaging never runs.
		panic("lambdaUpdateFunctionCodeBody probe failed: " + err.Error())
	}
	out[lambda.OpUpdateFunctionCode] = bodyMembers(updateCodeBody, codeMembers)

	esmCreateProps := probeProps(gated[lambda.OpCreateEventSourceMapping])
	out[lambda.OpCreateEventSourceMapping] = bodyMembers(
		lambdaESMCreateBody(esmCreateProps, nil),
		gated[lambda.OpCreateEventSourceMapping],
	)

	esmUpdateProps := probeProps(gated[lambda.OpUpdateEventSourceMapping])
	esmUpdateBody, _ := lambdaESMUpdateBody("probe", esmUpdateProps, map[string]any{})
	out[lambda.OpUpdateEventSourceMapping] = bodyMembers(esmUpdateBody, gated[lambda.OpUpdateEventSourceMapping])

	return out
}

// bodyMembers reports which of members appear as keys of the built request
// body, looking inside the Code map for "Code."-prefixed members.
func bodyMembers(body map[string]any, members []string) map[string]bool {
	present := make(map[string]bool, len(members))
	code, _ := body["Code"].(map[string]any)
	for _, member := range members {
		if parent, nested, isNested := strings.Cut(member, "."); isNested && parent == "Code" {
			if _, ok := code[nested]; ok {
				present[member] = true
			}
			continue
		}
		if _, ok := body[member]; ok {
			present[member] = true
		}
	}
	return present
}
