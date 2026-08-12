package lambda_test

// dead_letter_config_test.go — the API surface of DeadLetterConfig.
//
// The delivery half — an async invocation that fails ending up on the target —
// lives in internal/services/lambda/dead_letter_test.go, where a scripted
// runtime can fail on demand without Docker. What is covered here is the part
// a CloudFormation deploy actually trips over: the member has to be accepted,
// stored, and echoed back by every operation that reports a function's
// configuration, on create *and* on update.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

const deadLetterQueueARN = "arn:aws:sqs:us-east-1:000000000000:fn-dlq"

// deadLetterConfigResp is the DeadLetterConfig block as AWS returns it.
type deadLetterConfigResp struct {
	TargetArn string `json:"TargetArn"`
}

type configWithDeadLetter struct {
	FunctionName     string                `json:"FunctionName"`
	Description      string                `json:"Description"`
	DeadLetterConfig *deadLetterConfigResp `json:"DeadLetterConfig"`
}

func TestCreateFunction_deadLetterConfigIsStoredAndEchoed(t *testing.T) {
	// Given: a running server.
	srv := helpers.NewTestServer(t)

	// When: a function is created with a dead-letter queue.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":     "dlq-create-fn",
		"Runtime":          "python3.12",
		"Handler":          "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"DeadLetterConfig": map[string]any{"TargetArn": deadLetterQueueARN},
	})

	// Then: the create succeeds and reports the target back.
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var created configWithDeadLetter
	decodeJSON(t, resp, &created)
	assertDeadLetterTarget(t, "CreateFunction", created.DeadLetterConfig, deadLetterQueueARN)

	// And: GetFunctionConfiguration reports the same target.
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/dlq-create-fn/configuration"), nil)
	helpers.AssertStatus(t, get, http.StatusOK)
	var fetched configWithDeadLetter
	decodeJSON(t, get, &fetched)
	assertDeadLetterTarget(t, "GetFunctionConfiguration", fetched.DeadLetterConfig, deadLetterQueueARN)

	// And: so does GetFunction, which nests the same block.
	getFn := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/dlq-create-fn"), nil)
	helpers.AssertStatus(t, getFn, http.StatusOK)
	var wrapper struct {
		Configuration configWithDeadLetter `json:"Configuration"`
	}
	decodeJSON(t, getFn, &wrapper)
	assertDeadLetterTarget(t, "GetFunction", wrapper.Configuration.DeadLetterConfig, deadLetterQueueARN)
}

func TestCreateFunction_snsDeadLetterTargetIsAccepted(t *testing.T) {
	// Given: a running server. AWS permits a standard SNS topic as well as a
	// standard SQS queue:
	// https://docs.aws.amazon.com/lambda/latest/dg/invocation-async-retain-records.html
	srv := helpers.NewTestServer(t)
	const topicARN = "arn:aws:sns:us-east-1:000000000000:fn-dlq-topic"

	// When: a function is created with an SNS dead-letter target.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":     "dlq-sns-fn",
		"Runtime":          "python3.12",
		"Handler":          "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"DeadLetterConfig": map[string]any{"TargetArn": topicARN},
	})

	// Then: it is accepted and echoed like any other target.
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var created configWithDeadLetter
	decodeJSON(t, resp, &created)
	assertDeadLetterTarget(t, "CreateFunction", created.DeadLetterConfig, topicARN)
}

func TestUpdateFunctionConfiguration_addsADeadLetterQueueToAnExistingFunction(t *testing.T) {
	// Given: a function created without one. This is the redeploy shape: the
	// first deploy creates the function, the second changes its configuration,
	// and a create-only implementation fails the second with the same 501 the
	// first no longer returns.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "dlq-update-fn")

	// When: UpdateFunctionConfiguration adds a dead-letter queue.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/dlq-update-fn/configuration"), map[string]any{
		"DeadLetterConfig": map[string]any{"TargetArn": deadLetterQueueARN},
	})

	// Then: the update succeeds and reports the target back.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var updated configWithDeadLetter
	decodeJSON(t, resp, &updated)
	assertDeadLetterTarget(t, "UpdateFunctionConfiguration", updated.DeadLetterConfig, deadLetterQueueARN)

	// And: it survives to the next read.
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/dlq-update-fn/configuration"), nil)
	var fetched configWithDeadLetter
	decodeJSON(t, get, &fetched)
	assertDeadLetterTarget(t, "GetFunctionConfiguration", fetched.DeadLetterConfig, deadLetterQueueARN)
}

func TestUpdateFunctionConfiguration_emptyTargetArnClearsTheDeadLetterQueue(t *testing.T) {
	// Given: a function that has a dead-letter queue.
	srv := helpers.NewTestServer(t)
	create := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":     "dlq-clear-fn",
		"Runtime":          "python3.12",
		"Handler":          "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"DeadLetterConfig": map[string]any{"TargetArn": deadLetterQueueARN},
	})
	helpers.AssertStatus(t, create, http.StatusCreated)
	create.Body.Close()

	// When: the target is set to the empty string — AWS's documented way of
	// removing the association, and what CloudFormation sends when the
	// property is deleted from a template.
	resp := doJSON(t, http.MethodPut, lambdaURL(srv, "/functions/dlq-clear-fn/configuration"), map[string]any{
		"DeadLetterConfig": map[string]any{"TargetArn": ""},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the function no longer reports one.
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/dlq-clear-fn/configuration"), nil)
	var fetched configWithDeadLetter
	decodeJSON(t, get, &fetched)
	if fetched.DeadLetterConfig != nil && fetched.DeadLetterConfig.TargetArn != "" {
		t.Errorf("DeadLetterConfig.TargetArn = %q after clearing, want it gone", fetched.DeadLetterConfig.TargetArn)
	}
}

func TestCreateFunction_deadLetterTargetArnMustMatchAWSsPattern(t *testing.T) {
	// Given: a running server.
	srv := helpers.NewTestServer(t)

	// When: the target is not an ARN.
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":     "dlq-bad-arn-fn",
		"Runtime":          "python3.12",
		"Handler":          "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"DeadLetterConfig": map[string]any{"TargetArn": "not-an-arn"},
	})

	// Then: Lambda's own modeled constraint rejects it, and nothing is stored.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/dlq-bad-arn-fn/configuration"), nil)
	helpers.AssertStatus(t, get, http.StatusNotFound)
	get.Body.Close()
}

func TestGetFunctionConfiguration_withoutADeadLetterQueueOmitsTheBlock(t *testing.T) {
	// Given: an ordinary function.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "dlq-absent-fn")

	// When: its configuration is read.
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/dlq-absent-fn/configuration"), nil)
	helpers.AssertStatus(t, get, http.StatusOK)

	// Then: no DeadLetterConfig is reported, as on AWS.
	var fetched configWithDeadLetter
	decodeJSON(t, get, &fetched)
	if fetched.DeadLetterConfig != nil {
		t.Errorf("DeadLetterConfig = %+v for a function that configured none, want it absent", fetched.DeadLetterConfig)
	}
}

// TestInvokeFunction_asyncFailureReachesTheDeadLetterQueue is the whole feature
// end to end, through the real router: a failing asynchronous invocation, a
// real SQS queue, and the message AWS would have put on it.
//
// It needs no Docker and no handler that raises on purpose — without a Docker
// daemon the stub runtime answers every invocation with a Runtime.
// DockerUnavailable function error, which is exactly the failure the
// dead-letter queue exists to catch.
func TestInvokeFunction_asyncFailureReachesTheDeadLetterQueue(t *testing.T) {
	// Given: a queue, and a function whose dead-letter target is that queue.
	srv := helpers.NewTestServer(t)
	sqsCall(t, srv, "CreateQueue", map[string]any{"QueueName": "async-failure-dlq"})
	create := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions"), map[string]any{
		"FunctionName":     "dlq-e2e-fn",
		"Runtime":          "nodejs20.x",
		"Handler":          "index.handler",
		"Role":             "arn:aws:iam::000000000000:role/lambda-role",
		"Code":             map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		"DeadLetterConfig": map[string]any{"TargetArn": "arn:aws:sqs:us-east-1:000000000000:async-failure-dlq"},
	})
	helpers.AssertStatus(t, create, http.StatusCreated)
	create.Body.Close()

	// When: it is invoked asynchronously with an event that will fail.
	const event = `{"orderId":"a81ddca6"}`
	req, err := http.NewRequest(http.MethodPost, lambdaURL(srv, "/functions/dlq-e2e-fn/invocations"), strings.NewReader(event))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Amz-Invocation-Type", "Event")
	invoke, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	helpers.AssertStatus(t, invoke, http.StatusAccepted)
	invoke.Body.Close()

	// Then: the event turns up on the queue, as sent — the body is the original
	// event, not an invocation record — carrying the three documented
	// attributes. The invocation answered 202 and runs on its own goroutine, so
	// the arrival is awaited rather than assumed.
	var message sqsMessage
	helpers.Eventually(t, 10*time.Second, 20*time.Millisecond, func() bool {
		messages := receiveDeadLetterMessages(t, srv, "async-failure-dlq")
		if len(messages) == 0 {
			return false
		}
		message = messages[0]
		return true
	}, "the failed asynchronous invocation never reached the dead-letter queue")

	if message.Body != event {
		t.Errorf("dead-letter message body = %q, want the original event %q", message.Body, event)
	}
	for _, name := range []string{"RequestID", "ErrorCode", "ErrorMessage"} {
		attr, ok := message.MessageAttributes[name]
		if !ok {
			t.Errorf("dead-letter message has no %s attribute", name)
			continue
		}
		if attr.StringValue == "" {
			t.Errorf("dead-letter message attribute %s is empty", name)
		}
	}
	if got := message.MessageAttributes["ErrorCode"].StringValue; got != "200" {
		t.Errorf("ErrorCode = %q, want 200 — the invoke API answers 200 for a function error", got)
	}
	if got := message.MessageAttributes["ErrorCode"].DataType; got != "Number" {
		t.Errorf("ErrorCode DataType = %q, want Number", got)
	}
}

type sqsMessageAttribute struct {
	DataType    string `json:"DataType"`
	StringValue string `json:"StringValue"`
}

type sqsMessage struct {
	Body              string                         `json:"Body"`
	MessageAttributes map[string]sqsMessageAttribute `json:"MessageAttributes"`
}

// receiveDeadLetterMessages reads whatever is currently visible on a queue,
// asking for every message attribute.
func receiveDeadLetterMessages(t *testing.T, srv *helpers.TestServer, queue string) []sqsMessage {
	t.Helper()
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":              queue,
		"MessageAttributeNames": []string{"All"},
	})
	var out struct {
		Messages []sqsMessage `json:"Messages"`
	}
	decodeJSON(t, resp, &out)
	return out.Messages
}

// sqsCall makes an SQS JSON-protocol call against the shared test server. The
// caller owns the returned body.
func sqsCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s request: %v", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("%s returned %d: %s", action, resp.StatusCode, helpers.ReadBody(t, resp))
	}
	return resp
}

func assertDeadLetterTarget(t *testing.T, operation string, got *deadLetterConfigResp, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: DeadLetterConfig is absent, want TargetArn %s", operation, want)
	}
	if got.TargetArn != want {
		t.Errorf("%s: DeadLetterConfig.TargetArn = %q, want %q", operation, got.TargetArn, want)
	}
}
