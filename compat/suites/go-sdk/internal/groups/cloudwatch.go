package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func CloudWatchLogs(c *clients.Clients) ServiceGroup {
	g := &cwlGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateLogGroup":        g.CreateLogGroup,
			"DescribeLogGroups":     g.DescribeLogGroups,
			"DeleteLogGroup":        g.DeleteLogGroup,
			"DescribeLogStreams":    g.DescribeLogStreams,
			"PutLogEvents":          g.PutLogEvents,
			"GetLogEvents":          g.GetLogEvents,
			"FilterLogEvents":       g.FilterLogEvents,
			"DeleteLogStream":       g.DeleteLogStream,
			"PutRetentionPolicy":    g.PutRetentionPolicy,
			"VerifyRetentionPolicy": g.VerifyRetentionPolicy,
			"DeleteRetentionPolicy": g.DeleteRetentionPolicy,
			"CreateLogStream":       g.CreateLogStream,
			"TagLogGroup":           g.TagLogGroup,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"logs-groups": g.setupGroups,
			"logs-events": g.setupEvents,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"logs-groups": g.teardownGroups,
			"logs-events": g.teardownEvents,
		},
	}
}

type cwlGroup struct{ c *clients.Clients }

func (g *cwlGroup) client() *cloudwatchlogs.Client { return g.c.CloudWatchLogs() }

// ── logs-groups ───────────────────────────────────────────────────────────────

func (g *cwlGroup) setupGroups(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/logs", t.RunID)
	if _, err := g.client().CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(name),
	}); err != nil {
		return err
	}
	t.Set("cwl_group", name)
	return nil
}

func (g *cwlGroup) teardownGroups(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("cwl_group"); name != "" {
		g.client().DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *cwlGroup) CreateLogGroup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/create", t.RunID)
	if _, err := g.client().CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(name),
	}); err != nil {
		return err
	}
	// Verify group appears
	resp, err := g.client().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(name),
	})
	if err != nil {
		g.client().DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String(name)}) //nolint:errcheck
		return fmt.Errorf("CreateLogGroup: DescribeLogGroups verify failed: %w", err)
	}
	found := false
	for _, lg := range resp.LogGroups {
		if aws.ToString(lg.LogGroupName) == name {
			found = true
			break
		}
	}
	g.client().DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String(name)}) //nolint:errcheck
	if !found {
		return fmt.Errorf("CreateLogGroup: group %q not found", name)
	}
	return nil
}

func (g *cwlGroup) DescribeLogGroups(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("cwl_group")
	resp, err := g.client().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(name),
	})
	if err != nil {
		return err
	}
	if len(resp.LogGroups) == 0 {
		return fmt.Errorf("DescribeLogGroups: %q not found", name)
	}
	return nil
}

func (g *cwlGroup) DeleteLogGroup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("/oc/%s/del", t.RunID)
	g.client().CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{LogGroupName: aws.String(name)}) //nolint:errcheck
	_, err := g.client().DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String(name)})
	if err != nil {
		return err
	}
	resp, dErr := g.client().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(name),
	})
	if dErr != nil {
		return nil
	}
	for _, lg := range resp.LogGroups {
		if aws.ToString(lg.LogGroupName) == name {
			return fmt.Errorf("DeleteLogGroup: group %q still present", name)
		}
	}
	return nil
}

func (g *cwlGroup) CreateLogStream(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_group")
	streamName := "test-stream"
	if _, err := g.client().CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(streamName),
	}); err != nil {
		return err
	}
	t.Set("cwl_stream", streamName)
	return nil
}

func (g *cwlGroup) TagLogGroup(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_group")
	_, err := g.client().TagLogGroup(ctx, &cloudwatchlogs.TagLogGroupInput{
		LogGroupName: aws.String(group),
		Tags:         map[string]string{"env": "test"},
	})
	return err
}

func (g *cwlGroup) DescribeLogStreams(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_evt_group")
	resp, err := g.client().DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName: aws.String(group),
	})
	if err != nil {
		return err
	}
	if len(resp.LogStreams) == 0 {
		return fmt.Errorf("DescribeLogStreams: expected ≥1 stream")
	}
	return nil
}

func (g *cwlGroup) PutRetentionPolicy(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_group")
	_, err := g.client().PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		LogGroupName:    aws.String(group),
		RetentionInDays: aws.Int32(7),
	})
	if err != nil {
		return err
	}
	resp, err := g.client().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(group),
	})
	if err != nil {
		return fmt.Errorf("PutRetentionPolicy: DescribeLogGroups verify failed: %w", err)
	}
	for _, lg := range resp.LogGroups {
		if aws.ToString(lg.LogGroupName) == group {
			if aws.ToInt32(lg.RetentionInDays) != 7 {
				return fmt.Errorf("PutRetentionPolicy: expected retention=7, got %d", aws.ToInt32(lg.RetentionInDays))
			}
			return nil
		}
	}
	return fmt.Errorf("PutRetentionPolicy: group %q not found", group)
}

func (g *cwlGroup) VerifyRetentionPolicy(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_group")
	resp, err := g.client().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(group),
	})
	if err != nil {
		return err
	}
	for _, lg := range resp.LogGroups {
		if aws.ToString(lg.LogGroupName) == group {
			if lg.RetentionInDays == nil || *lg.RetentionInDays != 7 {
				return fmt.Errorf("VerifyRetentionPolicy: expected 7, got %v", lg.RetentionInDays)
			}
			return nil
		}
	}
	return fmt.Errorf("VerifyRetentionPolicy: log group %q not found", group)
}

func (g *cwlGroup) DeleteRetentionPolicy(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_group")
	_, err := g.client().DeleteRetentionPolicy(ctx, &cloudwatchlogs.DeleteRetentionPolicyInput{
		LogGroupName: aws.String(group),
	})
	if err != nil {
		return err
	}
	resp, dErr := g.client().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(group),
	})
	if dErr != nil {
		return nil
	}
	for _, lg := range resp.LogGroups {
		if aws.ToString(lg.LogGroupName) == group && aws.ToInt32(lg.RetentionInDays) != 0 {
			return fmt.Errorf("DeleteRetentionPolicy: retention still set to %d", aws.ToInt32(lg.RetentionInDays))
		}
	}
	return nil
}

// ── logs-events ───────────────────────────────────────────────────────────────

func (g *cwlGroup) setupEvents(ctx context.Context, t *harness.TestContext) error {
	group := fmt.Sprintf("/oc/%s/events", t.RunID)
	stream := "event-stream"
	if _, err := g.client().CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(group),
	}); err != nil {
		return err
	}
	if _, err := g.client().CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(stream),
	}); err != nil {
		return err
	}
	t.Set("cwl_evt_group", group)
	t.Set("cwl_evt_stream", stream)
	return nil
}

func (g *cwlGroup) teardownEvents(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("cwl_evt_group"); name != "" {
		g.client().DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *cwlGroup) PutLogEvents(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_evt_group")
	stream := t.GetString("cwl_evt_stream")
	now := time.Now().UnixMilli()
	resp, err := g.client().PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(stream),
		LogEvents: []cwltypes.InputLogEvent{
			{Message: aws.String("event-1"), Timestamp: aws.Int64(now)},
			{Message: aws.String("event-2"), Timestamp: aws.Int64(now + 1)},
		},
	})
	if err != nil {
		return err
	}
	if resp.RejectedLogEventsInfo != nil && (aws.ToInt32(resp.RejectedLogEventsInfo.TooOldLogEventEndIndex) > 0 || aws.ToInt32(resp.RejectedLogEventsInfo.TooNewLogEventStartIndex) > 0) {
		return fmt.Errorf("PutLogEvents: some events were rejected")
	}
	return nil
}

func (g *cwlGroup) GetLogEvents(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_evt_group")
	stream := t.GetString("cwl_evt_stream")
	resp, err := g.client().GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(stream),
		StartFromHead: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	if len(resp.Events) == 0 {
		return fmt.Errorf("GetLogEvents: expected ≥1 event")
	}
	return nil
}

func (g *cwlGroup) FilterLogEvents(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_evt_group")
	resp, err := g.client().FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  aws.String(group),
		FilterPattern: aws.String("event"),
	})
	if err != nil {
		return err
	}
	if len(resp.Events) == 0 {
		return fmt.Errorf("FilterLogEvents: expected ≥1 matching event")
	}
	return nil
}

func (g *cwlGroup) DeleteLogStream(ctx context.Context, t *harness.TestContext) error {
	group := t.GetString("cwl_evt_group")
	stream := "delete-stream"
	g.client().CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{ //nolint:errcheck
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream),
	})
	_, err := g.client().DeleteLogStream(ctx, &cloudwatchlogs.DeleteLogStreamInput{
		LogGroupName: aws.String(group), LogStreamName: aws.String(stream),
	})
	if err != nil {
		return err
	}
	resp, dErr := g.client().DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName:        aws.String(group),
		LogStreamNamePrefix: aws.String(stream),
	})
	if dErr != nil {
		return nil
	}
	for _, ls := range resp.LogStreams {
		if aws.ToString(ls.LogStreamName) == stream {
			return fmt.Errorf("DeleteLogStream: stream %q still present", stream)
		}
	}
	return nil
}
