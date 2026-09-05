package sns_test

// sns_lambda_permission_test.go — SNS → Lambda delivery under
// OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY.
//
// AWS checks the function's resource-based policy at delivery time, not at
// Subscribe: a subscription whose endpoint stops accepting SNS is a client-side
// error SNS does not retry, and "Amazon SNS discards the message—unless a
// dead-letter queue is attached to the subscription".
//
// https://docs.aws.amazon.com/sns/latest/dg/sns-dead-letter-queues.html

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// addLambdaPermission calls AddPermission over HTTP, as
// `aws lambda add-permission` does.
func addLambdaPermission(t *testing.T, srv *helpers.TestServer, functionName string, body map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the permission: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/2015-03-31/functions/"+functionName+"/policy", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
}

func TestPublish_lambdaSubscriberWithPermission_delivers(t *testing.T) {
	// Given: enforcement on, and the permission `aws lambda add-permission`
	// writes for an SNS subscriber
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	topicARN := createTopic(t, srv, "permitted-topic")
	fnARN := seedLambdaFunction(t, srv, "permitted-sns-handler")
	addLambdaPermission(t, srv, "permitted-sns-handler", map[string]any{
		"StatementId": "sns-invoke", "Action": "lambda:InvokeFunction",
		"Principal": "sns.amazonaws.com", "SourceArn": topicARN,
	})
	subscribe(t, srv, topicARN, "lambda", fnARN)

	// When: a message is published
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicARN},
		"Message":  {"hello lambda"},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the function is invoked
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		kvs, err := srv.Store.Scan(context.Background(), "lambda:invocations", "us-east-1/permitted-sns-handler:")
		return err == nil && len(kvs) > 0
	}, "timed out waiting for SNS to invoke the permitted lambda subscriber")
}

func TestPublish_lambdaSubscriberWithoutPermission_deadLetters(t *testing.T) {
	// Given: enforcement on, a real function with no AddPermission, and a
	// subscription DLQ
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	topicARN := createTopic(t, srv, "unpermitted-topic")
	dlqURL := sqsCreateQueue(t, srv, "unpermitted-dlq")
	fnARN := seedLambdaFunction(t, srv, "unpermitted-sns-handler")
	subARN := subscribe(t, srv, topicARN, "lambda", fnARN)

	setAttrResp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subARN},
		"AttributeName":   {"RedrivePolicy"},
		"AttributeValue":  {`{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:unpermitted-dlq"}`},
	})
	setAttrResp.Body.Close()
	helpers.AssertStatus(t, setAttrResp, http.StatusOK)

	// When: a message is published
	pubResp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicARN},
		"Message":  {"unauthorised"},
	})
	pubResp.Body.Close()
	// Publish itself still succeeds: the failure belongs to the delivery.
	helpers.AssertStatus(t, pubResp, http.StatusOK)

	// Then: the notification reaches the dead-letter queue…
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl": dlqURL, "MaxNumberOfMessages": 1,
		})
		defer recvResp.Body.Close()
		var recv struct {
			Messages []struct{ Body string } `json:"Messages"`
		}
		helpers.DecodeJSON(t, recvResp, &recv)
		return len(recv.Messages) > 0
	}, "timed out waiting for the unauthorised lambda delivery to reach the DLQ")

	// …and the function was never invoked
	kvs, err := srv.Store.Scan(context.Background(), "lambda:invocations", "us-east-1/unpermitted-sns-handler:")
	if err != nil {
		t.Fatalf("scanning invocations: %v", err)
	}
	if len(kvs) > 0 {
		t.Fatalf("expected no invocation, got %d", len(kvs))
	}
}

func TestPublish_lambdaSubscriberPermissionForAnotherTopic_deadLetters(t *testing.T) {
	// Given: a permission scoped to a different topic
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	topicARN := createTopic(t, srv, "actual-topic")
	dlqURL := sqsCreateQueue(t, srv, "wrong-topic-dlq")
	fnARN := seedLambdaFunction(t, srv, "wrong-topic-handler")
	addLambdaPermission(t, srv, "wrong-topic-handler", map[string]any{
		"StatementId": "sns-invoke", "Action": "lambda:InvokeFunction",
		"Principal": "sns.amazonaws.com",
		"SourceArn": "arn:aws:sns:us-east-1:000000000000:some-other-topic",
	})
	subARN := subscribe(t, srv, topicARN, "lambda", fnARN)

	setAttrResp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subARN},
		"AttributeName":   {"RedrivePolicy"},
		"AttributeValue":  {`{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:wrong-topic-dlq"}`},
	})
	setAttrResp.Body.Close()

	// When: a message is published
	pubResp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicARN}, "Message": {"wrong source"},
	})
	pubResp.Body.Close()

	// Then: the SourceArn condition fails and the message is dead-lettered
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl": dlqURL, "MaxNumberOfMessages": 1,
		})
		defer recvResp.Body.Close()
		var recv struct {
			Messages []struct{ Body string } `json:"Messages"`
		}
		helpers.DecodeJSON(t, recvResp, &recv)
		return len(recv.Messages) > 0
	}, "timed out waiting for the mismatched-source delivery to reach the DLQ")
}

func TestPublish_lambdaSubscriberEnforcementOff_needsNoPermission(t *testing.T) {
	// Given: the default server, with enforcement off
	srv := helpers.NewTestServer(t)
	topicARN := createTopic(t, srv, "unenforced-topic")
	fnARN := seedLambdaFunction(t, srv, "unenforced-sns-handler")
	subscribe(t, srv, topicARN, "lambda", fnARN)

	// When: a message is published with no permission anywhere
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicARN}, "Message": {"hello"},
	})
	resp.Body.Close()

	// Then: delivery is unchanged
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		kvs, err := srv.Store.Scan(context.Background(), "lambda:invocations", "us-east-1/unenforced-sns-handler:")
		return err == nil && len(kvs) > 0
	}, "timed out waiting for the unenforced lambda delivery")
}
