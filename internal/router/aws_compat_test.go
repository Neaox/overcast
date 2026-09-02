package router

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/smtp"
	"github.com/overcast-sh/overcast/internal/state"
)

// aws_compat_test.go — LocalStack's /_aws/ inspection namespace.
//
// As in health_compat_test.go, every route-level test goes through the real
// router.New() wiring: the claim is that a URL a test suite carried over from
// LocalStack answers at all, and before these routes every /_aws/* path fell
// through to the S3 catch-all. The shape tests below that call the translation
// functions directly cover what a handler-level test can — which fields are
// where — and no more.

// newAWSCompatServer is newHealthCompatServer with the mock SMTP listener on,
// so an SES send has somewhere to land. Port 0 asks for an ephemeral port.
func newAWSCompatServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",
		Version:   "test-version",

		SMTPMock:      true,
		SMTPPort:      0,
		SMTPInboxMax:  50,
		SigV4Validate: false,
	}
	handler, preShutdown, cleanup, _ := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		preShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cleanup(ctx)
		srv.Close()
	})
	return srv
}

// sendSESEmail sends one email through SES v1's Query API, which is the shape
// `aws ses send-email` and the v1 SDKs use.
func sendSESEmail(t *testing.T, srv *httptest.Server, from, to, subject, text string) {
	t.Helper()
	form := url.Values{
		"Action":                           {"SendEmail"},
		"Version":                          {"2010-12-01"},
		"Source":                           {from},
		"Destination.ToAddresses.member.1": {to},
		"Message.Subject.Data":             {subject},
		"Message.Body.Text.Data":           {text},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260902/us-east-1/ses/aws4_request, SignedHeaders=host, Signature=unverified")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SendEmail: status = %d, body: %s", resp.StatusCode, body)
	}
}

// awsSESMessages reads GET /_aws/ses with the given query string and returns
// the messages list, waiting briefly for the SMTP capture to land — delivery
// is synchronous from SES's point of view, but the assertion is on the
// capture, which is one hop further.
func awsSESMessages(t *testing.T, srv *httptest.Server, query string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, body, raw := getJSON(t, srv.URL+"/_aws/ses"+query)
		if status != http.StatusOK {
			t.Fatalf("GET /_aws/ses%s: status = %d (body: %s)", query, status, raw)
		}
		list, ok := body["messages"].([]any)
		if !ok {
			t.Fatalf("GET /_aws/ses%s: no messages list; body: %s", query, raw)
		}
		if len(list) == want || time.Now().After(deadline) {
			out := make([]map[string]any, 0, len(list))
			for _, m := range list {
				out = append(out, m.(map[string]any))
			}
			if len(out) != want {
				t.Fatalf("GET /_aws/ses%s: %d messages, want %d; body: %s", query, len(out), want, raw)
			}
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAWSCompatSES_listsSentEmailsInLocalStacksShape is the assertion a
// migrated test suite makes: send through SES, read it back at /_aws/ses,
// by LocalStack's field names.
func TestAWSCompatSES_listsSentEmailsInLocalStacksShape(t *testing.T) {
	srv := newAWSCompatServer(t)

	// Given: nothing has been sent — and the answer says so as an empty list,
	// not null, because `len(messages) == 0` is the first assertion written.
	status, _, raw := getJSON(t, srv.URL+"/_aws/ses")
	if status != http.StatusOK || !strings.Contains(raw, `"messages":[]`) {
		t.Fatalf("GET /_aws/ses before any send: status = %d, body: %s", status, raw)
	}

	// When: one email goes through SES.
	sendSESEmail(t, srv, "hello@example.com", "jeff@aws.com", "This is the email subject", "This is the email body")

	// Then: it reads back in LocalStack's shape.
	msgs := awsSESMessages(t, srv, "", 1)
	m := msgs[0]
	if m["Id"] == nil || m["Id"] == "" {
		t.Errorf("Id is missing; message: %v", m)
	}
	if m["Source"] != "hello@example.com" {
		t.Errorf("Source = %v, want hello@example.com", m["Source"])
	}
	if m["Subject"] != "This is the email subject" {
		t.Errorf("Subject = %v", m["Subject"])
	}
	dest, _ := m["Destination"].(map[string]any)
	to, _ := dest["ToAddresses"].([]any)
	if len(to) != 1 || to[0] != "jeff@aws.com" {
		t.Errorf("Destination.ToAddresses = %v, want [jeff@aws.com]", dest["ToAddresses"])
	}
	body, _ := m["Body"].(map[string]any)
	if body["text_part"] != "This is the email body" {
		t.Errorf("Body.text_part = %v", body["text_part"])
	}
	if part, present := body["html_part"]; !present || part != nil {
		t.Errorf("Body.html_part = %v, want an explicit null for a text-only email", part)
	}
	ts, _ := m["Timestamp"].(string)
	if _, err := time.Parse(localStackSESTimestampLayout, ts); err != nil {
		t.Errorf("Timestamp %q is not in LocalStack's %s form: %v", ts, localStackSESTimestampLayout, err)
	}
	// Overcast's own field names must not leak through: a reader keyed on
	// LocalStack's names would not notice, but a reader diffing the whole body
	// against a LocalStack fixture would.
	for _, overcastField := range []string{"id", "kind", "from", "to", "textBody", "receivedAt"} {
		if _, leaked := m[overcastField]; leaked {
			t.Errorf("message carries Overcast's %q field alongside LocalStack's", overcastField)
		}
	}
}

// TestAWSCompatSES_filtersByIdAndEmail covers LocalStack's two query
// parameters, and that the id filter selects rather than merely matches.
func TestAWSCompatSES_filtersByIdAndEmail(t *testing.T) {
	srv := newAWSCompatServer(t)
	sendSESEmail(t, srv, "one@example.com", "a@aws.com", "first", "body")
	sendSESEmail(t, srv, "two@example.com", "b@aws.com", "second", "body")
	all := awsSESMessages(t, srv, "", 2)

	// Oldest first, as LocalStack lists them.
	if all[0]["Subject"] != "first" || all[1]["Subject"] != "second" {
		t.Errorf("order = %v, %v; want oldest first", all[0]["Subject"], all[1]["Subject"])
	}

	byEmail := awsSESMessages(t, srv, "?email=two%40example.com", 1)
	if byEmail[0]["Subject"] != "second" {
		t.Errorf("?email= returned %v, want the second email", byEmail[0]["Subject"])
	}

	id, _ := all[0]["Id"].(string)
	byID := awsSESMessages(t, srv, "?id="+url.QueryEscape(id), 1)
	if byID[0]["Id"] != id {
		t.Errorf("?id= returned %v, want %s", byID[0]["Id"], id)
	}
	awsSESMessages(t, srv, "?id=no-such-id", 0)
}

// TestAWSCompatSES_deleteClearsEmailsOnly covers DELETE /_aws/ses, and that it
// reads the same store as the inbox: what it clears is gone from
// /_overcast/ses/inbox/messages too, and what the inbox holds that is not an
// email survives.
func TestAWSCompatSES_deleteClearsEmailsOnly(t *testing.T) {
	srv := newAWSCompatServer(t)
	sendSESEmail(t, srv, "one@example.com", "a@aws.com", "first", "body")
	sendSESEmail(t, srv, "two@example.com", "b@aws.com", "second", "body")
	all := awsSESMessages(t, srv, "", 2)

	// When: one is deleted by id.
	firstID, _ := all[0]["Id"].(string)
	if status := doDelete(t, srv.URL+"/_aws/ses?id="+url.QueryEscape(firstID)); status != http.StatusNoContent {
		t.Fatalf("DELETE /_aws/ses?id=: status = %d, want 204", status)
	}
	rest := awsSESMessages(t, srv, "", 1)
	if rest[0]["Subject"] != "second" {
		t.Errorf("after deleting the first, %v remains; want the second", rest[0]["Subject"])
	}

	// And: the inbox agrees, because it is the same store.
	status, _, raw := getJSON(t, srv.URL+"/_overcast/ses/inbox/messages")
	if status != http.StatusOK || strings.Contains(raw, firstID) {
		t.Errorf("/_overcast/ses/inbox/messages still lists the deleted email (status %d): %s", status, raw)
	}

	// When: everything is deleted.
	if status := doDelete(t, srv.URL+"/_aws/ses"); status != http.StatusNoContent {
		t.Fatalf("DELETE /_aws/ses: status = %d, want 204", status)
	}
	awsSESMessages(t, srv, "", 0)
	// A second delete of nothing is still a 204: the state asked for is the
	// state there is.
	if status := doDelete(t, srv.URL+"/_aws/ses?id=gone"); status != http.StatusNoContent {
		t.Errorf("DELETE of an unknown id: status = %d, want 204", status)
	}
}

func doDelete(t *testing.T, target string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", target, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestLocalStackSESMessages_emailsOnly pins the filter the store-level
// translation applies: the inbox holds SMS, webhook and push captures beside
// the emails, and /_aws/ses must list only what SES sent.
func TestLocalStackSESMessages_emailsOnly(t *testing.T) {
	store := smtp.NewMailStore(0)
	store.Add(smtp.NewSMSMessage("sns", "+15550001111", "+15550002222", "an sms", "", ""))
	store.Add(&smtp.CapturedMessage{ID: "mail", Kind: smtp.KindEmail, From: "a@x.com", To: []string{"b@x.com"}, TextBody: "hi", HTMLBody: "<p>hi</p>", ReceivedAt: time.Date(2026, 9, 2, 8, 37, 13, 0, time.UTC)})
	store.Add(smtp.NewWebhookMessage("sns", "https://example.com/hook", "{}", "", ""))
	store.Add(smtp.NewPushMessage("sns", "arn:aws:sns:us-east-1:000000000000:endpoint/GCM/app/x", "{}", "", ""))

	got := localStackSESMessages(store, "", "")
	if len(got) != 1 || got[0].ID != "mail" {
		t.Fatalf("got %d messages (%v), want just the email", len(got), got)
	}
	if got[0].Body.HTMLPart == nil || *got[0].Body.HTMLPart != "<p>hi</p>" {
		t.Errorf("html_part not carried: %v", got[0].Body.HTMLPart)
	}
	if got[0].Timestamp != "2026-09-02T08:37:13" {
		t.Errorf("Timestamp = %q, want LocalStack's naive UTC form", got[0].Timestamp)
	}
}

// ---- /_aws/sqs/messages ----------------------------------------------------

// sqsJSON calls one SQS JSON-protocol operation and returns the decoded body.
func sqsJSON(t *testing.T, srv *httptest.Server, target string, in map[string]any, region string) map[string]any {
	t.Helper()
	payload, _ := json.Marshal(in)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+target)
	if region != "" {
		req.Header.Set("X-Overcast-Region", region)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status = %d, body: %s", target, resp.StatusCode, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func createQueueWithMessage(t *testing.T, srv *httptest.Server, name, region string) string {
	t.Helper()
	created := sqsJSON(t, srv, "CreateQueue", map[string]any{"QueueName": name}, region)
	queueURL, _ := created["QueueUrl"].(string)
	if queueURL == "" {
		t.Fatalf("CreateQueue returned no QueueUrl: %v", created)
	}
	sqsJSON(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "hello",
		"MessageAttributes": map[string]any{
			"kind": map[string]any{"DataType": "String", "StringValue": "greeting"},
		},
	}, region)
	return queueURL
}

// get performs a GET with optional headers and returns status, body.
func get(t *testing.T, target string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// receiveMessageXML is the XML the peek renders, decoded through the same
// tags LocalStack's consumers parse it with.
type receiveMessageXML struct {
	XMLName xml.Name `xml:"ReceiveMessageResponse"`
	Result  struct {
		Message []struct {
			MessageID     string `xml:"MessageId"`
			MD5OfBody     string `xml:"MD5OfBody"`
			Body          string `xml:"Body"`
			ReceiptHandle string `xml:"ReceiptHandle"`
			Attribute     []struct {
				Name  string `xml:"Name"`
				Value string `xml:"Value"`
			} `xml:"Attribute"`
			MessageAttribute []struct {
				Name  string `xml:"Name"`
				Value struct {
					DataType    string `xml:"DataType"`
					StringValue string `xml:"StringValue"`
				} `xml:"Value"`
			} `xml:"MessageAttribute"`
		} `xml:"Message"`
	} `xml:"ReceiveMessageResult"`
	Metadata struct {
		RequestID string `xml:"RequestId"`
	} `xml:"ResponseMetadata"`
}

// TestAWSCompatSQS_peeksInLocalStacksXMLShape is the other assertion a
// migrated suite makes: send to a queue, read it back at /_aws/sqs/messages
// without consuming it, as the ReceiveMessageResponse LocalStack renders.
func TestAWSCompatSQS_peeksInLocalStacksXMLShape(t *testing.T) {
	srv := newAWSCompatServer(t)
	queueURL := createQueueWithMessage(t, srv, "peek-xml", "")

	status, raw := get(t, srv.URL+"/_aws/sqs/messages?QueueUrl="+url.QueryEscape(queueURL), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body: %s", status, raw)
	}
	if !strings.Contains(raw, `<ReceiveMessageResponse xmlns="http://queue.amazonaws.com/doc/2012-11-05/">`) {
		t.Errorf("body does not open with the SQS 2012-11-05 namespace: %s", raw)
	}
	var body receiveMessageXML
	if err := xml.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not a ReceiveMessageResponse: %v\n%s", err, raw)
	}
	if len(body.Result.Message) != 1 {
		t.Fatalf("%d messages, want 1: %s", len(body.Result.Message), raw)
	}
	m := body.Result.Message[0]
	if m.Body != "hello" || m.MessageID == "" || m.ReceiptHandle == "" {
		t.Errorf("message = %+v, want Body hello with an id and a receipt handle", m)
	}
	if m.MD5OfBody != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("MD5OfBody = %q, want the md5 of \"hello\"", m.MD5OfBody)
	}
	attrs := map[string]string{}
	for _, a := range m.Attribute {
		attrs[a.Name] = a.Value
	}
	if attrs["SenderId"] != "000000000000" || attrs["ApproximateReceiveCount"] != "0" || attrs["SentTimestamp"] == "" {
		t.Errorf("Attribute list = %v, want SenderId, ApproximateReceiveCount 0 and SentTimestamp", attrs)
	}
	if len(m.MessageAttribute) != 1 || m.MessageAttribute[0].Name != "kind" || m.MessageAttribute[0].Value.StringValue != "greeting" {
		t.Errorf("MessageAttribute = %+v, want the one sent", m.MessageAttribute)
	}
	if body.Metadata.RequestID == "" {
		t.Error("ResponseMetadata.RequestId is empty")
	}

	// And: nothing was consumed. A second peek and a real receive both still
	// see it, with the receive count untouched by the peeks.
	status, again := get(t, srv.URL+"/_aws/sqs/messages?QueueUrl="+url.QueryEscape(queueURL), nil)
	if status != http.StatusOK || !strings.Contains(again, "<Body>hello</Body>") {
		t.Errorf("second peek lost the message (status %d): %s", status, again)
	}
	received := sqsJSON(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL}, "")
	if list, _ := received["Messages"].([]any); len(list) != 1 {
		t.Errorf("ReceiveMessage after two peeks returned %d messages, want 1: %v", len(list), received)
	}
}

// TestAWSCompatSQS_peeksAsJSONUnderAccept covers LocalStack's JSON rendering,
// which is the same tree under one more level of key.
func TestAWSCompatSQS_peeksAsJSONUnderAccept(t *testing.T) {
	srv := newAWSCompatServer(t)
	queueURL := createQueueWithMessage(t, srv, "peek-json", "")

	status, raw := get(t, srv.URL+"/_aws/sqs/messages?QueueUrl="+url.QueryEscape(queueURL), map[string]string{"Accept": "application/json"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, body: %s", status, raw)
	}
	var body struct {
		ReceiveMessageResponse struct {
			ReceiveMessageResult struct {
				Message []struct {
					MessageID string `json:"MessageId"`
					Body      string
					Attribute []struct{ Name, Value string }
				}
			}
			ResponseMetadata struct {
				RequestID string `json:"RequestId"`
			}
		}
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not LocalStack's JSON shape: %v\n%s", err, raw)
	}
	msgs := body.ReceiveMessageResponse.ReceiveMessageResult.Message
	if len(msgs) != 1 || msgs[0].Body != "hello" || msgs[0].MessageID == "" {
		t.Errorf("messages = %+v, want one with Body hello", msgs)
	}
	if len(msgs[0].Attribute) == 0 {
		t.Errorf("Attribute list is empty: %s", raw)
	}
	if body.ReceiveMessageResponse.ResponseMetadata.RequestID == "" {
		t.Error("ResponseMetadata.RequestId is empty")
	}
	if strings.Contains(raw, "XMLName") {
		t.Errorf("the XML name leaked into the JSON body: %s", raw)
	}

	// An empty queue is an empty list, not null — `len == 0` is the assertion.
	sqsJSON(t, srv, "PurgeQueue", map[string]any{"QueueUrl": queueURL}, "")
	status, raw = get(t, srv.URL+"/_aws/sqs/messages?QueueUrl="+url.QueryEscape(queueURL), map[string]string{"Accept": "application/json"})
	if status != http.StatusOK || !strings.Contains(raw, `"Message":[]`) {
		t.Errorf("empty queue: status %d, body %s; want \"Message\":[]", status, raw)
	}
}

// TestAWSCompatSQS_acceptsEveryFormLocalStackDocuments covers the path form
// and the QueueName form beside ?QueueUrl=, and that a region named in any
// of them is honoured — the failure a peek is reached for is usually "the
// queue is in another region", and a peek that ignored the region could not
// show it.
func TestAWSCompatSQS_acceptsEveryFormLocalStackDocuments(t *testing.T) {
	srv := newAWSCompatServer(t)
	createQueueWithMessage(t, srv, "regional", "eu-west-1")

	cases := map[string]string{
		"path form":               "/_aws/sqs/messages/eu-west-1/000000000000/regional",
		"QueueName + QueueRegion": "/_aws/sqs/messages?QueueName=regional&QueueRegion=eu-west-1",
		"QueueUrl on a LocalStack regional host": "/_aws/sqs/messages?QueueUrl=" +
			url.QueryEscape("http://sqs.eu-west-1.localhost.localstack.cloud:4566/000000000000/regional"),
		"QueueUrl on a plain origin, QueueRegion beside it": "/_aws/sqs/messages?QueueRegion=eu-west-1&QueueUrl=" +
			url.QueryEscape("http://localhost:4566/000000000000/regional"),
	}
	for name, path := range cases {
		status, raw := get(t, srv.URL+path, nil)
		if status != http.StatusOK || !strings.Contains(raw, "<Body>hello</Body>") {
			t.Errorf("%s: status %d, body %s; want the message", name, status, raw)
		}
	}

	// The same queue asked for in the wrong region is NonExistentQueue, not
	// empty: the difference between "nothing sent yet" and "wrong region".
	status, raw := get(t, srv.URL+"/_aws/sqs/messages/us-east-1/000000000000/regional", nil)
	if status != http.StatusBadRequest || !strings.Contains(raw, "NonExistentQueue") {
		t.Errorf("wrong region: status %d, body %s; want 400 NonExistentQueue", status, raw)
	}
	// And a request naming no queue at all says which forms it accepts.
	status, raw = get(t, srv.URL+"/_aws/sqs/messages", nil)
	if status != http.StatusBadRequest || !strings.Contains(raw, "MissingParameter") || !strings.Contains(raw, "QueueUrl") {
		t.Errorf("no queue named: status %d, body %s; want 400 MissingParameter naming QueueUrl", status, raw)
	}
	// Errors follow the Accept header too.
	status, raw = get(t, srv.URL+"/_aws/sqs/messages", map[string]string{"Accept": "application/json"})
	if status != http.StatusBadRequest || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		t.Errorf("no queue named, JSON: status %d, body %s; want a JSON error", status, raw)
	}
}

// TestAWSCompatSQS_hidesInvisibleAndDelayedUnlessAsked covers LocalStack's two
// switches. By default the peek shows what ReceiveMessage would return now;
// ShowInvisible adds messages inside a visibility timeout and ShowDelayed adds
// those inside a send delay.
func TestAWSCompatSQS_hidesInvisibleAndDelayedUnlessAsked(t *testing.T) {
	srv := newAWSCompatServer(t)
	queueURL := createQueueWithMessage(t, srv, "visibility", "")
	sqsJSON(t, srv, "SendMessage", map[string]any{"QueueUrl": queueURL, "MessageBody": "later", "DelaySeconds": 60}, "")
	peek := func(query string) string {
		status, raw := get(t, srv.URL+"/_aws/sqs/messages?QueueUrl="+url.QueryEscape(queueURL)+query, nil)
		if status != http.StatusOK {
			t.Fatalf("peek%s: status %d, body %s", query, status, raw)
		}
		return raw
	}

	// Given: "hello" is visible and "later" is delayed.
	if raw := peek(""); !strings.Contains(raw, "<Body>hello</Body>") || strings.Contains(raw, "<Body>later</Body>") {
		t.Errorf("default peek: %s; want hello and not the delayed message", raw)
	}
	if raw := peek("&ShowDelayed=true"); !strings.Contains(raw, "<Body>later</Body>") {
		t.Errorf("ShowDelayed=true: %s; want the delayed message", raw)
	}

	// When: "hello" is received, so it is inside a visibility timeout.
	received := sqsJSON(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL, "VisibilityTimeout": 60}, "")
	if list, _ := received["Messages"].([]any); len(list) != 1 {
		t.Fatalf("ReceiveMessage returned %d messages, want 1: %v", len(list), received)
	}

	// Then: it is hidden until asked for, and a delayed message is not an
	// invisible one — each switch shows only its own kind.
	if raw := peek(""); strings.Contains(raw, "<Body>hello</Body>") {
		t.Errorf("default peek after receive: %s; want the in-flight message hidden", raw)
	}
	if raw := peek("&ShowInvisible=true"); !strings.Contains(raw, "<Body>hello</Body>") || strings.Contains(raw, "<Body>later</Body>") {
		t.Errorf("ShowInvisible=true: %s; want the in-flight message and not the delayed one", raw)
	}
	if raw := peek("&ShowInvisible=true&ShowDelayed=true"); !strings.Contains(raw, "<Body>hello</Body>") || !strings.Contains(raw, "<Body>later</Body>") {
		t.Errorf("both switches: %s; want both messages", raw)
	}
}

// ---- the rest of /_aws/ ----------------------------------------------------

// TestAWSCompatNamespace_404sNamingTheOvercastEndpoint is the reason the
// wildcard exists: an /_aws/ path Overcast does not serve must say where the
// data is, not answer as S3.
func TestAWSCompatNamespace_404sNamingTheOvercastEndpoint(t *testing.T) {
	srv := newAWSCompatServer(t)

	status, body, raw := getJSON(t, srv.URL+"/_aws/sns/sms-messages?phoneNumber=%2B15550001111")
	if status != http.StatusNotFound {
		t.Fatalf("GET /_aws/sns/sms-messages: status = %d, want 404 (body: %s)", status, raw)
	}
	message, _ := body["message"].(string)
	if want := "/_overcast/ses/inbox/messages"; !strings.Contains(message, want) {
		t.Errorf("message %q does not name %q", message, want)
	}
	if strings.Contains(raw, "NoSuchBucket") || strings.Contains(raw, "NoSuchKey") {
		t.Errorf("answered as S3: %s", raw)
	}
	// The body also says what does work under the prefix, so a reader landing
	// on the wrong path learns the right one.
	served, _ := body["served"].([]any)
	if len(served) == 0 || !strings.Contains(raw, "/_aws/ses") || !strings.Contains(raw, "/_aws/sqs/messages") {
		t.Errorf("404 body does not list the served paths: %s", raw)
	}

	// A path with no equivalent says so rather than pointing nowhere.
	status, body, _ = getJSON(t, srv.URL+"/_aws/dynamodb/expired")
	message, _ = body["message"].(string)
	if status != http.StatusNotFound || !strings.Contains(message, "no equivalent") {
		t.Errorf("GET /_aws/dynamodb/expired: status %d, message %q; want a 404 saying there is no equivalent", status, message)
	}

	// And one nobody mapped still points at the namespace.
	status, body, _ = getJSON(t, srv.URL+"/_aws/something-we-never-had")
	message, _ = body["message"].(string)
	if status != http.StatusNotFound || !strings.Contains(message, "/_overcast/") {
		t.Errorf("unknown path: status %d, message %q; want a 404 pointing at /_overcast/", status, message)
	}
}

// TestAWSCompatEndpointMapNamesOnlyUnservedPaths: the 404's map must not name
// a path Overcast actually answers — such an entry could never be reached, so
// it could only go stale. Same rule as localStackEndpointMap.
func TestAWSCompatEndpointMapNamesOnlyUnservedPaths(t *testing.T) {
	srv := newAWSCompatServer(t)

	for _, m := range []map[string]string{awsCompatReplacements, awsCompatUnavailable} {
		for endpoint := range m {
			status, _, _ := getJSON(t, srv.URL+"/_aws/"+endpoint)
			if status != http.StatusNotFound {
				t.Errorf("/_aws/%s answers %d — it is served, so remove it from the 404 map", endpoint, status)
			}
		}
	}
}

// TestAWSCompatServedPathsAreNotClaimedByTheWildcard guards the routing
// precedence: chi must prefer each static path over /_aws/*.
func TestAWSCompatServedPathsAreNotClaimedByTheWildcard(t *testing.T) {
	srv := newAWSCompatServer(t)

	for _, path := range []string{"/_aws/ses", "/_aws/sqs/messages", "/_aws/sqs/messages/us-east-1/000000000000/nope"} {
		status, raw := get(t, srv.URL+path, nil)
		if status == http.StatusNotFound {
			t.Errorf("%s = 404 — the wildcard claimed it (body: %s)", path, raw)
		}
	}
}

// TestRegionFromQueueHost pins which hostnames name a region and which do
// not. The negative cases matter more: reading a region out of a segment
// that is not one scopes the peek to a region nothing else can name, and
// reports the queue as missing when it is simply looked for in the wrong
// place.
func TestRegionFromQueueHost(t *testing.T) {
	cases := map[string]string{
		"sqs.us-east-1.localhost.localstack.cloud": "us-east-1",
		"sqs.ap-southeast-2.amazonaws.com":         "ap-southeast-2",
		"SQS.US-GOV-WEST-1.AMAZONAWS.COM":          "us-gov-west-1",
		"eu-west-1.queue.amazonaws.com":            "eu-west-1",
		"sqs.localhost.localstack.cloud":           "",
		"localhost":                                "",
		"127.0.0.1":                                "",
		"localhost.localstack.cloud":               "",
		"queue.amazonaws.com":                      "",
	}
	for host, want := range cases {
		if got := regionFromQueueHost(host); got != want {
			t.Errorf("regionFromQueueHost(%q) = %q, want %q", host, got, want)
		}
	}
}
