package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func SNS(c *clients.Clients) ServiceGroup {
	g := &snsGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateTopic":               g.CreateTopic,
			"GetTopicAttributes":        g.GetTopicAttributes,
			"SetTopicAttributes":        g.SetTopicAttributes,
			"ListTopics":                g.ListTopics,
			"DeleteTopic":               g.DeleteTopic,
			"Publish":                   g.Publish,
			"PublishBatch":              g.PublishBatch,
			"PublishWithAttributes":     g.PublishWithAttributes,
			"PublishDeliveredToSQS":     g.PublishDeliveredToSQS,
			"SubscribeSQS":              g.SubscribeSQS,
			"ListSubscriptionsByTopic":  g.ListSubscriptionsByTopic,
			"GetSubscriptionAttributes": g.GetSubscriptionAttributes,
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

type snsGroup struct{ c *clients.Clients }

func (g *snsGroup) sns() *sns.Client { return g.c.SNS() }
func (g *snsGroup) sqs() *sqs.Client { return g.c.SQS() }

// ── sns-topics ────────────────────────────────────────────────────────────────

func (g *snsGroup) setupTopics(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-sns", t.RunID)
	resp, err := g.sns().CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		return err
	}
	t.Set("sns_topic_arn", aws.ToString(resp.TopicArn))
	t.Set("sns_topic_name", name)
	return nil
}

func (g *snsGroup) teardownTopics(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sns_topic_arn"); arn != "" {
		g.sns().DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: aws.String(arn)}) //nolint:errcheck
	}
	return nil
}

func (g *snsGroup) CreateTopic(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-snscreate", t.RunID)
	resp, err := g.sns().CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		return err
	}
	defer g.sns().DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: resp.TopicArn}) //nolint:errcheck
	// Verify topic appears in ListTopics
	list, err := g.sns().ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return fmt.Errorf("CreateTopic: ListTopics verify failed: %w", err)
	}
	for _, topic := range list.Topics {
		if aws.ToString(topic.TopicArn) == aws.ToString(resp.TopicArn) {
			return nil
		}
	}
	return fmt.Errorf("CreateTopic: topic not found in ListTopics")
}

func (g *snsGroup) GetTopicAttributes(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_topic_arn")
	resp, err := g.sns().GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(arn)})
	if err != nil {
		return err
	}
	if resp.Attributes["TopicArn"] != arn {
		return fmt.Errorf("GetTopicAttributes: ARN mismatch")
	}
	return nil
}

func (g *snsGroup) SetTopicAttributes(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_topic_arn")
	_, err := g.sns().SetTopicAttributes(ctx, &sns.SetTopicAttributesInput{
		TopicArn:       aws.String(arn),
		AttributeName:  aws.String("DisplayName"),
		AttributeValue: aws.String("CompatSuite"),
	})
	if err != nil {
		return err
	}
	// Verify the attribute was updated
	resp, err := g.sns().GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(arn)})
	if err != nil {
		return fmt.Errorf("SetTopicAttributes: GetTopicAttributes failed: %w", err)
	}
	if resp.Attributes["DisplayName"] != "CompatSuite" {
		return fmt.Errorf("SetTopicAttributes: expected DisplayName=CompatSuite, got %q", resp.Attributes["DisplayName"])
	}
	return nil
}

func (g *snsGroup) ListTopics(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_topic_arn")
	resp, err := g.sns().ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return err
	}
	for _, tp := range resp.Topics {
		if aws.ToString(tp.TopicArn) == arn {
			return nil
		}
	}
	return fmt.Errorf("ListTopics: %q not found", arn)
}

func (g *snsGroup) DeleteTopic(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-snsdel", t.RunID)
	resp, err := g.sns().CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		return err
	}
	if _, err = g.sns().DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: resp.TopicArn}); err != nil {
		return err
	}
	// Verify the topic is gone
	listResp, err := g.sns().ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return fmt.Errorf("DeleteTopic: ListTopics failed: %w", err)
	}
	for _, tp := range listResp.Topics {
		if aws.ToString(tp.TopicArn) == aws.ToString(resp.TopicArn) {
			return fmt.Errorf("DeleteTopic: topic %q still present after delete", aws.ToString(resp.TopicArn))
		}
	}
	return nil
}

// ── sns-publish ───────────────────────────────────────────────────────────────

func (g *snsGroup) setupPublish(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-snspub", t.RunID)
	resp, err := g.sns().CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		return err
	}
	t.Set("sns_pub_topic_arn", aws.ToString(resp.TopicArn))

	// Create SQS queue and subscribe
	qname := fmt.Sprintf("%s-snssqs", t.RunID)
	qresp, err := g.sqs().CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(qname)})
	if err != nil {
		return err
	}
	qurl := aws.ToString(qresp.QueueUrl)
	t.Set("sns_sqs_url", qurl)

	attr, err := g.sqs().GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: qresp.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{"QueueArn"},
	})
	if err != nil {
		return err
	}
	qarn := attr.Attributes["QueueArn"]

	subResp, err := g.sns().Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: resp.TopicArn,
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(qarn),
	})
	if err != nil {
		return err
	}
	t.Set("sns_sub_arn", aws.ToString(subResp.SubscriptionArn))
	return nil
}

func (g *snsGroup) teardownPublish(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sns_sub_arn"); arn != "" {
		g.sns().Unsubscribe(ctx, &sns.UnsubscribeInput{SubscriptionArn: aws.String(arn)}) //nolint:errcheck
	}
	if arn := t.GetString("sns_pub_topic_arn"); arn != "" {
		g.sns().DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: aws.String(arn)}) //nolint:errcheck
	}
	if url := t.GetString("sns_sqs_url"); url != "" {
		g.sqs().DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(url)}) //nolint:errcheck
	}
	return nil
}

func (g *snsGroup) Publish(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_pub_topic_arn")
	resp, err := g.sns().Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(arn),
		Message:  aws.String("hello-from-go-sdk"),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.MessageId) == "" {
		return fmt.Errorf("Publish: empty MessageId")
	}
	return nil
}

func (g *snsGroup) PublishBatch(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_pub_topic_arn")
	entries := make([]snstypes.PublishBatchRequestEntry, 5)
	for i := range entries {
		entries[i] = snstypes.PublishBatchRequestEntry{
			Id: aws.String(fmt.Sprintf("m%d", i)), Message: aws.String(fmt.Sprintf("batch-%d", i)),
		}
	}
	resp, err := g.sns().PublishBatch(ctx, &sns.PublishBatchInput{
		TopicArn: aws.String(arn), PublishBatchRequestEntries: entries,
	})
	if err != nil {
		return err
	}
	if len(resp.Failed) > 0 {
		return fmt.Errorf("PublishBatch: %d failed", len(resp.Failed))
	}
	return nil
}

func (g *snsGroup) PublishWithAttributes(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_pub_topic_arn")
	resp, err := g.sns().Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(arn),
		Message:  aws.String("hello-with-attrs"),
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"env": {DataType: aws.String("String"), StringValue: aws.String("test")},
		},
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.MessageId) == "" {
		return fmt.Errorf("PublishWithAttributes: empty MessageId")
	}
	return nil
}

func (g *snsGroup) PublishToSQS(ctx context.Context, t *harness.TestContext) error {
	return g.Publish(ctx, t) // reuses Publish
}

func (g *snsGroup) PublishDeliveredToSQS(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("sns_sub_topic_arn")
	qurl := t.GetString("sns_sub_q_url")
	if _, err := g.sns().Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(arn), Message: aws.String("delivery-test"),
	}); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		recv, _ := g.sqs().ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(qurl), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
		})
		if recv != nil && len(recv.Messages) > 0 {
			for _, m := range recv.Messages {
				g.sqs().DeleteMessage(ctx, &sqs.DeleteMessageInput{ //nolint:errcheck
					QueueUrl: aws.String(qurl), ReceiptHandle: m.ReceiptHandle,
				})
			}
			return nil
		}
	}
	return fmt.Errorf("PublishDeliveredToSQS: message not delivered within 5s")
}

// ── sns-subscriptions ─────────────────────────────────────────────────────────

func (g *snsGroup) setupSubscriptions(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-snssub", t.RunID)
	resp, err := g.sns().CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		return err
	}
	t.Set("sns_sub_topic_arn", aws.ToString(resp.TopicArn))

	qname := fmt.Sprintf("%s-snssubq", t.RunID)
	qresp, err := g.sqs().CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(qname)})
	if err != nil {
		return err
	}
	t.Set("sns_sub_q_url", aws.ToString(qresp.QueueUrl))

	attr, _ := g.sqs().GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: qresp.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{"QueueArn"},
	})
	t.Set("sns_sub_q_arn", attr.Attributes["QueueArn"])
	return nil
}

func (g *snsGroup) teardownSubscriptions(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sns_created_sub_arn"); arn != "" {
		g.sns().Unsubscribe(ctx, &sns.UnsubscribeInput{SubscriptionArn: aws.String(arn)}) //nolint:errcheck
	}
	if arn := t.GetString("sns_sub_topic_arn"); arn != "" {
		g.sns().DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: aws.String(arn)}) //nolint:errcheck
	}
	if url := t.GetString("sns_sub_q_url"); url != "" {
		g.sqs().DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(url)}) //nolint:errcheck
	}
	return nil
}

func (g *snsGroup) Subscribe(ctx context.Context, t *harness.TestContext) error {
	topicARN := t.GetString("sns_sub_topic_arn")
	qARN := t.GetString("sns_sub_q_arn")
	resp, err := g.sns().Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicARN), Protocol: aws.String("sqs"), Endpoint: aws.String(qARN),
	})
	if err != nil {
		return err
	}
	t.Set("sns_created_sub_arn", aws.ToString(resp.SubscriptionArn))
	return nil
}

func (g *snsGroup) SubscribeSQS(ctx context.Context, t *harness.TestContext) error {
	topicARN := t.GetString("sns_sub_topic_arn")
	qARN := t.GetString("sns_sub_q_arn")
	resp, err := g.sns().Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicARN), Protocol: aws.String("sqs"), Endpoint: aws.String(qARN),
	})
	if err != nil {
		return err
	}
	t.Set("sns_created_sub_arn", aws.ToString(resp.SubscriptionArn))
	return nil
}

func (g *snsGroup) GetSubscriptionAttributes(ctx context.Context, t *harness.TestContext) error {
	subARN := t.GetString("sns_created_sub_arn")
	if subARN == "" {
		return fmt.Errorf("GetSubscriptionAttributes: no subscription ARN in context")
	}
	resp, err := g.sns().GetSubscriptionAttributes(ctx, &sns.GetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subARN),
	})
	if err != nil {
		return err
	}
	if resp.Attributes["Protocol"] != "sqs" {
		return fmt.Errorf("GetSubscriptionAttributes: expected Protocol=sqs, got %q", resp.Attributes["Protocol"])
	}
	return nil
}

func (g *snsGroup) SetSubscriptionAttributes(ctx context.Context, t *harness.TestContext) error {
	subARN := t.GetString("sns_created_sub_arn")
	if subARN == "" {
		return fmt.Errorf("SetSubscriptionAttributes: no subscription ARN in context")
	}
	_, err := g.sns().SetSubscriptionAttributes(ctx, &sns.SetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subARN),
		AttributeName:   aws.String("RawMessageDelivery"),
		AttributeValue:  aws.String("true"),
	})
	if err != nil {
		return err
	}
	resp, err := g.sns().GetSubscriptionAttributes(ctx, &sns.GetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(subARN),
	})
	if err != nil {
		return err
	}
	if resp.Attributes["RawMessageDelivery"] != "true" {
		return fmt.Errorf("SetSubscriptionAttributes: RawMessageDelivery not set")
	}
	return nil
}

func (g *snsGroup) ListSubscriptions(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.sns().ListSubscriptions(ctx, &sns.ListSubscriptionsInput{})
	if err != nil {
		return err
	}
	subARN := t.GetString("sns_created_sub_arn")
	if subARN == "" {
		return nil
	}
	for _, sub := range resp.Subscriptions {
		if aws.ToString(sub.SubscriptionArn) == subARN {
			return nil
		}
	}
	return fmt.Errorf("ListSubscriptions: subscription %q not found", subARN)
}

func (g *snsGroup) ListSubscriptionsByTopic(ctx context.Context, t *harness.TestContext) error {
	topicARN := t.GetString("sns_sub_topic_arn")
	resp, err := g.sns().ListSubscriptionsByTopic(ctx, &sns.ListSubscriptionsByTopicInput{
		TopicArn: aws.String(topicARN),
	})
	if err != nil {
		return err
	}
	subARN := t.GetString("sns_created_sub_arn")
	if subARN == "" {
		return nil
	}
	for _, sub := range resp.Subscriptions {
		if aws.ToString(sub.SubscriptionArn) == subARN {
			return nil
		}
	}
	return fmt.Errorf("ListSubscriptionsByTopic: subscription %q not found", subARN)
}

func (g *snsGroup) Unsubscribe(ctx context.Context, t *harness.TestContext) error {
	subARN := t.GetString("sns_created_sub_arn")
	if subARN == "" {
		return fmt.Errorf("Unsubscribe: no subscription ARN in context")
	}
	_, err := g.sns().Unsubscribe(ctx, &sns.UnsubscribeInput{SubscriptionArn: aws.String(subARN)})
	if err != nil {
		return err
	}
	t.Set("sns_created_sub_arn", "")
	// Verify subscription is gone
	topicARN := t.GetString("sns_sub_topic_arn")
	list, lErr := g.sns().ListSubscriptionsByTopic(ctx, &sns.ListSubscriptionsByTopicInput{
		TopicArn: aws.String(topicARN),
	})
	if lErr != nil {
		return nil // ListSubscriptionsByTopic may fail if topic was deleted
	}
	for _, sub := range list.Subscriptions {
		if aws.ToString(sub.SubscriptionArn) == subARN {
			return fmt.Errorf("Unsubscribe: subscription %q still present", subARN)
		}
	}
	return nil
}
