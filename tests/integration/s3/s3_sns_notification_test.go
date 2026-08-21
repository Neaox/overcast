// S3 → SNS bucket notifications.
//
// A TopicConfiguration in a bucket's notification configuration routes matching
// object events to an SNS topic. Real S3 publishes the standard notification
// JSON ({"Records":[…]}) as the SNS Message — a JSON string inside the SNS
// notification envelope, not a structured payload — with Subject
// "Amazon S3 Notification". A queue subscribed to the topic therefore receives
// the SNS envelope, whose Message field parses back into the S3 event.
//
// Event-type selection and prefix/suffix filters share the dispatcher's one
// matcher with the SQS and Lambda destinations, so their semantics are
// identical by construction; the negative tests below pin that behaviour at
// the wire.
package s3_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ---- SNS helpers -----------------------------------------------------------

// snsQueryCall sends an AWS Query-protocol SNS request (form-encoded POST).
func snsQueryCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
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

// createTopicForS3 creates an SNS topic and returns its ARN.
func createTopicForS3(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := snsQueryCall(t, srv, "CreateTopic", url.Values{"Name": {name}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"CreateTopicResult"`
	}
	helpers.DecodeXML(t, resp, &out)
	if out.Result.TopicArn == "" {
		t.Fatalf("CreateTopic %q returned no TopicArn", name)
	}
	return out.Result.TopicArn
}

// subscribeQueueToTopic subscribes an SQS queue (by ARN) to an SNS topic.
func subscribeQueueToTopic(t *testing.T, srv *helpers.TestServer, topicARN, queueARN string) {
	t.Helper()
	resp := snsQueryCall(t, srv, "Subscribe", url.Values{
		"TopicArn": {topicARN},
		"Protocol": {"sqs"},
		"Endpoint": {queueARN},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// snsEnvelope is the SNS notification envelope an SQS subscriber receives.
type snsEnvelope struct {
	Type      string `json:"Type"`
	MessageId string `json:"MessageId"`
	TopicArn  string `json:"TopicArn"`
	Subject   string `json:"Subject"`
	Message   string `json:"Message"`
	Timestamp string `json:"Timestamp"`
}

// s3RecordsEnvelope is the S3 event notification carried in the SNS Message.
type s3RecordsEnvelope struct {
	Records []struct {
		EventVersion string `json:"eventVersion"`
		EventSource  string `json:"eventSource"`
		AWSRegion    string `json:"awsRegion"`
		EventName    string `json:"eventName"`
		S3           struct {
			ConfigurationID string `json:"configurationId"`
			Bucket          struct {
				Name string `json:"name"`
				ARN  string `json:"arn"`
			} `json:"bucket"`
			Object struct {
				Key  string `json:"key"`
				Size int64  `json:"size"`
				ETag string `json:"eTag"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

// ---- Delivery --------------------------------------------------------------

func TestPutObject_deliversToSNSTopicSubscribedQueue(t *testing.T) {
	// Given: an SQS queue subscribed to an SNS topic, and a bucket whose
	// notification configuration routes ObjectCreated events under docs/ to
	// that topic
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "sns-notify")
	queueURL := createQueueForEventBridge(t, srv, "sns-notify-queue")
	topicARN := createTopicForS3(t, srv, "sns-notify-topic")
	subscribeQueueToTopic(t, srv, topicARN, "arn:aws:sqs:us-east-1:000000000000:sns-notify-queue")
	putNotificationOK(t, srv, "sns-notify", `<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <TopicConfiguration>
    <Id>to-topic</Id>
    <Topic>`+topicARN+`</Topic>
    <Event>s3:ObjectCreated:*</Event>
    <Filter><S3Key><FilterRule><Name>prefix</Name><Value>docs/</Value></FilterRule></S3Key></Filter>
  </TopicConfiguration>
</NotificationConfiguration>`)

	// When: a matching object is uploaded
	putObject(t, srv, "sns-notify", "docs/hello.txt", []byte("hello world"), "text/plain")

	// Then: the queue receives the SNS notification envelope
	body := waitForEventBridgeMessage(t, srv, queueURL)
	var env snsEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("delivered message is not an SNS envelope (%q): %v", body, err)
	}
	if env.Type != "Notification" {
		t.Errorf("envelope Type = %q, want Notification", env.Type)
	}
	if env.TopicArn != topicARN {
		t.Errorf("envelope TopicArn = %q, want %q", env.TopicArn, topicARN)
	}
	if env.Subject != "Amazon S3 Notification" {
		t.Errorf("envelope Subject = %q, want Amazon S3 Notification", env.Subject)
	}

	// And: the envelope's Message is the S3 records JSON as a string
	var event s3RecordsEnvelope
	if err := json.Unmarshal([]byte(env.Message), &event); err != nil {
		t.Fatalf("SNS Message is not the S3 records JSON (%q): %v", env.Message, err)
	}
	if len(event.Records) != 1 {
		t.Fatalf("Records has %d entries, want 1: %s", len(event.Records), env.Message)
	}
	rec := event.Records[0]
	if rec.EventSource != "aws:s3" {
		t.Errorf("eventSource = %q, want aws:s3", rec.EventSource)
	}
	if rec.EventName != "ObjectCreated:Put" {
		t.Errorf("eventName = %q, want ObjectCreated:Put", rec.EventName)
	}
	if rec.S3.ConfigurationID != "to-topic" {
		t.Errorf("configurationId = %q, want to-topic", rec.S3.ConfigurationID)
	}
	if rec.S3.Bucket.Name != "sns-notify" {
		t.Errorf("bucket.name = %q, want sns-notify", rec.S3.Bucket.Name)
	}
	if rec.S3.Object.Key != "docs/hello.txt" {
		t.Errorf("object.key = %q, want docs/hello.txt", rec.S3.Object.Key)
	}
	if rec.S3.Object.Size != 11 {
		t.Errorf("object.size = %d, want 11", rec.S3.Object.Size)
	}
}

func TestPutObject_snsTopicPrefixFilterExcludesNonMatchingKey(t *testing.T) {
	// Given: the same wiring, filtered to docs/
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "sns-filter")
	queueURL := createQueueForEventBridge(t, srv, "sns-filter-queue")
	topicARN := createTopicForS3(t, srv, "sns-filter-topic")
	subscribeQueueToTopic(t, srv, topicARN, "arn:aws:sqs:us-east-1:000000000000:sns-filter-queue")
	putNotificationOK(t, srv, "sns-filter",
		`<NotificationConfiguration><TopicConfiguration><Id>t</Id>`+
			`<Topic>`+topicARN+`</Topic><Event>s3:ObjectCreated:*</Event>`+
			`<Filter><S3Key><FilterRule><Name>prefix</Name><Value>docs/</Value></FilterRule></S3Key></Filter>`+
			`</TopicConfiguration></NotificationConfiguration>`)

	// When: an object outside the prefix is uploaded
	putObject(t, srv, "sns-filter", "images/pic.png", []byte("png"), "image/png")

	// Then: nothing reaches the topic's subscriber
	time.Sleep(100 * time.Millisecond)
	if body := receiveOneEventBridgeMessage(t, srv, queueURL); body != "" {
		t.Fatalf("a filtered-out key was published to SNS: %s", body)
	}
}

func TestPutObject_snsTopicEventTypeMismatchIsNotDelivered(t *testing.T) {
	// Given: a topic configuration selecting only ObjectRemoved events
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "sns-evtype")
	queueURL := createQueueForEventBridge(t, srv, "sns-evtype-queue")
	topicARN := createTopicForS3(t, srv, "sns-evtype-topic")
	subscribeQueueToTopic(t, srv, topicARN, "arn:aws:sqs:us-east-1:000000000000:sns-evtype-queue")
	putNotificationOK(t, srv, "sns-evtype",
		`<NotificationConfiguration><TopicConfiguration><Id>t</Id>`+
			`<Topic>`+topicARN+`</Topic><Event>s3:ObjectRemoved:*</Event>`+
			`</TopicConfiguration></NotificationConfiguration>`)

	// When: an object is created (not removed)
	putObject(t, srv, "sns-evtype", "created.txt", []byte("x"), "text/plain")

	// Then: the ObjectCreated event does not match and nothing is delivered
	time.Sleep(100 * time.Millisecond)
	if body := receiveOneEventBridgeMessage(t, srv, queueURL); body != "" {
		t.Fatalf("an unselected event type was published to SNS: %s", body)
	}
}
