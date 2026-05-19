package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// CloudWatchLogs returns the CloudWatch Logs service group.
func CloudWatchLogs() ServiceGroup {
	g := &cwlGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// logs-groups
			"CreateLogGroup":        g.CreateLogGroup,
			"DescribeLogGroups":     g.DescribeLogGroups,
			"PutRetentionPolicy":    g.PutRetentionPolicy,
			"VerifyRetentionPolicy": g.VerifyRetentionPolicy,
			"DeleteRetentionPolicy": g.DeleteRetentionPolicy,
			"CreateLogStream":       g.CreateLogStream,
			"TagLogGroup":           g.TagLogGroup,
			"DeleteLogGroup":        g.DeleteLogGroup,
			// logs-events
			"PutLogEvents":       g.PutLogEvents,
			"GetLogEvents":       g.GetLogEvents,
			"FilterLogEvents":    g.FilterLogEvents,
			"DescribeLogStreams": g.DescribeLogStreams,
			"DeleteLogStream":    g.DeleteLogStream,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"logs-groups": g.setupGroups,
			"logs-events": g.setupEvents,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"logs-groups": g.teardownGroup,
			"logs-events": g.teardownEventsGroup,
		},
	}
}

type cwlGroup struct{}

// groupsName returns the log group name used by the logs-groups test group.
// Uses a distinct suffix from eventsGroupName to avoid intra-suite conflicts
// when both test groups run in parallel.
func (g *cwlGroup) groupsName(t *harness.TestContext) string {
	return fmt.Sprintf("/oc/cwl/%s", t.RunID)
}

// eventsGroupName returns the log group name used by the logs-events test group.
func (g *cwlGroup) eventsGroupName(t *harness.TestContext) string {
	return fmt.Sprintf("/oc/cwl-ev/%s", t.RunID)
}

func (g *cwlGroup) groupName(t *harness.TestContext) string {
	return g.groupsName(t)
}
func (g *cwlGroup) streamName(t *harness.TestContext) string {
	return fmt.Sprintf("stream-%s", t.RunID)
}

// ─── logs-groups ─────────────────────────────────────────────────────────────

func (g *cwlGroup) setupGroups(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *cwlGroup) CreateLogGroup(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"logs", "create-log-group",
		"--log-group-name", g.groupName(t),
	)
}

func (g *cwlGroup) DescribeLogGroups(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "describe-log-groups",
		"--log-group-name-prefix", "/oc/cwl/",
	)
	if err != nil {
		return err
	}
	groups, _ := out["logGroups"].([]any)
	if len(groups) == 0 {
		return fmt.Errorf("cwl DescribeLogGroups: expected at least 1 log group")
	}
	return nil
}

func (g *cwlGroup) PutRetentionPolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"logs", "put-retention-policy",
		"--log-group-name", g.groupName(t),
		"--retention-in-days", "7",
	)
}

func (g *cwlGroup) VerifyRetentionPolicy(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "describe-log-groups",
		"--log-group-name-prefix", g.groupName(t),
	)
	if err != nil {
		return err
	}
	groups, _ := out["logGroups"].([]any)
	if len(groups) == 0 {
		return fmt.Errorf("cwl VerifyRetentionPolicy: log group not found")
	}
	grp := groups[0].(map[string]any)
	days, _ := grp["retentionInDays"].(float64)
	if days != 7 {
		return fmt.Errorf("cwl VerifyRetentionPolicy: expected retentionInDays=7, got %v", days)
	}
	return nil
}

func (g *cwlGroup) DeleteRetentionPolicy(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"logs", "delete-retention-policy",
		"--log-group-name", g.groupName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "describe-log-groups",
		"--log-group-name-prefix", g.groupName(t),
	)
	if err != nil {
		return fmt.Errorf("cwl DeleteRetentionPolicy: describe failed: %w", err)
	}
	groups, _ := out["logGroups"].([]any)
	for _, raw := range groups {
		if m, ok := raw.(map[string]any); ok && m["logGroupName"] == g.groupName(t) {
			if _, hasRetention := m["retentionInDays"]; hasRetention {
				return fmt.Errorf("cwl DeleteRetentionPolicy: retention still set")
			}
		}
	}
	return nil
}

func (g *cwlGroup) CreateLogStream(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"logs", "create-log-stream",
		"--log-group-name", g.groupName(t),
		"--log-stream-name", fmt.Sprintf("stream-grp-%s", t.RunID),
	)
}

func (g *cwlGroup) TagLogGroup(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"logs", "tag-log-group",
		"--log-group-name", g.groupName(t),
		"--tags", `env=test`,
	)
}

func (g *cwlGroup) DeleteLogGroup(_ context.Context, t *harness.TestContext) error {
	name := g.groupName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"logs", "delete-log-group",
		"--log-group-name", name,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "describe-log-groups",
		"--log-group-name-prefix", name,
	)
	if err != nil {
		return fmt.Errorf("cwl DeleteLogGroup: describe failed: %w", err)
	}
	groups, _ := out["logGroups"].([]any)
	for _, raw := range groups {
		if m, ok := raw.(map[string]any); ok && m["logGroupName"] == name {
			return fmt.Errorf("cwl DeleteLogGroup: group still present")
		}
	}
	return nil
}

func (g *cwlGroup) teardownGroup(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "logs", "delete-log-group", "--log-group-name", g.groupsName(t)) //nolint:errcheck
	return nil
}

func (g *cwlGroup) teardownEventsGroup(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "logs", "delete-log-group", "--log-group-name", g.eventsGroupName(t)) //nolint:errcheck
	return nil
}

// ─── logs-events ─────────────────────────────────────────────────────────────

func (g *cwlGroup) setupEvents(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"logs", "create-log-group",
		"--log-group-name", g.eventsGroupName(t),
	); err != nil {
		return err
	}
	return awscli.Run(t.Endpoint, t.Region,
		"logs", "create-log-stream",
		"--log-group-name", g.eventsGroupName(t),
		"--log-stream-name", g.streamName(t),
	)
}

func (g *cwlGroup) PutLogEvents(_ context.Context, t *harness.TestContext) error {
	events := `[{"timestamp":1700000000000,"message":"hello from CLI test"}]`
	if err := awscli.Run(t.Endpoint, t.Region,
		"logs", "put-log-events",
		"--log-group-name", g.eventsGroupName(t),
		"--log-stream-name", g.streamName(t),
		"--log-events", events,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "get-log-events",
		"--log-group-name", g.eventsGroupName(t),
		"--log-stream-name", g.streamName(t),
	)
	if err != nil {
		return fmt.Errorf("cwl PutLogEvents: get-log-events failed: %w", err)
	}
	evts, _ := out["events"].([]any)
	if len(evts) == 0 {
		return fmt.Errorf("cwl PutLogEvents: no events found after put")
	}
	return nil
}

func (g *cwlGroup) GetLogEvents(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "get-log-events",
		"--log-group-name", g.eventsGroupName(t),
		"--log-stream-name", g.streamName(t),
	)
	if err != nil {
		return err
	}
	evts, _ := out["events"].([]any)
	if len(evts) == 0 {
		return fmt.Errorf("cwl GetLogEvents: expected events, got none")
	}
	return nil
}

func (g *cwlGroup) FilterLogEvents(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "filter-log-events",
		"--log-group-name", g.eventsGroupName(t),
		"--filter-pattern", "hello",
	)
	if err != nil {
		return err
	}
	evts, _ := out["events"].([]any)
	if len(evts) == 0 {
		return fmt.Errorf("cwl FilterLogEvents: expected matching events, got none")
	}
	return nil
}

func (g *cwlGroup) DescribeLogStreams(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "describe-log-streams",
		"--log-group-name", g.eventsGroupName(t),
	)
	if err != nil {
		return err
	}
	streams, _ := out["logStreams"].([]any)
	if len(streams) == 0 {
		return fmt.Errorf("cwl DescribeLogStreams: expected at least 1 stream")
	}
	return nil
}

func (g *cwlGroup) DeleteLogStream(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"logs", "delete-log-stream",
		"--log-group-name", g.eventsGroupName(t),
		"--log-stream-name", g.streamName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"logs", "describe-log-streams",
		"--log-group-name", g.eventsGroupName(t),
	)
	if err != nil {
		return fmt.Errorf("cwl DeleteLogStream: describe failed: %w", err)
	}
	streams, _ := out["logStreams"].([]any)
	for _, raw := range streams {
		if m, ok := raw.(map[string]any); ok && m["logStreamName"] == g.streamName(t) {
			return fmt.Errorf("cwl DeleteLogStream: stream still present")
		}
	}
	return nil
}
