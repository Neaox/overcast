package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

func EventBridge(c *clients.Clients) ServiceGroup {
	g := &ebGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateEventBus":                 g.CreateEventBus,
			"DescribeEventBus":               g.DescribeEventBus,
			"ListEventBuses":                 g.ListEventBuses,
			"TagEventBus":                    g.TagEventBus,
			"ListEventBridgeTagsForResource": g.ListTagsForResource,
			"DeleteEventBus":                 g.DeleteEventBus,
			"PutRule":                        g.PutRule,
			"DescribeRule":                   g.DescribeRule,
			"ListRules":                      g.ListRules,
			"EnableRule":                     g.EnableRule,
			"DisableRule":                    g.DisableRule,
			"PutTargets":                     g.PutTargets,
			"ListTargetsByRule":              g.ListTargetsByRule,
			"RemoveTargets":                  g.RemoveTargets,
			"DeleteRule":                     g.DeleteRule,
			"PutEvents":                      g.PutEvents,
			"PutEventsBatch":                 g.PutEventsBatch,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"eventbridge-buses":  g.setupBuses,
			"eventbridge-rules":  g.setupRules,
			"eventbridge-events": g.setupEvents,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"eventbridge-buses":  g.teardownBuses,
			"eventbridge-rules":  g.teardownRules,
			"eventbridge-events": g.teardownEvents,
		},
	}
}

type ebGroup struct{ c *clients.Clients }

func (g *ebGroup) cl() *eventbridge.Client { return g.c.EventBridge() }

// ── eventbridge-buses ─────────────────────────────────────────────────────────

func (g *ebGroup) setupBuses(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-bus-%s", t.RunID)
	resp, err := g.cl().CreateEventBus(ctx, &eventbridge.CreateEventBusInput{
		Name: aws.String(name),
	})
	if err != nil {
		return err
	}
	t.Set("eb_bus_name", name)
	t.Set("eb_bus_arn", aws.ToString(resp.EventBusArn))
	return nil
}

func (g *ebGroup) teardownBuses(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("eb_bus_name"); name != "" {
		g.cl().DeleteEventBus(ctx, &eventbridge.DeleteEventBusInput{Name: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *ebGroup) CreateEventBus(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cb-%s", t.RunID)
	_, err := g.cl().CreateEventBus(ctx, &eventbridge.CreateEventBusInput{Name: aws.String(name)})
	if err == nil {
		g.cl().DeleteEventBus(ctx, &eventbridge.DeleteEventBusInput{Name: aws.String(name)}) //nolint:errcheck
	}
	return err
}

func (g *ebGroup) DescribeEventBus(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeEventBus(ctx, &eventbridge.DescribeEventBusInput{
		Name: aws.String(t.GetString("eb_bus_name")),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Name) != t.GetString("eb_bus_name") {
		return fmt.Errorf("DescribeEventBus: name mismatch")
	}
	return nil
}

func (g *ebGroup) ListEventBuses(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListEventBuses(ctx, &eventbridge.ListEventBusesInput{})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

func (g *ebGroup) TagEBResource(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().TagResource(ctx, &eventbridge.TagResourceInput{
		ResourceARN: aws.String(t.GetString("eb_bus_arn")),
		Tags:        []types.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	return err
}

func (g *ebGroup) UntagEBResource(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().UntagResource(ctx, &eventbridge.UntagResourceInput{
		ResourceARN: aws.String(t.GetString("eb_bus_arn")),
		TagKeys:     []string{"env"},
	})
	return err
}

func (g *ebGroup) TagEventBus(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().TagResource(ctx, &eventbridge.TagResourceInput{
		ResourceARN: aws.String(t.GetString("eb_bus_arn")),
		Tags:        []types.Tag{{Key: aws.String("env"), Value: aws.String("compat")}},
	})
	return err
}

func (g *ebGroup) ListTagsForResource(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListTagsForResource(ctx, &eventbridge.ListTagsForResourceInput{
		ResourceARN: aws.String(t.GetString("eb_bus_arn")),
	})
	if err != nil {
		return err
	}
	for _, tag := range resp.Tags {
		if aws.ToString(tag.Key) == "env" {
			return nil
		}
	}
	return fmt.Errorf("ListTagsForResource: tag 'env' not found")
}

func (g *ebGroup) DeleteEventBus(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-db-%s", t.RunID)
	g.cl().CreateEventBus(ctx, &eventbridge.CreateEventBusInput{Name: aws.String(name)}) //nolint:errcheck
	_, err := g.cl().DeleteEventBus(ctx, &eventbridge.DeleteEventBusInput{Name: aws.String(name)})
	return err
}

// ── eventbridge-rules ─────────────────────────────────────────────────────────

func (g *ebGroup) setupRules(ctx context.Context, t *harness.TestContext) error {
	busName := fmt.Sprintf("oc-rbus-%s", t.RunID)
	if _, err := g.cl().CreateEventBus(ctx, &eventbridge.CreateEventBusInput{Name: aws.String(busName)}); err != nil {
		return err
	}
	t.Set("eb_rules_bus", busName)

	ruleName := fmt.Sprintf("oc-rule-%s", t.RunID)
	if _, err := g.cl().PutRule(ctx, &eventbridge.PutRuleInput{
		Name:               aws.String(ruleName),
		EventBusName:       aws.String(busName),
		ScheduleExpression: aws.String("rate(5 minutes)"),
		State:              types.RuleStateEnabled,
	}); err != nil {
		return err
	}
	t.Set("eb_rule_name", ruleName)
	return nil
}

func (g *ebGroup) teardownRules(ctx context.Context, t *harness.TestContext) error {
	bus := t.GetString("eb_rules_bus")
	rule := t.GetString("eb_rule_name")
	if bus != "" && rule != "" {
		// Remove all targets first
		tgtsResp, err := g.cl().ListTargetsByRule(ctx, &eventbridge.ListTargetsByRuleInput{
			Rule:         aws.String(rule),
			EventBusName: aws.String(bus),
		})
		if err == nil && len(tgtsResp.Targets) > 0 {
			ids := make([]string, 0, len(tgtsResp.Targets))
			for _, tgt := range tgtsResp.Targets {
				ids = append(ids, aws.ToString(tgt.Id))
			}
			g.cl().RemoveTargets(ctx, &eventbridge.RemoveTargetsInput{ //nolint:errcheck
				Rule: aws.String(rule), EventBusName: aws.String(bus), Ids: ids,
			})
		}
		g.cl().DeleteRule(ctx, &eventbridge.DeleteRuleInput{ //nolint:errcheck
			Name: aws.String(rule), EventBusName: aws.String(bus),
		})
	}
	if bus != "" {
		g.cl().DeleteEventBus(ctx, &eventbridge.DeleteEventBusInput{Name: aws.String(bus)}) //nolint:errcheck
	}
	return nil
}

func (g *ebGroup) PutRule(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().PutRule(ctx, &eventbridge.PutRuleInput{
		Name:               aws.String(fmt.Sprintf("oc-pr-%s", t.RunID)),
		EventBusName:       aws.String(t.GetString("eb_rules_bus")),
		ScheduleExpression: aws.String("rate(10 minutes)"),
		State:              types.RuleStateEnabled,
	})
	if err == nil {
		g.cl().DeleteRule(ctx, &eventbridge.DeleteRuleInput{ //nolint:errcheck
			Name:         aws.String(fmt.Sprintf("oc-pr-%s", t.RunID)),
			EventBusName: aws.String(t.GetString("eb_rules_bus")),
		})
	}
	return err
}

func (g *ebGroup) DescribeRule(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeRule(ctx, &eventbridge.DescribeRuleInput{
		Name:         aws.String(t.GetString("eb_rule_name")),
		EventBusName: aws.String(t.GetString("eb_rules_bus")),
	})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

func (g *ebGroup) ListRules(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListRules(ctx, &eventbridge.ListRulesInput{
		EventBusName: aws.String(t.GetString("eb_rules_bus")),
	})
	return err
}

func (g *ebGroup) EnableRule(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().EnableRule(ctx, &eventbridge.EnableRuleInput{
		Name:         aws.String(t.GetString("eb_rule_name")),
		EventBusName: aws.String(t.GetString("eb_rules_bus")),
	})
	return err
}

func (g *ebGroup) DisableRule(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DisableRule(ctx, &eventbridge.DisableRuleInput{
		Name:         aws.String(t.GetString("eb_rule_name")),
		EventBusName: aws.String(t.GetString("eb_rules_bus")),
	})
	if err == nil {
		g.cl().EnableRule(ctx, &eventbridge.EnableRuleInput{ //nolint:errcheck
			Name: aws.String(t.GetString("eb_rule_name")), EventBusName: aws.String(t.GetString("eb_rules_bus")),
		})
	}
	return err
}

func (g *ebGroup) PutTargets(ctx context.Context, t *harness.TestContext) error {
	rule := t.GetString("eb_rule_name")
	bus := t.GetString("eb_rules_bus")
	fakeArn := fmt.Sprintf("arn:aws:sqs:us-east-1:000000000000:oc-target-%s", t.RunID)
	resp, err := g.cl().PutTargets(ctx, &eventbridge.PutTargetsInput{
		Rule:         aws.String(rule),
		EventBusName: aws.String(bus),
		Targets:      []types.Target{{Id: aws.String("t1"), Arn: aws.String(fakeArn)}},
	})
	if err != nil {
		return err
	}
	if resp.FailedEntryCount > 0 {
		return fmt.Errorf("PutTargets: %d failed entries", resp.FailedEntryCount)
	}
	t.Set("eb_target_id", "t1")
	return nil
}

func (g *ebGroup) ListTargetsByRule(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListTargetsByRule(ctx, &eventbridge.ListTargetsByRuleInput{
		Rule:         aws.String(t.GetString("eb_rule_name")),
		EventBusName: aws.String(t.GetString("eb_rules_bus")),
	})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

func (g *ebGroup) RemoveTargets(ctx context.Context, t *harness.TestContext) error {
	targetID := t.GetString("eb_target_id")
	if targetID == "" {
		return nil
	}
	resp, err := g.cl().RemoveTargets(ctx, &eventbridge.RemoveTargetsInput{
		Rule:         aws.String(t.GetString("eb_rule_name")),
		EventBusName: aws.String(t.GetString("eb_rules_bus")),
		Ids:          []string{targetID},
	})
	if err != nil {
		return err
	}
	if resp.FailedEntryCount > 0 {
		return fmt.Errorf("RemoveTargets: %d failed", resp.FailedEntryCount)
	}
	return nil
}

func (g *ebGroup) TestEventPattern(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().TestEventPattern(ctx, &eventbridge.TestEventPatternInput{
		EventPattern: aws.String(`{"source":["my.app"]}`),
		Event:        aws.String(`{"source":"my.app","detail-type":"order","detail":{}}`),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	_ = resp
	return nil
}

func (g *ebGroup) DeleteRule(ctx context.Context, t *harness.TestContext) error {
	bus := t.GetString("eb_rules_bus")
	name := fmt.Sprintf("oc-dr-%s", t.RunID)
	g.cl().PutRule(ctx, &eventbridge.PutRuleInput{ //nolint:errcheck
		Name: aws.String(name), EventBusName: aws.String(bus),
		ScheduleExpression: aws.String("rate(1 day)"), State: types.RuleStateEnabled,
	})
	_, err := g.cl().DeleteRule(ctx, &eventbridge.DeleteRuleInput{
		Name: aws.String(name), EventBusName: aws.String(bus),
	})
	return err
}

// ── eventbridge-events ────────────────────────────────────────────────────────

func (g *ebGroup) setupEvents(ctx context.Context, t *harness.TestContext) error {
	busName := fmt.Sprintf("oc-ebus-%s", t.RunID)
	if _, err := g.cl().CreateEventBus(ctx, &eventbridge.CreateEventBusInput{Name: aws.String(busName)}); err != nil {
		return err
	}
	t.Set("eb_evt_bus", busName)
	return nil
}

func (g *ebGroup) teardownEvents(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("eb_evt_bus"); name != "" {
		g.cl().DeleteEventBus(ctx, &eventbridge.DeleteEventBusInput{Name: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *ebGroup) PutEvents(ctx context.Context, t *harness.TestContext) error {
	bus := t.GetString("eb_evt_bus")
	resp, err := g.cl().PutEvents(ctx, &eventbridge.PutEventsInput{
		Entries: []types.PutEventsRequestEntry{
			{
				EventBusName: aws.String(bus),
				Source:       aws.String("my.app"),
				DetailType:   aws.String("order"),
				Detail:       aws.String(`{"orderId":"123"}`),
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.FailedEntryCount > 0 {
		return fmt.Errorf("PutEvents: %d failed entries", resp.FailedEntryCount)
	}
	return nil
}

func (g *ebGroup) PutEventsBatch(ctx context.Context, t *harness.TestContext) error {
	entries := make([]types.PutEventsRequestEntry, 5)
	for i := range entries {
		entries[i] = types.PutEventsRequestEntry{
			Source:       aws.String(fmt.Sprintf("compat.%s", t.RunID)),
			DetailType:   aws.String("CompatBatch"),
			Detail:       aws.String(fmt.Sprintf(`{"index":%d}`, i)),
			EventBusName: aws.String("default"),
		}
	}
	resp, err := g.cl().PutEvents(ctx, &eventbridge.PutEventsInput{Entries: entries})
	if err != nil {
		return err
	}
	if resp.FailedEntryCount > 0 {
		return fmt.Errorf("PutEventsBatch: %d failed entries", resp.FailedEntryCount)
	}
	return nil
}
