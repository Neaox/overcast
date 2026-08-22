// golden_test.go contains wire-byte golden tests for SQS — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// SQS is the only one of the five §5.1 priority services that serves BOTH
// wire protocols: JSON (application/x-amz-json-1.0, X-Amz-Target:
// AmazonSQS.<Action>) and AWS Query/XML (form-encoded, Action=<Action>) — see
// internal/services/sqs/typed_ops.go's SupportedProtocols (JSON10, JSON11,
// RPCv2CBOR, QueryXML). The Query path is a JSON→XML conversion layer
// (internal/services/sqs/query.go's writeQueryXMLFromJSON) rather than a
// second independent implementation, but that conversion is exactly where a
// future refactor could silently drift, so every operation below is
// golden'd on both protocols, in separate fixture directories
// (goldens/json/<Action>.json and goldens/query/<Action>.json).
//
// Coverage — the 11 queue-lifecycle/management operations already proven
// stable by the existing (non-golden) tests in protocol_dispatch_golden_test.go
// and query_queue_url_path_test.go:
//
//	CreateQueue, GetQueueUrl, ListQueues, SetQueueAttributes,
//	GetQueueAttributes, TagQueue, ListQueueTags, UntagQueue,
//	ListDeadLetterSourceQueues, PurgeQueue, DeleteQueue
//
// Plus one error-shape golden (JSON only): GetQueueUrl for a queue name that
// doesn't exist, pinning the AWS.SimpleQueueService.NonExistentQueue fault.
//
// Excluded (message-plane / non-deterministic, per the plan's §3 non-goal):
//
//   - SendMessage, ReceiveMessage, DeleteMessage, ChangeMessageVisibility and
//     their *Batch variants: internal/services/sqs/handler_message.go mints
//     every MessageId and receipt handle via uuid.New() — genuinely random on
//     every call, not just between record and assert. These already have
//     behavioral integration tests (sqs_test.go, protocol_dispatch_test.go).
//   - AddPermission, RemovePermission: unconditional 501 stubs, already
//     covered by TestProtocolDispatch_remainingSQSOperations — no wire-format
//     value in a byte golden.
//
// Determinism: helpers.WithMockClock() fixes any clock-derived field, but
// every QueueUrl embeds the ephemeral host:port httptest.NewServer bound for
// this test run (see canonicalQueueURL/queueURLBase in
// internal/services/sqs/handler.go) — different on every invocation.
// normalizeSQSHost regex-redacts the scheme+host+port prefix of any queue URL
// to a fixed placeholder before comparison. Separately, the Query/XML
// protocol embeds the per-request UUID inside the response body itself
// (query.go's writeQueryXMLFromJSON writes <ResponseMetadata><RequestId>),
// unlike JSON responses which only carry it in the (excluded-from-comparison)
// x-amzn-requestid header — sqsGoldenQueryCall pins that header to a fixed
// value (protocol.RequestIDFromContext honours a caller-supplied
// x-amzn-requestid for exactly this reason) so the embedded RequestId element
// is reproducible instead of needing its own regex.
//
// Record: go test ./tests/integration/sqs/ -run TestGolden -record
// Assert: go test ./tests/integration/sqs/ -run TestGolden
package sqs_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const (
	sqsJSONGoldenDir  = "goldens/json"
	sqsQueryGoldenDir = "goldens/query"
)

// goldenRequestID pins the Query/XML protocol's embedded ResponseMetadata/
// RequestId element (see the package comment above) so it is reproducible
// across record and repeated assert runs.
const goldenRequestID = "00000000-0000-0000-0000-000000000000"

// queueURLHostRE matches the scheme+host+port prefix of any queue URL this
// emulator hands back — httptest.NewServer binds an ephemeral port, so this
// is different on every test run regardless of the mock clock.
var queueURLHostRE = regexp.MustCompile(`http://[^/"<]+/(\d{12}/[\w.-]+)`)

// normalizeSQSHost redacts the ephemeral host:port of every queue URL in a
// response body to a fixed placeholder, leaving the account-id/queue-name
// suffix (already deterministic) intact.
func normalizeSQSHost(body []byte) []byte {
	return queueURLHostRE.ReplaceAll(body, []byte("http://localhost:0/$1"))
}

// sqsGoldenQueryCall sends a Query-protocol request with a pinned
// x-amzn-requestid header, so the RequestId embedded in the response body
// (see the package comment) is reproducible without its own regex.
func sqsGoldenQueryCall(t *testing.T, srv *helpers.TestServer, action string, form url.Values) *http.Response {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("Action", action)
	form.Set("Version", "2012-11-05")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("sqsGoldenQueryCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("x-amzn-requestid", goldenRequestID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sqsGoldenQueryCall: do: %v", err)
	}
	return resp
}

// goldenQueueSetup creates a queue (over JSON — creation protocol is
// irrelevant to the op under test) and returns its URL and path
// (accountID/name, the form the Query protocol's queue-scoped ops expect
// when QueueUrl is passed as a body parameter rather than the endpoint).
func goldenQueueSetup(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	return createQueue(t, srv, name)
}

func TestGolden_CreateQueue(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		resp := sqsCall(t, srv, "CreateQueue", map[string]any{
			"QueueName":  "golden-queue",
			"Attributes": map[string]string{"VisibilityTimeout": "45"},
		})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "CreateQueue", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		resp := sqsGoldenQueryCall(t, srv, "CreateQueue", url.Values{
			"QueueName":         {"golden-queue"},
			"Attribute.1.Name":  {"VisibilityTimeout"},
			"Attribute.1.Value": {"45"},
		})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "CreateQueue", resp, normalizeSQSHost)
	})
}

func TestGolden_GetQueueUrl(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsCall(t, srv, "GetQueueUrl", map[string]any{"QueueName": "golden-queue"})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "GetQueueUrl", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsGoldenQueryCall(t, srv, "GetQueueUrl", url.Values{"QueueName": {"golden-queue"}})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "GetQueueUrl", resp, normalizeSQSHost)
	})
}

// TestGolden_GetQueueUrl_notFound pins the
// AWS.SimpleQueueService.NonExistentQueue fault (JSON only — one error golden
// suffices per the task's scope).
func TestGolden_GetQueueUrl_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	resp := sqsCall(t, srv, "GetQueueUrl", map[string]any{"QueueName": "ghost-queue"})
	helpers.GoldenTest(t, sqsJSONGoldenDir, "GetQueueUrl_notFound", resp, normalizeSQSHost)
}

func TestGolden_ListQueues(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		goldenQueueSetup(t, srv, "golden-queue-a")
		goldenQueueSetup(t, srv, "golden-queue-b")
		resp := sqsCall(t, srv, "ListQueues", map[string]any{"QueueNamePrefix": "golden"})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "ListQueues", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		goldenQueueSetup(t, srv, "golden-queue-a")
		goldenQueueSetup(t, srv, "golden-queue-b")
		resp := sqsGoldenQueryCall(t, srv, "ListQueues", url.Values{"QueueNamePrefix": {"golden"}})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "ListQueues", resp, normalizeSQSHost)
	})
}

func TestGolden_SetQueueAttributes(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
			"QueueUrl":   queueURL,
			"Attributes": map[string]string{"ReceiveMessageWaitTimeSeconds": "2"},
		})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "SetQueueAttributes", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsGoldenQueryCall(t, srv, "SetQueueAttributes", url.Values{
			"QueueUrl":          {queueURL},
			"Attribute.1.Name":  {"ReceiveMessageWaitTimeSeconds"},
			"Attribute.1.Value": {"2"},
		})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "SetQueueAttributes", resp, normalizeSQSHost)
	})
}

func TestGolden_GetQueueAttributes(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
			"QueueUrl":       queueURL,
			"AttributeNames": []string{"VisibilityTimeout", "ReceiveMessageWaitTimeSeconds"},
		})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "GetQueueAttributes", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsGoldenQueryCall(t, srv, "GetQueueAttributes", url.Values{
			"QueueUrl":        {queueURL},
			"AttributeName.1": {"VisibilityTimeout"},
			"AttributeName.2": {"ReceiveMessageWaitTimeSeconds"},
		})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "GetQueueAttributes", resp, normalizeSQSHost)
	})
}

func TestGolden_TagQueue(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsCall(t, srv, "TagQueue", map[string]any{
			"QueueUrl": queueURL,
			"Tags":     map[string]string{"env": "test"},
		})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "TagQueue", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsGoldenQueryCall(t, srv, "TagQueue", url.Values{
			"QueueUrl":    {queueURL},
			"Tag.1.Key":   {"env"},
			"Tag.1.Value": {"test"},
		})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "TagQueue", resp, normalizeSQSHost)
	})
}

func TestGolden_ListQueueTags(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		tagResp := sqsCall(t, srv, "TagQueue", map[string]any{
			"QueueUrl": queueURL, "Tags": map[string]string{"env": "test"},
		})
		tagResp.Body.Close()
		helpers.AssertStatus(t, tagResp, http.StatusOK)

		resp := sqsCall(t, srv, "ListQueueTags", map[string]any{"QueueUrl": queueURL})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "ListQueueTags", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		tagResp := sqsGoldenQueryCall(t, srv, "TagQueue", url.Values{
			"QueueUrl": {queueURL}, "Tag.1.Key": {"env"}, "Tag.1.Value": {"test"},
		})
		tagResp.Body.Close()
		helpers.AssertStatus(t, tagResp, http.StatusOK)

		resp := sqsGoldenQueryCall(t, srv, "ListQueueTags", url.Values{"QueueUrl": {queueURL}})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "ListQueueTags", resp, normalizeSQSHost)
	})
}

func TestGolden_UntagQueue(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		tagResp := sqsCall(t, srv, "TagQueue", map[string]any{
			"QueueUrl": queueURL, "Tags": map[string]string{"env": "test"},
		})
		tagResp.Body.Close()
		helpers.AssertStatus(t, tagResp, http.StatusOK)

		resp := sqsCall(t, srv, "UntagQueue", map[string]any{
			"QueueUrl": queueURL, "TagKeys": []string{"env"},
		})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "UntagQueue", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		tagResp := sqsGoldenQueryCall(t, srv, "TagQueue", url.Values{
			"QueueUrl": {queueURL}, "Tag.1.Key": {"env"}, "Tag.1.Value": {"test"},
		})
		tagResp.Body.Close()
		helpers.AssertStatus(t, tagResp, http.StatusOK)

		resp := sqsGoldenQueryCall(t, srv, "UntagQueue", url.Values{
			"QueueUrl": {queueURL}, "TagKey.1": {"env"},
		})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "UntagQueue", resp, normalizeSQSHost)
	})
}

func TestGolden_ListDeadLetterSourceQueues(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		sourceURL := goldenQueueSetup(t, srv, "golden-source")
		dlqURL := goldenQueueSetup(t, srv, "golden-dlq")
		attrsResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
			"QueueUrl": dlqURL, "AttributeNames": []string{"QueueArn"},
		})
		var attrs struct {
			Attributes map[string]string `json:"Attributes"`
		}
		helpers.DecodeJSON(t, attrsResp, &attrs)

		policy := `{"deadLetterTargetArn":"` + attrs.Attributes["QueueArn"] + `","maxReceiveCount":"3"}`
		setResp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
			"QueueUrl":   sourceURL,
			"Attributes": map[string]string{"RedrivePolicy": policy},
		})
		setResp.Body.Close()
		helpers.AssertStatus(t, setResp, http.StatusOK)

		resp := sqsCall(t, srv, "ListDeadLetterSourceQueues", map[string]any{"QueueUrl": dlqURL})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "ListDeadLetterSourceQueues", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		sourceURL := goldenQueueSetup(t, srv, "golden-source")
		dlqURL := goldenQueueSetup(t, srv, "golden-dlq")
		attrsResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
			"QueueUrl": dlqURL, "AttributeNames": []string{"QueueArn"},
		})
		var attrs struct {
			Attributes map[string]string `json:"Attributes"`
		}
		helpers.DecodeJSON(t, attrsResp, &attrs)

		policy := `{"deadLetterTargetArn":"` + attrs.Attributes["QueueArn"] + `","maxReceiveCount":"3"}`
		setResp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
			"QueueUrl":   sourceURL,
			"Attributes": map[string]string{"RedrivePolicy": policy},
		})
		setResp.Body.Close()
		helpers.AssertStatus(t, setResp, http.StatusOK)

		resp := sqsGoldenQueryCall(t, srv, "ListDeadLetterSourceQueues", url.Values{"QueueUrl": {dlqURL}})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "ListDeadLetterSourceQueues", resp, normalizeSQSHost)
	})
}

func TestGolden_PurgeQueue(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsCall(t, srv, "PurgeQueue", map[string]any{"QueueUrl": queueURL})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "PurgeQueue", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsGoldenQueryCall(t, srv, "PurgeQueue", url.Values{"QueueUrl": {queueURL}})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "PurgeQueue", resp, normalizeSQSHost)
	})
}

func TestGolden_DeleteQueue(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsCall(t, srv, "DeleteQueue", map[string]any{"QueueUrl": queueURL})
		helpers.GoldenTest(t, sqsJSONGoldenDir, "DeleteQueue", resp, normalizeSQSHost)
	})
	t.Run("Query", func(t *testing.T) {
		srv := helpers.NewTestServer(t, helpers.WithMockClock())
		queueURL := goldenQueueSetup(t, srv, "golden-queue")
		resp := sqsGoldenQueryCall(t, srv, "DeleteQueue", url.Values{"QueueUrl": {queueURL}})
		helpers.GoldenTest(t, sqsQueryGoldenDir, "DeleteQueue", resp, normalizeSQSHost)
	})
}
