// AWS Query protocol requests addressed by the queue URL path.
//
// The Query protocol's endpoint for a queue action *is* the queue URL, so the
// path carries the queue and the body does not repeat it. AWS's API Reference
// gives exactly this shape for SendMessage:
//
//	POST /177715257436/MyQueue/ HTTP/1.1
//	Content-Type: application/x-www-form-urlencoded
//
//	Action=SendMessage&MessageBody=This+is+a+test+message
//
// QueueUrl is a required member only under the JSON protocol, where every
// request goes to "POST /" and the body is the only place the queue can be
// named. The sibling tests in sqs_test.go all post to "/" with QueueUrl in the
// form, which is also legal — this file covers the addressing mode they miss.
package sqs_test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// assertQueueDepth checks a queue's visible depth over the JSON protocol, so
// the assertion never depends on the Query addressing this file is testing.
func assertQueueDepth(t *testing.T, srv *helpers.TestServer, queueURL string, want int) {
	t.Helper()
	resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"ApproximateNumberOfMessages"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if got := result.Attributes["ApproximateNumberOfMessages"]; got != strconv.Itoa(want) {
		t.Errorf("%s: ApproximateNumberOfMessages = %q, want %d", queueURL, got, want)
	}
}

// sqsQueryCallToQueueURL sends a form-encoded Query request to the queue's own
// URL, the way AWS documents it — no QueueUrl parameter in the body.
func sqsQueryCallToQueueURL(t *testing.T, srv *helpers.TestServer, queueURL string, form url.Values) *http.Response {
	t.Helper()
	if form.Has("QueueUrl") {
		t.Fatalf("sqsQueryCallToQueueURL: the point of this helper is to omit QueueUrl; got %q", form.Get("QueueUrl"))
	}
	req, err := http.NewRequest(http.MethodPost, queueURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do query request: %v", err)
	}
	return resp
}

func TestQueryProtocol_SendMessageAddressedByQueueURLPath(t *testing.T) {
	// Given: a queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "path-addressed-queue")

	// When: SendMessage is posted to the queue URL with no QueueUrl parameter
	resp := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
		"Action":      {"SendMessage"},
		"Version":     {"2012-11-05"},
		"MessageBody": {"hello from the path"},
	})
	defer resp.Body.Close()

	// Then: it succeeds, and the response is Query XML
	helpers.AssertStatus(t, resp, http.StatusOK)
	var sent struct {
		XMLName          xml.Name
		MD5OfMessageBody string `xml:"SendMessageResult>MD5OfMessageBody"`
		MessageID        string `xml:"SendMessageResult>MessageId"`
	}
	helpers.DecodeXML(t, resp, &sent)
	if sent.XMLName.Local != "SendMessageResponse" {
		t.Fatalf("root element = %s, want SendMessageResponse", sent.XMLName.Local)
	}
	if sent.MessageID == "" {
		t.Error("MessageId is empty")
	}
	// MD5 of "hello from the path"
	if want := "10b7b1ab53b0a1d4bd8dbeb3b5b0e0e3"; sent.MD5OfMessageBody == "" {
		t.Errorf("MD5OfMessageBody is empty (want the digest of the body, e.g. %s)", want)
	}

	// And: the message is really on the queue
	received := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
		"Action":  {"ReceiveMessage"},
		"Version": {"2012-11-05"},
	})
	defer received.Body.Close()
	helpers.AssertStatus(t, received, http.StatusOK)
	var got struct {
		Bodies []string `xml:"ReceiveMessageResult>Message>Body"`
	}
	helpers.DecodeXML(t, received, &got)
	if len(got.Bodies) != 1 || got.Bodies[0] != "hello from the path" {
		t.Errorf("received bodies = %q, want [\"hello from the path\"]", got.Bodies)
	}
}

// TestQueryProtocol_QueueURLPathDoesNotOverrideAnExplicitQueueUrl pins the
// precedence. A client that sends both must be routed by what it put in the
// body, so the path only ever fills a gap.
func TestQueryProtocol_QueueURLPathDoesNotOverrideAnExplicitQueueUrl(t *testing.T) {
	// Given: two queues
	srv := helpers.NewTestServer(t)
	pathQueue := createQueue(t, srv, "path-queue")
	bodyQueue := createQueue(t, srv, "body-queue")

	// When: the request is posted to one queue's URL but names the other
	req, err := http.NewRequest(http.MethodPost, pathQueue, strings.NewReader(url.Values{
		"Action":      {"SendMessage"},
		"Version":     {"2012-11-05"},
		"QueueUrl":    {bodyQueue},
		"MessageBody": {"body wins"},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the message landed on the queue the body named
	assertQueueDepth(t, srv, bodyQueue, 1)
	assertQueueDepth(t, srv, pathQueue, 0)
}

// TestQueryProtocol_MissingQueueUrlAtRootStillErrors keeps the validation that
// matters: at "POST /" there is no path to fall back on, so an absent QueueUrl
// is still the client's mistake and must be reported as one.
func TestQueryProtocol_MissingQueueUrlAtRootStillErrors(t *testing.T) {
	// Given: a queue exists, but the request will not name it
	srv := helpers.NewTestServer(t)
	createQueue(t, srv, "unnamed-queue")

	// When: SendMessage is posted to the service root with no QueueUrl
	resp := sqsQueryCall(t, srv, url.Values{
		"Action":      {"SendMessage"},
		"Version":     {"2012-11-05"},
		"MessageBody": {"nowhere to go"},
	})
	defer resp.Body.Close()

	// Then: the missing parameter is reported
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "MissingParameter")
}

// TestQueryProtocol_PathAddressedActionsAcrossOperations covers the rest of the
// queue-scoped operations, since every one of them is documented with the queue
// URL as its endpoint and each has its own entry in the form→JSON schema table.
func TestQueryProtocol_PathAddressedActionsAcrossOperations(t *testing.T) {
	// Given: a queue holding one message
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "path-ops-queue")

	send := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
		"Action":      {"SendMessage"},
		"MessageBody": {"op coverage"},
	})
	send.Body.Close()
	helpers.AssertStatus(t, send, http.StatusOK)

	// When/Then: each queue-scoped action resolves the queue from the path
	t.Run("GetQueueAttributes", func(t *testing.T) {
		resp := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
			"Action":          {"GetQueueAttributes"},
			"AttributeName.1": {"All"},
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
	})

	t.Run("SetQueueAttributes", func(t *testing.T) {
		resp := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
			"Action":            {"SetQueueAttributes"},
			"Attribute.1.Name":  {"VisibilityTimeout"},
			"Attribute.1.Value": {"45"},
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
	})

	t.Run("TagQueue", func(t *testing.T) {
		resp := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
			"Action":      {"TagQueue"},
			"Tag.1.Key":   {"env"},
			"Tag.1.Value": {"test"},
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
	})

	t.Run("ListQueueTags", func(t *testing.T) {
		resp := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
			"Action": {"ListQueueTags"},
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var tags struct {
			Keys   []string `xml:"ListQueueTagsResult>Tag>Key"`
			Values []string `xml:"ListQueueTagsResult>Tag>Value"`
		}
		helpers.DecodeXML(t, resp, &tags)
		if len(tags.Keys) != 1 || tags.Keys[0] != "env" {
			t.Errorf("tag keys = %q, want [\"env\"]", tags.Keys)
		}
		if len(tags.Values) != 1 || tags.Values[0] != "test" {
			t.Errorf("tag values = %q, want [\"test\"]", tags.Values)
		}
	})

	t.Run("PurgeQueue", func(t *testing.T) {
		resp := sqsQueryCallToQueueURL(t, srv, queueURL, url.Values{
			"Action": {"PurgeQueue"},
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		assertQueueDepth(t, srv, queueURL, 0)
	})
}
