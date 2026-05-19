// Package router_test — region isolation tests.
//
// These tests verify that per-request region (extracted from the SigV4
// Authorization header Credential scope) correctly namespaces resources,
// so that two clients configured for different regions cannot see each
// other's resources.
//
// Global services (S3) are also tested to confirm they are NOT region-scoped.
package router_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// sigV4Auth returns a minimal Authorization header that carries the given
// region in its Credential scope.  Signature validation is disabled in tests
// so the credentials themselves are irrelevant — only the region field matters.
//
//	Authorization: AWS4-HMAC-SHA256 Credential=test/20240101/<region>/sqs/aws4_request, ...
func sigV4Auth(region, service string) string {
	return fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=test/20240101/%s/%s/aws4_request, SignedHeaders=host, Signature=aabbcc",
		region, service,
	)
}

// ─── SQS ──────────────────────────────────────────────────────────────────────

// regionalSQSCall issues a JSON SQS request with an explicit region in the
// SigV4 Authorization header.
func regionalSQSCall(t *testing.T, srv *helpers.TestServer, region, action string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	req.Header.Set("Authorization", sigV4Auth(region, "sqs"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s: %v", action, err)
	}
	return resp
}

func TestRegion_SQS_queueIsolatedByRegion(t *testing.T) {
	srv := newServer(t)

	// Given: a queue created in us-east-1
	createResp := regionalSQSCall(t, srv, "us-east-1", "CreateQueue", map[string]any{"QueueName": "regional-queue"})
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateQueue us-east-1: status %d", createResp.StatusCode)
	}

	// When: we look up that queue from us-west-2
	getResp := regionalSQSCall(t, srv, "us-west-2", "GetQueueUrl", map[string]any{"QueueName": "regional-queue"})
	defer getResp.Body.Close()

	// Then: it is not found — the queue lives in us-east-1, not us-west-2
	if getResp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 NonExistentQueue when querying from wrong region, got %d", getResp.StatusCode)
	}
	assertJSONErrorCode(t, getResp, "AWS.SimpleQueueService.NonExistentQueue")
}

func TestRegion_SQS_sameNameDifferentRegions_independent(t *testing.T) {
	srv := newServer(t)

	// Given: queues with the same name in two different regions
	r1 := regionalSQSCall(t, srv, "us-east-1", "CreateQueue", map[string]any{"QueueName": "shared-name"})
	r1.Body.Close()
	r2 := regionalSQSCall(t, srv, "eu-west-1", "CreateQueue", map[string]any{"QueueName": "shared-name"})
	r2.Body.Close()

	// When: we list queues in each region
	listEast := regionalSQSCall(t, srv, "us-east-1", "ListQueues", map[string]any{})
	defer listEast.Body.Close()
	var eastResult struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	if err := json.NewDecoder(listEast.Body).Decode(&eastResult); err != nil {
		t.Fatalf("decode east list: %v", err)
	}

	listWest := regionalSQSCall(t, srv, "eu-west-1", "ListQueues", map[string]any{})
	defer listWest.Body.Close()
	var westResult struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	if err := json.NewDecoder(listWest.Body).Decode(&westResult); err != nil {
		t.Fatalf("decode west list: %v", err)
	}

	// Then: each region sees exactly one queue and the URLs contain the correct region
	if len(eastResult.QueueUrls) != 1 {
		t.Errorf("us-east-1: expected 1 queue URL, got %d: %v", len(eastResult.QueueUrls), eastResult.QueueUrls)
	}
	if len(westResult.QueueUrls) != 1 {
		t.Errorf("eu-west-1: expected 1 queue URL, got %d: %v", len(westResult.QueueUrls), westResult.QueueUrls)
	}
}

func TestRegion_SQS_arnContainsRequestRegion(t *testing.T) {
	srv := newServer(t)

	// Given / When: queue created from eu-central-1
	resp := regionalSQSCall(t, srv, "eu-central-1", "CreateQueue", map[string]any{"QueueName": "arn-check"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateQueue: status %d", resp.StatusCode)
	}

	// Get its attributes to read the ARN
	var createResult struct {
		QueueUrl string `json:"QueueUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResult); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	attrResp := regionalSQSCall(t, srv, "eu-central-1", "GetQueueAttributes", map[string]any{
		"QueueUrl":       createResult.QueueUrl,
		"AttributeNames": []string{"All"},
	})
	defer attrResp.Body.Close()
	var attrResult struct {
		Attributes map[string]string `json:"Attributes"`
	}
	if err := json.NewDecoder(attrResp.Body).Decode(&attrResult); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}

	// Then: the QueueArn must include eu-central-1
	queueARN := attrResult.Attributes["QueueArn"]
	if queueARN == "" {
		t.Fatal("QueueArn not in attributes")
	}
	if !containsStr(queueARN, "eu-central-1") {
		t.Errorf("expected QueueArn to contain 'eu-central-1', got %q", queueARN)
	}
}

// ─── SNS ──────────────────────────────────────────────────────────────────────

func regionalSNSCall(t *testing.T, srv *helpers.TestServer, region, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewBufferString(params.Encode()))
	if err != nil {
		t.Fatalf("build SNS request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", sigV4Auth(region, "sns"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do SNS request %s: %v", action, err)
	}
	return resp
}

func TestRegion_SNS_topicIsolatedByRegion(t *testing.T) {
	srv := newServer(t)

	// Given: a topic created in ap-southeast-1
	createResp := regionalSNSCall(t, srv, "ap-southeast-1", "CreateTopic", url.Values{"Name": {"isolated-topic"}})
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTopic: status %d", createResp.StatusCode)
	}

	// When: we list topics in ap-northeast-1
	listResp := regionalSNSCall(t, srv, "ap-northeast-1", "ListTopics", url.Values{})
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("ListTopics: status %d", listResp.StatusCode)
	}

	// Then: the topic created in ap-southeast-1 must not appear
	body := readBodyStr(t, listResp)
	if containsStr(body, "isolated-topic") {
		t.Errorf("topic created in ap-southeast-1 should not appear when listing ap-northeast-1 topics")
	}
}

func TestRegion_SNS_arnContainsRequestRegion(t *testing.T) {
	srv := newServer(t)

	// Given / When: topic created in eu-west-2
	resp := regionalSNSCall(t, srv, "eu-west-2", "CreateTopic", url.Values{"Name": {"region-arn-topic"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTopic: status %d", resp.StatusCode)
	}

	var result struct {
		XMLName xml.Name `xml:"CreateTopicResponse"`
		Result  struct {
			TopicArn string `xml:"TopicArn"`
		} `xml:"CreateTopicResult"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode SNS response: %v", err)
	}

	// Then: the ARN must include eu-west-2
	if !containsStr(result.Result.TopicArn, "eu-west-2") {
		t.Errorf("expected TopicArn to contain 'eu-west-2', got %q", result.Result.TopicArn)
	}
}

// ─── DynamoDB ─────────────────────────────────────────────────────────────────

func regionalDDBCall(t *testing.T, srv *helpers.TestServer, region, target string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build DDB request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+target)
	req.Header.Set("Authorization", sigV4Auth(region, "dynamodb"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do DDB request %s: %v", target, err)
	}
	return resp
}

func TestRegion_DynamoDB_tableIsolatedByRegion(t *testing.T) {
	srv := newServer(t)

	// Given: a table created in us-west-2
	createResp := regionalDDBCall(t, srv, "us-west-2", "CreateTable", map[string]any{
		"TableName":            "region-isolated-table",
		"BillingMode":          "PAY_PER_REQUEST",
		"AttributeDefinitions": []map[string]any{{"AttributeName": "pk", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "pk", "KeyType": "HASH"}},
	})
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTable us-west-2: status %d", createResp.StatusCode)
	}

	// When: we describe that table from eu-central-1
	descResp := regionalDDBCall(t, srv, "eu-central-1", "DescribeTable", map[string]any{
		"TableName": "region-isolated-table",
	})
	defer descResp.Body.Close()

	// Then: ResourceNotFoundException — table doesn't exist in eu-central-1
	if descResp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 ResourceNotFoundException from wrong region, got %d", descResp.StatusCode)
	}
	assertJSONErrorCode(t, descResp, "ResourceNotFoundException")
}

func TestRegion_DynamoDB_tableARNContainsRequestRegion(t *testing.T) {
	srv := newServer(t)

	// Given / When: table created in sa-east-1
	resp := regionalDDBCall(t, srv, "sa-east-1", "CreateTable", map[string]any{
		"TableName":            "arn-region-check",
		"BillingMode":          "PAY_PER_REQUEST",
		"AttributeDefinitions": []map[string]any{{"AttributeName": "pk", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "pk", "KeyType": "HASH"}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTable: status %d", resp.StatusCode)
	}
	var result struct {
		TableDescription struct {
			TableArn string `json:"TableArn"`
		} `json:"TableDescription"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode CreateTable: %v", err)
	}

	// Then: TableArn must include sa-east-1
	if !containsStr(result.TableDescription.TableArn, "sa-east-1") {
		t.Errorf("expected TableArn to contain 'sa-east-1', got %q", result.TableDescription.TableArn)
	}
}

// ─── S3 (global — no region isolation) ───────────────────────────────────────

func regionalS3Call(t *testing.T, srv *helpers.TestServer, region, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build S3 request: %v", err)
	}
	req.Header.Set("Authorization", sigV4Auth(region, "s3"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do S3 request: %v", err)
	}
	return resp
}

func TestRegion_S3_bucketIsGlobal(t *testing.T) {
	srv := newServer(t)

	// Given: a bucket created with a us-east-1 client
	putResp := regionalS3Call(t, srv, "us-east-1", http.MethodPut, "/global-bucket")
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT bucket: status %d", putResp.StatusCode)
	}

	// When: we HEAD the bucket from ap-east-1 (a different region)
	headResp := regionalS3Call(t, srv, "ap-east-1", http.MethodHead, "/global-bucket")
	defer headResp.Body.Close()

	// Then: the bucket is visible — S3 is a global service
	if headResp.StatusCode != http.StatusOK {
		t.Errorf("S3 bucket created in us-east-1 should be visible from ap-east-1 (global), got %d", headResp.StatusCode)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// newServer is a convenience alias — router_test already imports helpers.
func newServer(t *testing.T) *helpers.TestServer {
	t.Helper()
	return helpers.NewTestServer(t)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func readBodyStr(t *testing.T, resp *http.Response) string {
	t.Helper()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.String()
}

func assertJSONErrorCode(t *testing.T, resp *http.Response, wantCode string) {
	t.Helper()
	var result struct {
		Code    string `json:"__type"`
		Message string `json:"message"`
	}
	body := readBodyStr(t, resp)
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("assertJSONErrorCode: parse body %q: %v", body, err)
	}
	if result.Code != wantCode {
		t.Errorf("expected error code %q, got %q (body: %s)", wantCode, result.Code, body)
	}
}
