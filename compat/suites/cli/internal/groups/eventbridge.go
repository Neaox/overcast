package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// EventBridge returns the EventBridge service group.
func EventBridge() ServiceGroup {
	g := &ebGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// eventbridge-buses
			"CreateEventBus":                 g.CreateEventBus,
			"DescribeEventBus":               g.DescribeEventBus,
			"ListEventBuses":                 g.ListEventBuses,
			"TagEventBus":                    g.TagEventBus,
			"ListEventBridgeTagsForResource": g.ListTagsForResource,
			"DeleteEventBus":                 g.DeleteEventBus,
			// eventbridge-rules
			"PutRule":           g.PutRule,
			"DescribeRule":      g.DescribeRule,
			"ListRules":         g.ListRules,
			"PutTargets":        g.PutTargets,
			"ListTargetsByRule": g.ListTargetsByRule,
			"DisableRule":       g.DisableRule,
			"EnableRule":        g.EnableRule,
			"RemoveTargets":     g.RemoveTargets,
			"DeleteRule":        g.DeleteRule,
			// eventbridge-events
			"PutEvents":      g.PutEvents,
			"PutEventsBatch": g.PutEventsBatch,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"eventbridge-buses":  g.setupBuses,
			"eventbridge-rules":  g.setupRules,
			"eventbridge-events": g.setupEvents,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"eventbridge-buses":  g.teardownBus,
			"eventbridge-rules":  g.teardownRules,
			"eventbridge-events": g.teardownEvents,
		},
	}
}

type ebGroup struct{}

func (g *ebGroup) busName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-eb", t.RunID)
}
func (g *ebGroup) ruleName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-rule", t.RunID)
}
func (g *ebGroup) targetID(t *harness.TestContext) string {
	return fmt.Sprintf("%s-tgt", t.RunID)
}
func (g *ebGroup) fakeTargetARN(t *harness.TestContext) string {
	return fmt.Sprintf("arn:aws:sqs:us-east-1:000000000000:oc-target-%s", t.RunID)
}

// ─── eventbridge-buses ───────────────────────────────────────────────────────

func (g *ebGroup) setupBuses(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *ebGroup) CreateEventBus(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "create-event-bus",
		"--name", g.busName(t),
	)
	if err != nil {
		return err
	}
	arn, _ := out["EventBusArn"].(string)
	if arn == "" {
		return fmt.Errorf("eventbridge CreateEventBus: missing EventBusArn")
	}
	t.Set("bus_arn", arn)
	return nil
}

func (g *ebGroup) DescribeEventBus(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "describe-event-bus",
		"--name", g.busName(t),
	)
	if err != nil {
		return err
	}
	if name, _ := out["Name"].(string); name != g.busName(t) {
		return fmt.Errorf("eb DescribeEventBus: expected Name=%q, got %q", g.busName(t), name)
	}
	return nil
}

func (g *ebGroup) ListEventBuses(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "events", "list-event-buses")
	if err != nil {
		return err
	}
	buses, _ := out["EventBuses"].([]any)
	for _, raw := range buses {
		if m, ok := raw.(map[string]any); ok && m["Name"] == g.busName(t) {
			return nil
		}
	}
	return fmt.Errorf("eb ListEventBuses: bus %q not found", g.busName(t))
}

func (g *ebGroup) TagEventBus(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("bus_arn")
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "tag-resource",
		"--resource-arn", arn,
		"--tags", `[{"Key":"env","Value":"test"}]`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-tags-for-resource",
		"--resource-arn", arn,
	)
	if err != nil {
		return fmt.Errorf("eb TagEventBus: list-tags failed: %w", err)
	}
	tags, _ := out["Tags"].([]any)
	for _, raw := range tags {
		if m, ok := raw.(map[string]any); ok && m["Key"] == "env" && m["Value"] == "test" {
			return nil
		}
	}
	return fmt.Errorf("eb TagEventBus: env=test tag not found")
}

func (g *ebGroup) ListTagsForResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("bus_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-tags-for-resource",
		"--resource-arn", arn,
	)
	if err != nil {
		return err
	}
	tags, _ := out["Tags"].([]any)
	if len(tags) == 0 {
		return fmt.Errorf("eb ListTagsForResource: expected tags, got none")
	}
	return nil
}

func (g *ebGroup) DeleteEventBus(_ context.Context, t *harness.TestContext) error {
	name := g.busName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "delete-event-bus",
		"--name", name,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "events", "list-event-buses")
	if err != nil {
		return fmt.Errorf("eb DeleteEventBus: list-event-buses failed: %w", err)
	}
	buses, _ := out["EventBuses"].([]any)
	for _, raw := range buses {
		if m, ok := raw.(map[string]any); ok && m["Name"] == name {
			return fmt.Errorf("eb DeleteEventBus: bus %q still present", name)
		}
	}
	return nil
}

func (g *ebGroup) teardownBus(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "events", "delete-event-bus", "--name", g.busName(t)) //nolint:errcheck
	return nil
}

// ─── eventbridge-rules ───────────────────────────────────────────────────────

func (g *ebGroup) setupRules(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "create-event-bus",
		"--name", g.busName(t),
	)
	if err != nil {
		return err
	}
	arn, _ := out["EventBusArn"].(string)
	t.Set("bus_arn", arn)
	return nil
}

func (g *ebGroup) PutRule(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "put-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
		"--schedule-expression", "rate(5 minutes)",
		"--state", "ENABLED",
	)
	if err != nil {
		return err
	}
	if out["RuleArn"] == nil {
		return fmt.Errorf("eb PutRule: missing RuleArn")
	}
	return nil
}

func (g *ebGroup) DescribeRule(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "describe-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return err
	}
	if name, _ := out["Name"].(string); name != g.ruleName(t) {
		return fmt.Errorf("eb DescribeRule: expected Name=%q, got %q", g.ruleName(t), name)
	}
	return nil
}

func (g *ebGroup) ListRules(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-rules",
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return err
	}
	rules, _ := out["Rules"].([]any)
	for _, raw := range rules {
		if m, ok := raw.(map[string]any); ok && m["Name"] == g.ruleName(t) {
			return nil
		}
	}
	return fmt.Errorf("eb ListRules: rule %q not found", g.ruleName(t))
}

func (g *ebGroup) PutTargets(_ context.Context, t *harness.TestContext) error {
	targets := fmt.Sprintf(
		`[{"Id":"%s","Arn":"%s"}]`,
		g.targetID(t), g.fakeTargetARN(t),
	)
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "put-targets",
		"--rule", g.ruleName(t),
		"--event-bus-name", g.busName(t),
		"--targets", targets,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-targets-by-rule",
		"--rule", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return fmt.Errorf("eb PutTargets: list-targets failed: %w", err)
	}
	tgts, _ := out["Targets"].([]any)
	if len(tgts) == 0 {
		return fmt.Errorf("eb PutTargets: no targets after put")
	}
	return nil
}

func (g *ebGroup) ListTargetsByRule(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-targets-by-rule",
		"--rule", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return err
	}
	tgts, _ := out["Targets"].([]any)
	if len(tgts) == 0 {
		return fmt.Errorf("eb ListTargetsByRule: expected targets, got none")
	}
	return nil
}

func (g *ebGroup) DisableRule(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "disable-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "describe-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return fmt.Errorf("eb DisableRule: describe failed: %w", err)
	}
	if state, _ := out["State"].(string); state != "DISABLED" {
		return fmt.Errorf("eb DisableRule: expected DISABLED, got %q", state)
	}
	return nil
}

func (g *ebGroup) EnableRule(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "enable-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "describe-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return fmt.Errorf("eb EnableRule: describe failed: %w", err)
	}
	if state, _ := out["State"].(string); state != "ENABLED" {
		return fmt.Errorf("eb EnableRule: expected ENABLED, got %q", state)
	}
	return nil
}

func (g *ebGroup) RemoveTargets(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "remove-targets",
		"--rule", g.ruleName(t),
		"--event-bus-name", g.busName(t),
		"--ids", g.targetID(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-targets-by-rule",
		"--rule", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return fmt.Errorf("eb RemoveTargets: list-targets failed: %w", err)
	}
	tgts, _ := out["Targets"].([]any)
	if len(tgts) > 0 {
		return fmt.Errorf("eb RemoveTargets: targets still present")
	}
	return nil
}

func (g *ebGroup) DeleteRule(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"events", "delete-rule",
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "list-rules",
		"--event-bus-name", g.busName(t),
	)
	if err != nil {
		return fmt.Errorf("eb DeleteRule: list-rules failed: %w", err)
	}
	rules, _ := out["Rules"].([]any)
	for _, raw := range rules {
		if m, ok := raw.(map[string]any); ok && m["Name"] == g.ruleName(t) {
			return fmt.Errorf("eb DeleteRule: rule still present")
		}
	}
	return nil
}

func (g *ebGroup) teardownRules(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "events", "remove-targets", //nolint:errcheck
		"--rule", g.ruleName(t),
		"--event-bus-name", g.busName(t),
		"--ids", g.targetID(t),
	)
	awscli.Run(t.Endpoint, t.Region, "events", "delete-rule", //nolint:errcheck
		"--name", g.ruleName(t),
		"--event-bus-name", g.busName(t),
	)
	awscli.Run(t.Endpoint, t.Region, "events", "delete-event-bus", "--name", g.busName(t)) //nolint:errcheck
	return nil
}

// ─── eventbridge-events ──────────────────────────────────────────────────────

func (g *ebGroup) setupEvents(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "create-event-bus",
		"--name", g.busName(t),
	)
	if err != nil {
		return err
	}
	arn, _ := out["EventBusArn"].(string)
	t.Set("bus_arn", arn)
	return nil
}

func (g *ebGroup) PutEvents(_ context.Context, t *harness.TestContext) error {
	entries := fmt.Sprintf(
		`[{"Source":"oc.cli","DetailType":"TestEvent","Detail":"{\"runId\":\"%s\"}","EventBusName":"%s"}]`,
		t.RunID, g.busName(t),
	)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "put-events",
		"--entries", entries,
	)
	if err != nil {
		return err
	}
	if fc, _ := out["FailedEntryCount"].(float64); fc > 0 {
		return fmt.Errorf("eb PutEvents: FailedEntryCount=%v", fc)
	}
	return nil
}

func (g *ebGroup) PutEventsBatch(_ context.Context, t *harness.TestContext) error {
	entries := fmt.Sprintf(
		`[`+
			`{"Source":"oc.cli","DetailType":"Event1","Detail":"{\"n\":1}","EventBusName":"%s"},`+
			`{"Source":"oc.cli","DetailType":"Event2","Detail":"{\"n\":2}","EventBusName":"%s"}`+
			`]`,
		g.busName(t), g.busName(t),
	)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"events", "put-events",
		"--entries", entries,
	)
	if err != nil {
		return err
	}
	if fc, _ := out["FailedEntryCount"].(float64); fc > 0 {
		return fmt.Errorf("eb PutEventsBatch: FailedEntryCount=%v", fc)
	}
	resEntries, _ := out["Entries"].([]any)
	if len(resEntries) != 2 {
		return fmt.Errorf("eb PutEventsBatch: expected 2 Entries, got %d", len(resEntries))
	}
	return nil
}

func (g *ebGroup) teardownEvents(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "events", "delete-event-bus", "--name", g.busName(t)) //nolint:errcheck
	return nil
}
