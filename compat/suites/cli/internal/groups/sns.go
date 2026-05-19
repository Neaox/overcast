package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// SNS returns the SNS service group.
func SNS() ServiceGroup {
	g := &snsGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// sns-topics
			"CreateTopic":        g.CreateTopic,
			"ListTopics":         g.ListTopics,
			"GetTopicAttributes": g.GetTopicAttributes,
			"SetTopicAttributes": g.SetTopicAttributes,
			"DeleteTopic":        g.DeleteTopic,
			// sns-publish
			"Publish":               g.Publish,
			"PublishWithAttributes": g.PublishWithAttributes,
			"PublishBatch":          g.PublishBatch,
			// sns-subscriptions
			"SubscribeSQS":              g.SubscribeSQS,
			"ListSubscriptionsByTopic":  g.ListSubscriptionsByTopic,
			"GetSubscriptionAttributes": g.GetSubscriptionAttributes,
			"PublishDeliveredToSQS":     g.PublishDeliveredToSQS,
			"SetSubscriptionAttributes": g.SetSubscriptionAttributes,
			"Unsubscribe":               g.Unsubscribe,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"sns-topics":        g.setupTopics,
			"sns-publish":       g.setupPublish,
			"sns-subscriptions": g.setupSubscriptions,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"sns-topics":        g.teardownTopics,
			"sns-publish":       g.teardownPublish,
			"sns-subscriptions": g.teardownSubscriptions,
		},
	}
}

type snsGroup struct{}

// topicsGroupTopicName is used by the sns-topics group.
func (g *snsGroup) topicsGroupTopicName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-snst", t.RunID)
}

// publishTopicName is used by the sns-publish group.
func (g *snsGroup) publishTopicName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-snsp", t.RunID)
}

// subTopicName is used by the sns-subscriptions group.
func (g *snsGroup) subTopicName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-snss", t.RunID)
}

// ─── sns-topics ───────────────────────────────────────────────────────────────

func (g *snsGroup) setupTopics(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *snsGroup) CreateTopic(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "create-topic",
		"--name", g.topicsGroupTopicName(t),
	)
	if err != nil {
		return err
	}
	arn, _ := out["TopicArn"].(string)
	if arn == "" {
		return fmt.Errorf("sns CreateTopic: missing TopicArn")
	}
	t.Set("topic_arn", arn)
	return nil
}

func (g *snsGroup) ListTopics(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "sns", "list-topics")
	if err != nil {
		return err
	}
	topics, _ := out["Topics"].([]any)
	want := t.GetString("topic_arn")
	for _, raw := range topics {
		if m, ok := raw.(map[string]any); ok {
			if m["TopicArn"] == want {
				return nil
			}
		}
	}
	return fmt.Errorf("sns ListTopics: topic ARN %q not found in list", want)
}

func (g *snsGroup) GetTopicAttributes(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("topic_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "get-topic-attributes",
		"--topic-arn", arn,
	)
	if err != nil {
		return err
	}
	attrs, _ := out["Attributes"].(map[string]any)
	if attrs["TopicArn"] != arn {
		return fmt.Errorf("sns GetTopicAttributes: TopicArn mismatch: got %v", attrs["TopicArn"])
	}
	return nil
}

func (g *snsGroup) SetTopicAttributes(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("topic_arn")
	if err := awscli.Run(t.Endpoint, t.Region,
		"sns", "set-topic-attributes",
		"--topic-arn", arn,
		"--attribute-name", "DisplayName",
		"--attribute-value", "OvercastTest",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "get-topic-attributes",
		"--topic-arn", arn,
	)
	if err != nil {
		return fmt.Errorf("sns SetTopicAttributes: get-topic-attributes failed: %w", err)
	}
	attrs, _ := out["Attributes"].(map[string]any)
	if attrs["DisplayName"] != "OvercastTest" {
		return fmt.Errorf("sns SetTopicAttributes: DisplayName not updated; got %v", attrs["DisplayName"])
	}
	return nil
}

func (g *snsGroup) DeleteTopic(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("topic_arn")
	if err := awscli.Run(t.Endpoint, t.Region, "sns", "delete-topic", "--topic-arn", arn); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "sns", "list-topics")
	if err != nil {
		return fmt.Errorf("sns DeleteTopic: list-topics failed: %w", err)
	}
	topics, _ := out["Topics"].([]any)
	for _, raw := range topics {
		if m, ok := raw.(map[string]any); ok && m["TopicArn"] == arn {
			return fmt.Errorf("sns DeleteTopic: topic %q still present", arn)
		}
	}
	return nil
}

func (g *snsGroup) teardownTopics(_ context.Context, t *harness.TestContext) error {
	if arn := t.GetString("topic_arn"); arn != "" {
		awscli.Run(t.Endpoint, t.Region, "sns", "delete-topic", "--topic-arn", arn) //nolint:errcheck
	}
	return nil
}

// ─── sns-publish ─────────────────────────────────────────────────────────────

func (g *snsGroup) setupPublish(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "sns", "create-topic", "--name", g.publishTopicName(t))
	if err != nil {
		return err
	}
	arn, _ := out["TopicArn"].(string)
	t.Set("topic_arn", arn)
	return nil
}

func (g *snsGroup) Publish(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("topic_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "publish",
		"--topic-arn", arn,
		"--message", "hello from CLI",
	)
	if err != nil {
		return err
	}
	if msgID, _ := out["MessageId"].(string); msgID == "" {
		return fmt.Errorf("sns Publish: missing MessageId")
	}
	return nil
}

func (g *snsGroup) PublishWithAttributes(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("topic_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "publish",
		"--topic-arn", arn,
		"--message", "attributed",
		"--message-attributes", `{"event":{"DataType":"String","StringValue":"test"}}`,
	)
	if err != nil {
		return err
	}
	if msgID, _ := out["MessageId"].(string); msgID == "" {
		return fmt.Errorf("sns PublishWithAttributes: missing MessageId")
	}
	return nil
}

func (g *snsGroup) PublishBatch(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("topic_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "publish-batch",
		"--topic-arn", arn,
		"--publish-batch-request-entries",
		`[{"Id":"m1","Message":"batch1"},{"Id":"m2","Message":"batch2"}]`,
	)
	if err != nil {
		return err
	}
	successful, _ := out["Successful"].([]any)
	if len(successful) != 2 {
		return fmt.Errorf("sns PublishBatch: expected 2 Successful entries, got %d", len(successful))
	}
	return nil
}

func (g *snsGroup) teardownPublish(_ context.Context, t *harness.TestContext) error {
	if arn := t.GetString("topic_arn"); arn != "" {
		awscli.Run(t.Endpoint, t.Region, "sns", "delete-topic", "--topic-arn", arn) //nolint:errcheck
	}
	return nil
}

// ─── sns-subscriptions ───────────────────────────────────────────────────────

func (g *snsGroup) setupSubscriptions(_ context.Context, t *harness.TestContext) error {
	// Create topic.
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "sns", "create-topic", "--name", g.subTopicName(t))
	if err != nil {
		return err
	}
	topicArn, _ := out["TopicArn"].(string)
	t.Set("topic_arn", topicArn)

	// Create SQS queue for subscription.
	qName := fmt.Sprintf("%s-snssub", t.RunID)
	qOut, err := awscli.RunOutput(t.Endpoint, t.Region, "sqs", "create-queue", "--queue-name", qName)
	if err != nil {
		return err
	}
	qURL, _ := qOut["QueueUrl"].(string)
	t.Set("sub_queue_url", qURL)

	// Get queue ARN.
	attrs, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sqs", "get-queue-attributes",
		"--queue-url", qURL,
		"--attribute-names", "QueueArn",
	)
	if err != nil {
		return err
	}
	attrMap, _ := attrs["Attributes"].(map[string]any)
	qArn, _ := attrMap["QueueArn"].(string)
	t.Set("sub_queue_arn", qArn)
	return nil
}

func (g *snsGroup) SubscribeSQS(_ context.Context, t *harness.TestContext) error {
	topicArn := t.GetString("topic_arn")
	qArn := t.GetString("sub_queue_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "subscribe",
		"--topic-arn", topicArn,
		"--protocol", "sqs",
		"--notification-endpoint", qArn,
	)
	if err != nil {
		return err
	}
	subArn, _ := out["SubscriptionArn"].(string)
	t.Set("sub_arn", subArn)
	return nil
}

func (g *snsGroup) ListSubscriptionsByTopic(_ context.Context, t *harness.TestContext) error {
	topicArn := t.GetString("topic_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "list-subscriptions-by-topic",
		"--topic-arn", topicArn,
	)
	if err != nil {
		return err
	}
	subs, _ := out["Subscriptions"].([]any)
	if len(subs) == 0 {
		return fmt.Errorf("sns ListSubscriptionsByTopic: expected at least 1 subscription")
	}
	return nil
}

func (g *snsGroup) GetSubscriptionAttributes(_ context.Context, t *harness.TestContext) error {
	subArn := t.GetString("sub_arn")
	if subArn == "" {
		return fmt.Errorf("sns GetSubscriptionAttributes: missing sub_arn")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "get-subscription-attributes",
		"--subscription-arn", subArn,
	)
	if err != nil {
		return err
	}
	attrs, _ := out["Attributes"].(map[string]any)
	if attrs["Protocol"] != "sqs" {
		return fmt.Errorf("sns GetSubscriptionAttributes: expected Protocol=sqs, got %v", attrs["Protocol"])
	}
	return nil
}

func (g *snsGroup) PublishDeliveredToSQS(_ context.Context, t *harness.TestContext) error {
	topicArn := t.GetString("topic_arn")
	qURL := t.GetString("sub_queue_url")
	if _, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "publish",
		"--topic-arn", topicArn,
		"--message", "delivery test",
	); err != nil {
		return err
	}
	// Poll the SQS queue to verify the message was delivered.
	for i := 0; i < 10; i++ {
		out, err := awscli.RunOutput(t.Endpoint, t.Region,
			"sqs", "receive-message",
			"--queue-url", qURL,
			"--max-number-of-messages", "1",
			"--wait-time-seconds", "1",
		)
		if err != nil {
			return fmt.Errorf("sns PublishDeliveredToSQS: receive-message failed: %w", err)
		}
		msgs, _ := out["Messages"].([]any)
		if len(msgs) > 0 {
			// Delete the message to clean up.
			if msg, ok := msgs[0].(map[string]any); ok {
				receipt, _ := msg["ReceiptHandle"].(string)
				awscli.Run(t.Endpoint, t.Region, "sqs", "delete-message", "--queue-url", qURL, "--receipt-handle", receipt) //nolint:errcheck
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("sns PublishDeliveredToSQS: message not received in SQS after 10 attempts")
}

func (g *snsGroup) SetSubscriptionAttributes(_ context.Context, t *harness.TestContext) error {
	subArn := t.GetString("sub_arn")
	if subArn == "" {
		return nil
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"sns", "set-subscription-attributes",
		"--subscription-arn", subArn,
		"--attribute-name", "RawMessageDelivery",
		"--attribute-value", "true",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "get-subscription-attributes",
		"--subscription-arn", subArn,
	)
	if err != nil {
		return fmt.Errorf("sns SetSubscriptionAttributes: get-subscription-attributes failed: %w", err)
	}
	attrs, _ := out["Attributes"].(map[string]any)
	if attrs["RawMessageDelivery"] != "true" {
		return fmt.Errorf("sns SetSubscriptionAttributes: RawMessageDelivery not updated; got %v", attrs["RawMessageDelivery"])
	}
	return nil
}

func (g *snsGroup) Unsubscribe(_ context.Context, t *harness.TestContext) error {
	subArn := t.GetString("sub_arn")
	if subArn == "" {
		return nil
	}
	if err := awscli.Run(t.Endpoint, t.Region, "sns", "unsubscribe", "--subscription-arn", subArn); err != nil {
		return err
	}
	// Verify the subscription no longer appears.
	topicArn := t.GetString("topic_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sns", "list-subscriptions-by-topic",
		"--topic-arn", topicArn,
	)
	if err != nil {
		return fmt.Errorf("sns Unsubscribe: list-subscriptions-by-topic failed: %w", err)
	}
	subs, _ := out["Subscriptions"].([]any)
	for _, raw := range subs {
		if m, ok := raw.(map[string]any); ok {
			if m["SubscriptionArn"] == subArn {
				return fmt.Errorf("sns Unsubscribe: subscription ARN %q still present after unsubscribe", subArn)
			}
		}
	}
	return nil
}

func (g *snsGroup) teardownSubscriptions(_ context.Context, t *harness.TestContext) error {
	if subArn := t.GetString("sub_arn"); subArn != "" {
		awscli.Run(t.Endpoint, t.Region, "sns", "unsubscribe", "--subscription-arn", subArn) //nolint:errcheck
	}
	if topicArn := t.GetString("topic_arn"); topicArn != "" {
		awscli.Run(t.Endpoint, t.Region, "sns", "delete-topic", "--topic-arn", topicArn) //nolint:errcheck
	}
	if qURL := t.GetString("sub_queue_url"); qURL != "" {
		awscli.Run(t.Endpoint, t.Region, "sqs", "delete-queue", "--queue-url", qURL) //nolint:errcheck
	}
	return nil
}
