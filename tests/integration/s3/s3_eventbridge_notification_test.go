// EventBridge bucket notification configuration.
//
// com.amazonaws.s3#NotificationConfiguration carries an EventBridgeConfiguration
// member alongside TopicConfigurations, QueueConfigurations and
// LambdaFunctionConfigurations. The shape is an empty structure — presence is
// the whole signal, and AWS delivers *every* object event to the default event
// bus while it is present, with no per-event or key filtering.
//
// The delivered envelope follows AWS's documented S3 EventBridge event:
// source "aws.s3", detail-type "Object Created" / "Object Deleted", the bucket
// ARN in resources, and a detail carrying version, bucket.name, object.key,
// object.size, object.etag and reason. Fields Overcast has no truthful value
// for (request-id, requester, source-ip-address, sequencer, version-id) are
// omitted rather than invented — see docs/services/s3.md.
package s3_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- Wire shapes -----------------------------------------------------------

type notificationConfigurationXMLT struct {
	XMLName                  xml.Name                      `xml:"NotificationConfiguration"`
	EventBridgeConfiguration *eventBridgeConfigurationXMLT `xml:"EventBridgeConfiguration"`
	QueueConfigurations      []queueConfigurationXMLT      `xml:"QueueConfiguration"`
}

type eventBridgeConfigurationXMLT struct{}

type queueConfigurationXMLT struct {
	ID    string `xml:"Id"`
	Queue string `xml:"Queue"`
}

// ---- Local helpers ---------------------------------------------------------

func putNotification(t *testing.T, srv *helpers.TestServer, bucket, body string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"?notification", []byte(body), map[string]string{
		"Content-Type": "application/xml",
	}))
	if err != nil {
		t.Fatalf("PutBucketNotificationConfiguration %q: %v", bucket, err)
	}
	return resp
}

func putNotificationOK(t *testing.T, srv *helpers.TestServer, bucket, body string) {
	t.Helper()
	resp := putNotification(t, srv, bucket, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutBucketNotificationConfiguration %q: status %d, body %s",
			bucket, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

func getNotification(t *testing.T, srv *helpers.TestServer, bucket string) notificationConfigurationXMLT {
	t.Helper()
	resp, err := http.DefaultClient.Do(get(srv, "/"+bucket+"?notification"))
	if err != nil {
		t.Fatalf("GetBucketNotificationConfiguration %q: %v", bucket, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var cfg notificationConfigurationXMLT
	helpers.DecodeXML(t, resp, &cfg)
	return cfg
}

// ---- Configuration round trip ----------------------------------------------

func TestPutBucketNotificationConfiguration_eventBridgeRoundTrip(t *testing.T) {
	// Given: a bucket with no notification configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "eb-notify")

	// When: EventBridge delivery is enabled alongside a queue destination
	putNotificationOK(t, srv, "eb-notify", `<?xml version="1.0" encoding="UTF-8"?>
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <EventBridgeConfiguration></EventBridgeConfiguration>
  <QueueConfiguration>
    <Id>to-queue</Id>
    <Queue>arn:aws:sqs:us-east-1:000000000000:eb-notify-queue</Queue>
    <Event>s3:ObjectCreated:*</Event>
  </QueueConfiguration>
</NotificationConfiguration>`)

	// Then: both survive — the EventBridge element does not displace the others
	cfg := getNotification(t, srv, "eb-notify")
	if cfg.EventBridgeConfiguration == nil {
		t.Fatalf("EventBridgeConfiguration missing from the round trip: %+v", cfg)
	}
	if len(cfg.QueueConfigurations) != 1 || cfg.QueueConfigurations[0].ID != "to-queue" {
		t.Fatalf("QueueConfigurations = %+v, want the queue destination preserved", cfg.QueueConfigurations)
	}
}

func TestGetBucketNotificationConfiguration_omitsEventBridgeWhenNotEnabled(t *testing.T) {
	// Given: a bucket configured with a queue destination only
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "eb-notify-absent")
	putNotificationOK(t, srv, "eb-notify-absent",
		`<NotificationConfiguration><QueueConfiguration><Id>q</Id>`+
			`<Queue>arn:aws:sqs:us-east-1:000000000000:eb-notify-absent-queue</Queue>`+
			`<Event>s3:ObjectCreated:*</Event></QueueConfiguration></NotificationConfiguration>`)

	// Then: no empty EventBridgeConfiguration element is invented — its mere
	// presence would turn delivery on for a client that read it back and re-put it
	resp, err := http.DefaultClient.Do(get(srv, "/eb-notify-absent?notification"))
	if err != nil {
		t.Fatalf("GetBucketNotificationConfiguration: %v", err)
	}
	defer resp.Body.Close()
	body := helpers.ReadBody(t, resp)
	if strings.Contains(body, "EventBridgeConfiguration") {
		t.Fatalf("response invents an EventBridgeConfiguration element: %s", body)
	}
}

func TestPutBucketNotificationConfiguration_eventBridgeIsClearedByReplacement(t *testing.T) {
	// Given: a bucket with EventBridge delivery enabled
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "eb-notify-clear")
	putNotificationOK(t, srv, "eb-notify-clear",
		`<NotificationConfiguration><EventBridgeConfiguration/></NotificationConfiguration>`)
	if getNotification(t, srv, "eb-notify-clear").EventBridgeConfiguration == nil {
		t.Fatal("EventBridgeConfiguration was not stored")
	}

	// When: an empty configuration replaces it — AWS's documented way to turn
	// notifications off for a bucket
	putNotificationOK(t, srv, "eb-notify-clear", `<NotificationConfiguration></NotificationConfiguration>`)

	// Then: EventBridge delivery is off again
	if cfg := getNotification(t, srv, "eb-notify-clear"); cfg.EventBridgeConfiguration != nil {
		t.Fatalf("EventBridgeConfiguration survived a replacing Put: %+v", cfg)
	}
}

// ---- Delivery --------------------------------------------------------------

func TestPutObject_deliversObjectCreatedToEventBridge(t *testing.T) {
	// Given: an EventBridge rule for S3 object events targeting an SQS queue,
	// and a bucket that publishes to EventBridge
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "eb-created")
	queueURL := createQueueForEventBridge(t, srv, "eb-created-queue")
	putEventBridgeRule(t, srv, "s3-object-created",
		`{"source":["aws.s3"],"detail-type":["Object Created"]}`,
		"arn:aws:sqs:us-east-1:000000000000:eb-created-queue")
	putNotificationOK(t, srv, "eb-created",
		`<NotificationConfiguration><EventBridgeConfiguration/></NotificationConfiguration>`)

	// When: an object is uploaded
	putObject(t, srv, "eb-created", "docs/hello.txt", []byte("hello world"), "text/plain")

	// Then: the rule's target receives AWS's S3 EventBridge envelope
	body := waitForEventBridgeMessage(t, srv, queueURL)
	var event struct {
		Source     string   `json:"source"`
		DetailType string   `json:"detail-type"`
		Resources  []string `json:"resources"`
		Detail     struct {
			Version string `json:"version"`
			Bucket  struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key  string `json:"key"`
				Size int64  `json:"size"`
				ETag string `json:"etag"`
			} `json:"object"`
			Reason string `json:"reason"`
		} `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		t.Fatalf("delivered event is not valid JSON (%q): %v", body, err)
	}
	if event.Source != "aws.s3" {
		t.Errorf("source = %q, want aws.s3", event.Source)
	}
	if event.DetailType != "Object Created" {
		t.Errorf("detail-type = %q, want Object Created", event.DetailType)
	}
	if len(event.Resources) != 1 || event.Resources[0] != "arn:aws:s3:::eb-created" {
		t.Errorf("resources = %v, want the bucket ARN", event.Resources)
	}
	if event.Detail.Version != "0" {
		t.Errorf("detail.version = %q, want 0", event.Detail.Version)
	}
	if event.Detail.Bucket.Name != "eb-created" {
		t.Errorf("detail.bucket.name = %q, want eb-created", event.Detail.Bucket.Name)
	}
	if event.Detail.Object.Key != "docs/hello.txt" {
		t.Errorf("detail.object.key = %q, want docs/hello.txt", event.Detail.Object.Key)
	}
	if event.Detail.Object.Size != 11 {
		t.Errorf("detail.object.size = %d, want 11", event.Detail.Object.Size)
	}
	if event.Detail.Object.ETag == "" {
		t.Error("detail.object.etag is empty")
	}
	if event.Detail.Reason != "PutObject" {
		t.Errorf("detail.reason = %q, want PutObject", event.Detail.Reason)
	}
}

func TestDeleteObject_deliversObjectDeletedToEventBridge(t *testing.T) {
	// Given: a rule for S3 delete events and a bucket holding one object
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "eb-deleted")
	queueURL := createQueueForEventBridge(t, srv, "eb-deleted-queue")
	putEventBridgeRule(t, srv, "s3-object-deleted",
		`{"source":["aws.s3"],"detail-type":["Object Deleted"]}`,
		"arn:aws:sqs:us-east-1:000000000000:eb-deleted-queue")
	putObject(t, srv, "eb-deleted", "gone.txt", []byte("bye"), "text/plain")
	putNotificationOK(t, srv, "eb-deleted",
		`<NotificationConfiguration><EventBridgeConfiguration/></NotificationConfiguration>`)

	// When: the object is deleted
	resp, err := http.DefaultClient.Do(del(srv, "/eb-deleted/gone.txt"))
	if err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// Then: the delete arrives as "Object Deleted" with AWS's deletion-type
	body := waitForEventBridgeMessage(t, srv, queueURL)
	var event struct {
		DetailType string `json:"detail-type"`
		Detail     struct {
			Object struct {
				Key string `json:"key"`
			} `json:"object"`
			Reason       string `json:"reason"`
			DeletionType string `json:"deletion-type"`
		} `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		t.Fatalf("delivered event is not valid JSON (%q): %v", body, err)
	}
	if event.DetailType != "Object Deleted" {
		t.Errorf("detail-type = %q, want Object Deleted", event.DetailType)
	}
	if event.Detail.Object.Key != "gone.txt" {
		t.Errorf("detail.object.key = %q, want gone.txt", event.Detail.Object.Key)
	}
	if event.Detail.Reason != "DeleteObject" {
		t.Errorf("detail.reason = %q, want DeleteObject", event.Detail.Reason)
	}
	if event.Detail.DeletionType != "Permanently Deleted" {
		t.Errorf("detail.deletion-type = %q, want Permanently Deleted", event.Detail.DeletionType)
	}
}

func TestPutObject_noEventBridgeDeliveryWhenNotConfigured(t *testing.T) {
	// Given: the same rule, but a bucket with no EventBridge configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "eb-off")
	queueURL := createQueueForEventBridge(t, srv, "eb-off-queue")
	putEventBridgeRule(t, srv, "s3-all",
		`{"source":["aws.s3"]}`,
		"arn:aws:sqs:us-east-1:000000000000:eb-off-queue")

	// When: an object is uploaded
	putObject(t, srv, "eb-off", "quiet.txt", []byte("shh"), "text/plain")

	// Then: nothing is delivered — EventBridge delivery is opt-in per bucket
	time.Sleep(100 * time.Millisecond)
	if body := receiveOneEventBridgeMessage(t, srv, queueURL); body != "" {
		t.Fatalf("an unconfigured bucket published to EventBridge: %s", body)
	}
}

// ---- EventBridge helpers ---------------------------------------------------

// eventBridgeCall performs an EventBridge X-Amz-Target dispatch request.
func eventBridgeCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal EventBridge %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSEvents."+operation)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("EventBridge %s: %v", operation, err)
	}
	return resp
}

func createQueueForEventBridge(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{"QueueName": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		QueueURL string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.QueueURL
}

func putEventBridgeRule(t *testing.T, srv *helpers.TestServer, rule, pattern, targetARN string) {
	t.Helper()
	ruleResp := eventBridgeCall(t, srv, "PutRule", map[string]any{
		"Name":         rule,
		"EventPattern": pattern,
	})
	ruleResp.Body.Close()
	helpers.AssertStatus(t, ruleResp, http.StatusOK)

	targetResp := eventBridgeCall(t, srv, "PutTargets", map[string]any{
		"Rule":    rule,
		"Targets": []any{map[string]any{"Id": "queue", "Arn": targetARN}},
	})
	targetResp.Body.Close()
	helpers.AssertStatus(t, targetResp, http.StatusOK)
}

// waitForEventBridgeMessage polls the target queue until the asynchronous S3
// event bus has run the dispatcher and EventBridge has fanned out.
func waitForEventBridgeMessage(t *testing.T, srv *helpers.TestServer, queueURL string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if body := receiveOneEventBridgeMessage(t, srv, queueURL); body != "" {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no EventBridge delivery arrived at %s", queueURL)
	return ""
}

func receiveOneEventBridgeMessage(t *testing.T, srv *helpers.TestServer, queueURL string) string {
	t.Helper()
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
	})
	defer resp.Body.Close()
	var out struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if len(out.Messages) == 0 {
		return ""
	}
	return out.Messages[0].Body
}
