// Package sns_test contains integration tests for the SNS service emulator.
//
// TDD contract: every handler in internal/services/sns/ must have a
// corresponding failing test here before implementation begins.
//
// Run: go test ./tests/integration/sns/...
package sns_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ---- CreateTopic -----------------------------------------------------------

func TestCreateTopic_success(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := snsCall(t, srv, "CreateTopic", url.Values{"Name": {"my-topic"}})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName xml.Name `xml:"CreateTopicResponse"`
		Result  struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"CreateTopicResult"`
	}
	decodeXML(t, resp, &result)

	if result.Result.TopicArn == "" {
		t.Error("expected TopicArn to be set")
	}
	if !strings.Contains(result.Result.TopicArn, "my-topic") {
		t.Errorf("expected TopicArn to contain 'my-topic', got %q", result.Result.TopicArn)
	}
}

func TestCreateTopic_missingName(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "CreateTopic", url.Values{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateTopic_idempotent(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn1 := createTopic(t, srv, "dupe-topic")
	arn2 := createTopic(t, srv, "dupe-topic")
	if arn1 != arn2 {
		t.Errorf("idempotent CreateTopic should return same ARN: %q vs %q", arn1, arn2)
	}
}

// ---- DeleteTopic -----------------------------------------------------------

func TestDeleteTopic_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createTopic(t, srv, "delete-me")

	resp := snsCall(t, srv, "DeleteTopic", url.Values{"TopicArn": {arn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Topic should no longer appear in ListTopics.
	listResp := snsCall(t, srv, "ListTopics", url.Values{})
	defer listResp.Body.Close()
	var list struct {
		Result struct {
			Topics []struct{ TopicArn string } `xml:"Topics>member"`
		} `xml:"ListTopicsResult"`
	}
	decodeXML(t, listResp, &list)
	for _, t2 := range list.Result.Topics {
		if t2.TopicArn == arn {
			t.Errorf("topic %q should have been deleted", arn)
		}
	}
}

func TestDeleteTopic_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "DeleteTopic", url.Values{
		"TopicArn": {"arn:aws:sns:us-east-1:000000000000:no-such-topic"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- ListTopics ------------------------------------------------------------

func TestListTopics_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "ListTopics", url.Values{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Topics []struct{ TopicArn string } `xml:"Topics>member"`
		} `xml:"ListTopicsResult"`
	}
	decodeXML(t, resp, &result)
	if len(result.Result.Topics) != 0 {
		t.Errorf("expected empty list, got %d", len(result.Result.Topics))
	}
}

func TestListTopics_returnsAll(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTopic(t, srv, "topic-a")
	createTopic(t, srv, "topic-b")
	createTopic(t, srv, "topic-c")

	resp := snsCall(t, srv, "ListTopics", url.Values{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Topics []struct{ TopicArn string } `xml:"Topics>member"`
		} `xml:"ListTopicsResult"`
	}
	decodeXML(t, resp, &result)
	if len(result.Result.Topics) != 3 {
		t.Errorf("expected 3 topics, got %d", len(result.Result.Topics))
	}
}

// ---- Subscribe -------------------------------------------------------------

func TestSubscribe_sqsProtocol(t *testing.T) {
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "my-topic")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:my-queue"

	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicArn},
		"Protocol": {"sqs"},
		"Endpoint": {queueArn},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"SubscribeResult"`
	}
	decodeXML(t, resp, &result)
	if result.Result.SubscriptionArn == "" {
		t.Error("expected SubscriptionArn to be set")
	}
	if !strings.Contains(result.Result.SubscriptionArn, "my-topic") {
		t.Errorf("expected SubscriptionArn to contain topic name, got %q", result.Result.SubscriptionArn)
	}
}

func TestSubscribe_topicNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {"arn:aws:sns:us-east-1:000000000000:no-such-topic"},
		"Protocol": {"sqs"},
		"Endpoint": {"arn:aws:sqs:us-east-1:000000000000:my-queue"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestSubscribe_crossRegion(t *testing.T) {
	// Given: a topic in the test server's default region (us-east-1)
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "cross-region-topic")

	// When: Subscribe is called with an SQS endpoint in a different region
	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicArn},
		"Protocol": {"sqs"},
		"Endpoint": {"arn:aws:sqs:eu-west-1:000000000000:my-queue"},
	})
	defer resp.Body.Close()

	// Then: real AWS rejects cross-region subscriptions with InvalidParameter
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestSubscribe_smsProtocol(t *testing.T) {
	// Given: a topic exists.
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "sms-topic")

	// When: Subscribe is called with the sms protocol.
	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicArn},
		"Protocol": {"sms"},
		"Endpoint": {"+12125551234"},
	})
	defer resp.Body.Close()

	// Then: 200 OK with a SubscriptionArn.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"SubscribeResult"`
	}
	decodeXML(t, resp, &result)
	if result.Result.SubscriptionArn == "" {
		t.Error("expected SubscriptionArn to be set")
	}
}

func TestSubscribe_applicationProtocol_returns400(t *testing.T) {
	// Given: a topic and an "application" protocol (mobile push — not supported).
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "app-topic")

	// When: Subscribe is called with the application protocol.
	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicArn},
		"Protocol": {"application"},
		"Endpoint": {"arn:aws:sns:us-east-1:000000000000:endpoint/GCM/app/token"},
	})
	defer resp.Body.Close()

	// Then: 400 InvalidParameter.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestSubscribe_firehoseProtocol_returns400(t *testing.T) {
	// Given: a topic and a "firehose" protocol (Kinesis Data Firehose — not supported).
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "firehose-topic")

	// When: Subscribe is called with the firehose protocol.
	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicArn},
		"Protocol": {"firehose"},
		"Endpoint": {"arn:aws:firehose:us-east-1:000000000000:deliverystream/my-stream"},
	})
	defer resp.Body.Close()

	// Then: 400 InvalidParameter.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- Unsubscribe -----------------------------------------------------------

func TestUnsubscribe_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "unsub-topic")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:my-queue"
	subArn := subscribe(t, srv, topicArn, "sqs", queueArn)

	resp := snsCall(t, srv, "Unsubscribe", url.Values{
		"SubscriptionArn": {subArn},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Should no longer be in list.
	listResp := snsCall(t, srv, "ListSubscriptionsByTopic", url.Values{
		"TopicArn": {topicArn},
	})
	defer listResp.Body.Close()
	var list struct {
		Result struct {
			Subscriptions []struct{ SubscriptionArn string } `xml:"Subscriptions>member"`
		} `xml:"ListSubscriptionsByTopicResult"`
	}
	decodeXML(t, listResp, &list)
	for _, s := range list.Result.Subscriptions {
		if s.SubscriptionArn == subArn {
			t.Errorf("subscription %q should have been removed", subArn)
		}
	}
}

// ---- ListSubscriptionsByTopic ----------------------------------------------

func TestListSubscriptionsByTopic_returnsAll(t *testing.T) {
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "fan-topic")
	subscribe(t, srv, topicArn, "sqs", "arn:aws:sqs:us-east-1:000000000000:queue-a")
	subscribe(t, srv, topicArn, "sqs", "arn:aws:sqs:us-east-1:000000000000:queue-b")

	resp := snsCall(t, srv, "ListSubscriptionsByTopic", url.Values{
		"TopicArn": {topicArn},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Subscriptions []struct {
				SubscriptionArn string `xml:"SubscriptionArn"`
				Protocol        string `xml:"Protocol"`
				Endpoint        string `xml:"Endpoint"`
			} `xml:"Subscriptions>member"`
		} `xml:"ListSubscriptionsByTopicResult"`
	}
	decodeXML(t, resp, &result)
	if len(result.Result.Subscriptions) != 2 {
		t.Errorf("expected 2 subscriptions, got %d", len(result.Result.Subscriptions))
	}
}

// ---- Publish → SQS fan-out -------------------------------------------------

func TestPublish_deliversToSQSSubscriber(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create topic and queue.
	topicArn := createTopic(t, srv, "notify-topic")
	queueURL := sqsCreateQueue(t, srv, "notify-queue")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:notify-queue"
	subscribe(t, srv, topicArn, "sqs", queueArn)

	// Publish a message.
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicArn},
		"Message":  {"hello from sns"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var pub struct {
		Result struct {
			MessageId string `xml:"MessageId"`
		} `xml:"PublishResult"`
	}
	decodeXML(t, resp, &pub)
	if pub.Result.MessageId == "" {
		t.Error("expected MessageId in Publish response")
	}

	// ReceiveMessage from SQS — delivery is async, so poll until it arrives.
	var recv struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": 1,
		})
		defer recvResp.Body.Close()
		recv = struct {
			Messages []struct {
				Body string `json:"Body"`
			} `json:"Messages"`
		}{}
		helpers.DecodeJSON(t, recvResp, &recv)
		return len(recv.Messages) == 1
	}, "timed out waiting for SNS message to arrive in SQS")

	// The body should be the SNS notification envelope.
	var envelope struct {
		Type     string `json:"Type"`
		Message  string `json:"Message"`
		TopicArn string `json:"TopicArn"`
	}
	if err := decodeJSONString(recv.Messages[0].Body, &envelope); err != nil {
		t.Fatalf("expected SQS body to be SNS envelope JSON: %v\nbody: %s", err, recv.Messages[0].Body)
	}
	if envelope.Type != "Notification" {
		t.Errorf("expected Type=Notification, got %q", envelope.Type)
	}
	if envelope.Message != "hello from sns" {
		t.Errorf("expected Message='hello from sns', got %q", envelope.Message)
	}
	if envelope.TopicArn != topicArn {
		t.Errorf("expected TopicArn=%q, got %q", topicArn, envelope.TopicArn)
	}
}

func TestPublish_fanOutToMultipleQueues(t *testing.T) {
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "fanout-topic")

	queueURL1 := sqsCreateQueue(t, srv, "fan-a")
	queueURL2 := sqsCreateQueue(t, srv, "fan-b")
	subscribe(t, srv, topicArn, "sqs", "arn:aws:sqs:us-east-1:000000000000:fan-a")
	subscribe(t, srv, topicArn, "sqs", "arn:aws:sqs:us-east-1:000000000000:fan-b")

	snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicArn},
		"Message":  {"broadcast"},
	})

	// Delivery is async — poll each queue until the message arrives.
	for _, u := range []string{queueURL1, queueURL2} {
		queueURL := u
		helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
			recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
				"QueueUrl":            queueURL,
				"MaxNumberOfMessages": 1,
			})
			defer recvResp.Body.Close()
			var recv struct {
				Messages []struct{ Body string } `json:"Messages"`
			}
			helpers.DecodeJSON(t, recvResp, &recv)
			return len(recv.Messages) == 1
		}, "timed out waiting for SNS message in queue "+queueURL)
	}
}

func TestPublish_topicNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {"arn:aws:sns:us-east-1:000000000000:no-such-topic"},
		"Message":  {"test"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestPublish_snsOnlyServiceSet(t *testing.T) {
	// Given: SNS is enabled without SQS fan-out support.
	srv := helpers.NewTestServer(t, helpers.WithServices("sns"))
	topicArn := createTopic(t, srv, "sns-only-topic")

	// When: a message is published to the topic.
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicArn},
		"Message":  {"hello without sqs"},
	})
	defer resp.Body.Close()

	// Then: Publish still succeeds and returns a message ID.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	var pub struct {
		Result struct {
			MessageId string `xml:"MessageId"`
		} `xml:"PublishResult"`
	}
	decodeXML(t, resp, &pub)
	if pub.Result.MessageId == "" {
		t.Error("expected MessageId in Publish response")
	}
}

// ---- PublishBatch ----------------------------------------------------------

func TestPublishBatch_returnsMessageIdsForEachEntry(t *testing.T) {
	// Given: a topic with an SQS subscriber
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "batch-topic")
	queueURL := sqsCreateQueue(t, srv, "batch-queue")
	_ = queueURL
	subscribe(t, srv, topicArn, "sqs", "arn:aws:sqs:us-east-1:000000000000:batch-queue")

	// When: PublishBatch is called with two entries
	resp := snsCall(t, srv, "PublishBatch", url.Values{
		"TopicArn":                                    {topicArn},
		"PublishBatchRequestEntries.member.1.Id":      {"msg-1"},
		"PublishBatchRequestEntries.member.1.Message": {"first message"},
		"PublishBatchRequestEntries.member.2.Id":      {"msg-2"},
		"PublishBatchRequestEntries.member.2.Message": {"second message"},
	})
	defer resp.Body.Close()

	// Then: HTTP 200 with Successful entries in the response
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Successful []struct {
				Id        string `xml:"Id"`
				MessageId string `xml:"MessageId"`
			} `xml:"Successful>member"`
			Failed []struct {
				Id string `xml:"Id"`
			} `xml:"Failed>member"`
		} `xml:"PublishBatchResult"`
	}
	decodeXML(t, resp, &result)
	if len(result.Result.Successful) != 2 {
		t.Fatalf("expected 2 successful entries, got %d", len(result.Result.Successful))
	}
	for _, s := range result.Result.Successful {
		if s.Id == "" || s.MessageId == "" {
			t.Errorf("expected non-empty Id and MessageId, got Id=%q MessageId=%q", s.Id, s.MessageId)
		}
	}
	if len(result.Result.Failed) != 0 {
		t.Errorf("expected no failed entries, got %d", len(result.Result.Failed))
	}
}

func TestPublishBatch_deliversAllMessagesToSQS(t *testing.T) {
	// Given: a topic with a subscribed queue
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "batch-deliver-topic")
	queueURL := sqsCreateQueue(t, srv, "batch-deliver-queue")
	subscribe(t, srv, topicArn, "sqs", "arn:aws:sqs:us-east-1:000000000000:batch-deliver-queue")

	// When: PublishBatch sends two messages
	snsCall(t, srv, "PublishBatch", url.Values{
		"TopicArn":                                    {topicArn},
		"PublishBatchRequestEntries.member.1.Id":      {"a"},
		"PublishBatchRequestEntries.member.1.Message": {"msg-a"},
		"PublishBatchRequestEntries.member.2.Id":      {"b"},
		"PublishBatchRequestEntries.member.2.Message": {"msg-b"},
	})

	// Then: both messages arrive in the queue (delivery is async, so poll).
	seen := map[string]bool{}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": 10,
		})
		defer recvResp.Body.Close()
		var recv struct {
			Messages []struct{ Body string } `json:"Messages"`
		}
		helpers.DecodeJSON(t, recvResp, &recv)
		for _, msg := range recv.Messages {
			var envelope struct {
				Message string `json:"Message"`
			}
			if err := decodeJSONString(msg.Body, &envelope); err == nil {
				seen[envelope.Message] = true
			}
		}
		return seen["msg-a"] && seen["msg-b"]
	}, "timed out waiting for 2 batch messages in SQS queue")
}

func TestPublishBatch_topicNotFound_returns404(t *testing.T) {
	// Given: no topic exists
	srv := helpers.NewTestServer(t)

	// When: PublishBatch is called with a non-existent topic
	resp := snsCall(t, srv, "PublishBatch", url.Values{
		"TopicArn":                                    {"arn:aws:sns:us-east-1:000000000000:no-such-topic"},
		"PublishBatchRequestEntries.member.1.Id":      {"x"},
		"PublishBatchRequestEntries.member.1.Message": {"test"},
	})
	defer resp.Body.Close()

	// Then: 404 Not Found
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestGetTopicAttributes_returnsArn(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createTopic(t, srv, "attrs-topic")

	resp := snsCall(t, srv, "GetTopicAttributes", url.Values{
		"TopicArn": {arn},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Attributes []struct {
				Key   string `xml:"key"`
				Value string `xml:"value"`
			} `xml:"Attributes>entry"`
		} `xml:"GetTopicAttributesResult"`
	}
	decodeXML(t, resp, &result)
	var gotARN string
	for _, e := range result.Result.Attributes {
		if e.Key == "TopicArn" {
			gotARN = e.Value
		}
	}
	if gotARN != arn {
		t.Errorf("expected TopicArn=%q, got %q", arn, gotARN)
	}
}

// ---- Request ID on every response ------------------------------------------

func TestEveryResponse_hasRequestID(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createTopic(t, srv, "hdr-topic")
	resp := snsCall(t, srv, "ListTopics", url.Values{})
	defer resp.Body.Close()
	helpers.AssertRequestID(t, resp)
	_ = arn
}

// ---- email / email-json delivery -------------------------------------------

func TestPublish_deliversToEmailSubscriber(t *testing.T) {
	// Given: a topic and an email subscriber, with the SMTP mock enabled.
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock())
	arn := createTopic(t, srv, "email-topic")
	subscribe(t, srv, arn, "email", "user@example.com")

	// When: a message is published with a subject.
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {arn},
		"Message":  {"Hello from SNS"},
		"Subject":  {"SNS Alert"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the captured mail inbox contains the message (delivery is async, so poll).
	var msgs []struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"textBody"`
	}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		mailResp, err := http.Get(srv.URL + "/_overcast/inbox/messages")
		if err != nil {
			return false
		}
		defer mailResp.Body.Close()
		msgs = nil
		helpers.DecodeJSON(t, mailResp, &msgs)
		return len(msgs) == 1
	}, "timed out waiting for email delivery")

	if msgs[0].Subject != "SNS Alert" {
		t.Errorf("Subject = %q, want %q", msgs[0].Subject, "SNS Alert")
	}
	if len(msgs[0].To) == 0 || msgs[0].To[0] != "user@example.com" {
		t.Errorf("To = %v, want [user@example.com]", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Body, "Hello from SNS") {
		t.Errorf("body %q should contain %q", msgs[0].Body, "Hello from SNS")
	}
}

func TestPublish_deliversToEmailJSONSubscriber(t *testing.T) {
	// Given: a topic and an email-json subscriber.
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock())
	arn := createTopic(t, srv, "emailjson-topic")
	subscribe(t, srv, arn, "email-json", "dev@example.com")

	// When: a message is published.
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {arn},
		"Message":  {"raw payload"},
		"Subject":  {"JSON notification"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the captured body is the full SNS JSON envelope (delivery is async, so poll).
	var msgs []struct {
		Body string `json:"textBody"`
	}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		mailResp, err := http.Get(srv.URL + "/_overcast/inbox/messages")
		if err != nil {
			return false
		}
		defer mailResp.Body.Close()
		msgs = nil
		helpers.DecodeJSON(t, mailResp, &msgs)
		return len(msgs) == 1
	}, "timed out waiting for email-json delivery")
	// body should be a valid JSON envelope containing the original message.
	var envelope struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal([]byte(msgs[0].Body), &envelope); err != nil {
		t.Fatalf("email body is not valid JSON envelope: %v\nbody: %s", err, msgs[0].Body)
	}
	if envelope.Type != "Notification" {
		t.Errorf("Type = %q, want Notification", envelope.Type)
	}
	if envelope.Message != "raw payload" {
		t.Errorf("Message = %q, want %q", envelope.Message, "raw payload")
	}
}

// ---- sms delivery ----------------------------------------------------------

func TestPublish_deliversToSMSSubscriber(t *testing.T) {
	// Given: a topic and an SMS subscriber, with the SMTP mock enabled.
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock())
	topicArn := createTopic(t, srv, "sms-delivery-topic")
	subscribe(t, srv, topicArn, "sms", "+12125551234")

	// When: a message is published.
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicArn},
		"Message":  {"Hello via SMS"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the inbox contains an SMS message (delivery is async, so poll).
	var msgs []struct {
		Kind string   `json:"kind"`
		To   []string `json:"to"`
		Body string   `json:"textBody"`
	}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		mailResp, err := http.Get(srv.URL + "/_overcast/inbox/messages")
		if err != nil {
			return false
		}
		defer mailResp.Body.Close()
		msgs = nil
		helpers.DecodeJSON(t, mailResp, &msgs)
		return len(msgs) == 1
	}, "timed out waiting for SMS delivery")

	if msgs[0].Kind != "sms" {
		t.Errorf("Kind = %q, want sms", msgs[0].Kind)
	}
	if len(msgs[0].To) == 0 || msgs[0].To[0] != "+12125551234" {
		t.Errorf("To = %v, want [+12125551234]", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Body, "Hello via SMS") {
		t.Errorf("body %q should contain 'Hello via SMS'", msgs[0].Body)
	}
}

func TestMailCapture_clearAndDelete(t *testing.T) {
	// Given: two captured emails.
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock())
	arn := createTopic(t, srv, "clear-topic")
	subscribe(t, srv, arn, "email", "a@example.com")
	for _, msg := range []string{"msg1", "msg2"} {
		resp := snsCall(t, srv, "Publish", url.Values{"TopicArn": {arn}, "Message": {msg}})
		resp.Body.Close()
	}

	// Assert 2 messages (delivery is async, so poll).
	var msgs []struct {
		ID string `json:"id"`
	}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		listResp, _ := http.Get(srv.URL + "/_overcast/inbox/messages")
		msgs = nil
		helpers.DecodeJSON(t, listResp, &msgs)
		listResp.Body.Close()
		return len(msgs) == 2
	}, "timed out waiting for 2 captured emails")

	// Delete one.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/_overcast/inbox/messages/"+msgs[0].ID, nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE message: %v", err)
	}
	delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusNoContent)

	listResp2, _ := http.Get(srv.URL + "/_overcast/inbox/messages")
	var msgs2 []struct {
		ID string `json:"id"`
	}
	helpers.DecodeJSON(t, listResp2, &msgs2)
	listResp2.Body.Close()
	if len(msgs2) != 1 {
		t.Fatalf("expected 1 message after delete, got %d", len(msgs2))
	}

	// Clear all.
	clearReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/_overcast/inbox/messages", nil)
	clearResp, _ := http.DefaultClient.Do(clearReq)
	clearResp.Body.Close()

	listResp3, _ := http.Get(srv.URL + "/_overcast/inbox/messages")
	var msgs3 []struct{}
	helpers.DecodeJSON(t, listResp3, &msgs3)
	listResp3.Body.Close()
	if len(msgs3) != 0 {
		t.Fatalf("expected 0 messages after clear, got %d", len(msgs3))
	}
}

// ---- GetSubscriptionAttributes / SetSubscriptionAttributes ----------------

func TestGetSubscriptionAttributes_returnsBaseFields(t *testing.T) {
	// Given: a topic and an SQS subscription
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "attr-q")
	arn := createTopic(t, srv, "attr-topic")
	subArn := subscribe(t, srv, arn, "sqs", qURL)

	// When: GetSubscriptionAttributes is called
	resp := snsCall(t, srv, "GetSubscriptionAttributes", url.Values{"SubscriptionArn": {subArn}})
	defer resp.Body.Close()

	// Then: 200 OK with core attributes
	helpers.AssertStatus(t, resp, http.StatusOK)
	attrs := decodeAttributeMap(t, resp)
	if attrs["SubscriptionArn"] != subArn {
		t.Errorf("SubscriptionArn = %q, want %q", attrs["SubscriptionArn"], subArn)
	}
	if attrs["TopicArn"] != arn {
		t.Errorf("TopicArn = %q, want %q", attrs["TopicArn"], arn)
	}
	if attrs["Protocol"] != "sqs" {
		t.Errorf("Protocol = %q, want sqs", attrs["Protocol"])
	}
}

func TestGetSubscriptionAttributes_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "GetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {"arn:aws:sns:us-east-1:000000000000:no-topic:no-id"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestSetSubscriptionAttributes_storesAndRetrieves(t *testing.T) {
	// Given: a subscription
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "set-attr-q")
	arn := createTopic(t, srv, "set-attr-topic")
	subArn := subscribe(t, srv, arn, "sqs", qURL)

	// When: SetSubscriptionAttributes sets RawMessageDelivery
	resp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subArn},
		"AttributeName":   {"RawMessageDelivery"},
		"AttributeValue":  {"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: GetSubscriptionAttributes reflects the new value
	getResp := snsCall(t, srv, "GetSubscriptionAttributes", url.Values{"SubscriptionArn": {subArn}})
	defer getResp.Body.Close()
	attrs := decodeAttributeMap(t, getResp)
	if attrs["RawMessageDelivery"] != "true" {
		t.Errorf("RawMessageDelivery = %q, want true", attrs["RawMessageDelivery"])
	}
}

func TestSetSubscriptionAttributes_filterPolicy(t *testing.T) {
	// Given: a subscription
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "fp-q")
	arn := createTopic(t, srv, "fp-topic")
	subArn := subscribe(t, srv, arn, "sqs", qURL)

	// When: a filter policy is set
	fp := `{"eventType":["order_placed","order_cancelled"]}`
	resp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subArn},
		"AttributeName":   {"FilterPolicy"},
		"AttributeValue":  {fp},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: GetSubscriptionAttributes returns it
	getResp := snsCall(t, srv, "GetSubscriptionAttributes", url.Values{"SubscriptionArn": {subArn}})
	defer getResp.Body.Close()
	attrs := decodeAttributeMap(t, getResp)
	if attrs["FilterPolicy"] != fp {
		t.Errorf("FilterPolicy = %q, want %q", attrs["FilterPolicy"], fp)
	}
}

func TestSetSubscriptionAttributes_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {"arn:aws:sns:us-east-1:000000000000:no-topic:no-id"},
		"AttributeName":   {"RawMessageDelivery"},
		"AttributeValue":  {"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- ConfirmSubscription ---------------------------------------------------

func TestConfirmSubscription_success(t *testing.T) {
	// Given: a topic exists
	srv := helpers.NewTestServer(t)
	arn := createTopic(t, srv, "confirm-topic")

	// When: ConfirmSubscription is called with any token
	resp := snsCall(t, srv, "ConfirmSubscription", url.Values{
		"TopicArn": {arn},
		"Token":    {"any-token-value"},
	})
	defer resp.Body.Close()

	// Then: 200 OK with a SubscriptionArn
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"ConfirmSubscriptionResult"`
	}
	decodeXML(t, resp, &result)
	if result.Result.SubscriptionArn == "" {
		t.Error("expected non-empty SubscriptionArn")
	}
}

func TestConfirmSubscription_topicNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "ConfirmSubscription", url.Values{
		"TopicArn": {"arn:aws:sns:us-east-1:000000000000:no-such"},
		"Token":    {"tok"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- Message filtering -----------------------------------------------------

func TestPublish_filterPolicy_matchesDelivery(t *testing.T) {
	// Given: a queue subscribed to a topic with a filter policy
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "filter-match-q")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:filter-match-q"
	arn := createTopic(t, srv, "filter-topic")
	subArn := subscribe(t, srv, arn, "sqs", queueArn)

	// Set filter: only deliver when eventType = "order_placed"
	setResp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subArn},
		"AttributeName":   {"FilterPolicy"},
		"AttributeValue":  {`{"eventType":["order_placed"]}`},
	})
	setResp.Body.Close()

	// When: publish with matching attribute
	publishResp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn":                       {arn},
		"Message":                        {"matched message"},
		"MessageAttributes.entry.1.Name": {"eventType"},
		"MessageAttributes.entry.1.Value.DataType":    {"String"},
		"MessageAttributes.entry.1.Value.StringValue": {"order_placed"},
	})
	publishResp.Body.Close()

	// Then: message is delivered. SNS fan-out is asynchronous, so poll rather
	// than peeking immediately on slower CI runners.
	var msgs []map[string]any
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		msgs = sqsPeekMessages(t, srv, qURL)
		return len(msgs) == 1
	}, "timed out waiting for matching filtered SNS message in SQS")
	if len(msgs) != 1 {
		t.Errorf("expected 1 delivered message, got %d", len(msgs))
	}
}

func TestPublish_filterPolicy_noMatchSkipsDelivery(t *testing.T) {
	// Given: a queue subscribed to a topic with a filter policy
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "filter-nomatch-q")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:filter-nomatch-q"
	arn := createTopic(t, srv, "filter-nomatch-topic")
	subArn := subscribe(t, srv, arn, "sqs", queueArn)

	// Set filter: only order_placed
	setResp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subArn},
		"AttributeName":   {"FilterPolicy"},
		"AttributeValue":  {`{"eventType":["order_placed"]}`},
	})
	setResp.Body.Close()

	// When: publish with non-matching attribute
	publishResp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn":                       {arn},
		"Message":                        {"non-matching message"},
		"MessageAttributes.entry.1.Name": {"eventType"},
		"MessageAttributes.entry.1.Value.DataType":    {"String"},
		"MessageAttributes.entry.1.Value.StringValue": {"order_shipped"},
	})
	publishResp.Body.Close()

	// Then: no message delivered
	msgs := sqsPeekMessages(t, srv, qURL)
	if len(msgs) != 0 {
		t.Errorf("expected 0 delivered messages, got %d", len(msgs))
	}
}

func TestPublish_filterPolicy_noAttributeSkipsDelivery(t *testing.T) {
	// Given: a subscription with a filter policy
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "filter-noattr-q")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:filter-noattr-q"
	arn := createTopic(t, srv, "filter-noattr-topic")
	subArn := subscribe(t, srv, arn, "sqs", queueArn)

	setResp := snsCall(t, srv, "SetSubscriptionAttributes", url.Values{
		"SubscriptionArn": {subArn},
		"AttributeName":   {"FilterPolicy"},
		"AttributeValue":  {`{"eventType":["order_placed"]}`},
	})
	setResp.Body.Close()

	// When: publish with no message attributes
	publishResp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {arn},
		"Message":  {"bare message"},
	})
	publishResp.Body.Close()

	// Then: no message delivered (filter policy requires eventType)
	msgs := sqsPeekMessages(t, srv, qURL)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages without matching attribute, got %d", len(msgs))
	}
}

func TestPublish_noFilterPolicy_alwaysDelivers(t *testing.T) {
	// Given: a subscription with no filter policy
	srv := helpers.NewTestServer(t)
	qURL := sqsCreateQueue(t, srv, "nofilter-q")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:nofilter-q"
	arn := createTopic(t, srv, "nofilter-topic")
	subscribe(t, srv, arn, "sqs", queueArn)

	// When: publish (no message attributes)
	publishResp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {arn},
		"Message":  {"always delivered"},
	})
	publishResp.Body.Close()

	// Then: message is delivered
	var msgs []map[string]any
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		msgs = sqsPeekMessages(t, srv, qURL)
		return len(msgs) == 1
	}, "timed out waiting for SNS message to arrive in SQS")
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

// ---- UnsubscribeURL --------------------------------------------------------

func TestPublish_unsubscribeURLContainsSubscriptionArn(t *testing.T) {
	// Given: a topic and an SQS subscriber.
	srv := helpers.NewTestServer(t)
	topicArn := createTopic(t, srv, "unsub-url-topic")
	queueURL := sqsCreateQueue(t, srv, "unsub-url-queue")
	queueArn := "arn:aws:sqs:us-east-1:000000000000:unsub-url-queue"
	subARN := subscribe(t, srv, topicArn, "sqs", queueArn)

	// When: a message is published.
	snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicArn},
		"Message":  {"test unsubscribe url"},
	}).Body.Close()

	// Then: the delivered envelope's UnsubscribeURL references the subscription ARN
	// (not the topic ARN) and points to the correct Action.
	var recv struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.Eventually(t, 2*time.Second, 10*time.Millisecond, func() bool {
		recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": 1,
		})
		defer recvResp.Body.Close()
		recv = struct {
			Messages []struct {
				Body string `json:"Body"`
			} `json:"Messages"`
		}{}
		helpers.DecodeJSON(t, recvResp, &recv)
		return len(recv.Messages) == 1
	}, "timed out waiting for message in SQS")

	var envelope struct {
		UnsubscribeURL string `json:"UnsubscribeURL"`
	}
	if err := json.Unmarshal([]byte(recv.Messages[0].Body), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	u, err := url.Parse(envelope.UnsubscribeURL)
	if err != nil {
		t.Fatalf("parse UnsubscribeURL %q: %v", envelope.UnsubscribeURL, err)
	}
	if got := u.Query().Get("SubscriptionArn"); got != subARN {
		t.Errorf("UnsubscribeURL SubscriptionArn = %q, want subscription ARN %q (not topic ARN)", got, subARN)
	}
	if got := u.Query().Get("Action"); got != "Unsubscribe" {
		t.Errorf("UnsubscribeURL Action = %q, want Unsubscribe", got)
	}

	// And: issuing a GET to the URL path+query removes the subscription.
	getResp, err := http.Get(srv.URL + u.RequestURI())
	if err != nil {
		t.Fatalf("GET UnsubscribeURL: %v", err)
	}
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)

	listResp := snsCall(t, srv, "ListSubscriptionsByTopic", url.Values{"TopicArn": {topicArn}})
	defer listResp.Body.Close()
	var list struct {
		Result struct {
			Subscriptions []struct {
				SubscriptionArn string `xml:"SubscriptionArn"`
			} `xml:"Subscriptions>member"`
		} `xml:"ListSubscriptionsByTopicResult"`
	}
	decodeXML(t, listResp, &list)
	for _, s := range list.Result.Subscriptions {
		if s.SubscriptionArn == subARN {
			t.Errorf("subscription %q should have been removed by UnsubscribeURL GET", subARN)
		}
	}
}

// ---- Test helpers ----------------------------------------------------------

// snsCall sends an AWS Query-protocol SNS request (form-encoded POST body).
func snsCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	params.Set("Version", "2010-03-31")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("build SNS request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SNS call %q: %v", action, err)
	}
	return resp
}

// decodeXML reads and XML-decodes a response body into v.
func decodeXML(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if err := xml.Unmarshal(body, v); err != nil {
		t.Fatalf("decode XML: %v\nbody: %s", err, body)
	}
}

// decodeJSONString decodes a JSON string into v.
func decodeJSONString(s string, v any) error {
	return decodeJSONBytes([]byte(s), v)
}

func decodeJSONBytes(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

func sqsCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SQS call %q: %v", action, err)
	}
	return resp
}

func createTopic(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := snsCall(t, srv, "CreateTopic", url.Values{"Name": {name}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createTopic %q: unexpected status %d", name, resp.StatusCode)
	}
	var result struct {
		Result struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"CreateTopicResult"`
	}
	decodeXML(t, resp, &result)
	return result.Result.TopicArn
}

func subscribe(t *testing.T, srv *helpers.TestServer, topicArn, protocol, endpoint string) string {
	t.Helper()
	resp := snsCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicArn},
		"Protocol": {protocol},
		"Endpoint": {endpoint},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe %q→%q: unexpected status %d", topicArn, endpoint, resp.StatusCode)
	}
	var result struct {
		Result struct {
			SubscriptionArn string `xml:"SubscriptionArn"`
		} `xml:"SubscribeResult"`
	}
	decodeXML(t, resp, &result)
	return result.Result.SubscriptionArn
}

func sqsCreateQueue(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{"QueueName": name})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sqsCreateQueue %q: unexpected status %d", name, resp.StatusCode)
	}
	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.QueueUrl
}

// decodeAttributeMap decodes the standard AWS attribute map (Attributes/entry/key+value)
// from an XML response body. responseTag and resultTag are the outer XML element names.
func decodeAttributeMap(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Generic structure to decode the nested map.
	type entry struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	}
	type result struct {
		Attributes []entry `xml:"Attributes>entry"`
	}
	type root struct {
		Result result `xml:"GetSubscriptionAttributesResult"`
	}
	var r root
	if err := xml.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode attributes XML: %v\nbody: %s", err, body)
	}
	m := make(map[string]string, len(r.Result.Attributes))
	for _, e := range r.Result.Attributes {
		m[e.Key] = e.Value
	}
	return m
}

// sqsPeekMessages uses the SQS ReceiveMessage call to peek at messages on a queue.
func sqsPeekMessages(t *testing.T, srv *helpers.TestServer, queueURL string) []map[string]any {
	t.Helper()
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
		"VisibilityTimeout":   0,
	})
	defer resp.Body.Close()
	var result struct {
		Messages []map[string]any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Messages
}
