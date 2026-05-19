package dynamodb_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// dynamoStreamESMTestTemplate creates a DynamoDB table with streams, a Lambda
// function, and an EventSourceMapping wired to the DynamoDB stream.
const dynamoStreamESMTestTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyTable": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "ddb-stream-esm-table",
        "BillingMode": "PAY_PER_REQUEST",
        "AttributeDefinitions": [
          { "AttributeName": "id", "AttributeType": "S" }
        ],
        "KeySchema": [
          { "AttributeName": "id", "KeyType": "HASH" }
        ],
        "StreamSpecification": {
          "StreamViewType": "NEW_AND_OLD_IMAGES"
        }
      }
    },
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "ddb-stream-esm-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": { "Service": "lambda.amazonaws.com" },
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "MyFunction": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": "ExecRole",
      "Properties": {
        "FunctionName": "ddb-stream-esm-fn",
        "Runtime": "nodejs20.x",
        "Handler": "index.handler",
        "Role": { "Fn::GetAtt": ["ExecRole", "Arn"] },
        "Code": { "ZipFile": "exports.handler = async (e) => ({ statusCode: 200, body: 'ok' });" }
      }
    },
    "StreamESM": {
      "Type": "AWS::Lambda::EventSourceMapping",
      "Properties": {
        "EventSourceArn": { "Fn::GetAtt": ["MyTable", "StreamArn"] },
        "FunctionName":   { "Fn::GetAtt": ["MyFunction", "Arn"] },
        "StartingPosition": "LATEST",
        "BatchSize": 1
      }
    }
  }
}`

// TestDDBStreamESM_invokesLambdaAfterPutItem validates end-to-end delivery:
// DynamoDB table with streams → Event Source Mapping → Lambda invocation.
func TestDDBStreamESM_invokesLambdaAfterPutItem(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)
	stackName := "ddb-stream-esm-stack"

	// When: the stack is created via CloudFormation
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{dynamoStreamESMTestTemplate},
	})
	crBody, _ := io.ReadAll(cr.Body)
	cr.Body.Close()
	if cr.StatusCode != http.StatusOK {
		t.Fatalf("CreateStack failed: %d - %s", cr.StatusCode, string(crBody))
	}

	// Then: stack reaches CREATE_COMPLETE
	t.Log("waiting for stack to reach CREATE_COMPLETE...")
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	t.Log("stack is CREATE_COMPLETE")

	// Clean up after test
	defer cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{stackName},
	})

	// And: the DynamoDB stream ESM should exist
	t.Log("verifying ESM exists...")
	esmUUID, streamArn := verifyStreamESMExists(t, srv)
	t.Logf("ESM UUID=%s StreamArn=%s", esmUUID, streamArn)

	// And: the table should have streams enabled
	t.Log("verifying stream is enabled...")
	verifyDDBStreamEnabled(t, srv)

	// When: we put an item into the table
	t.Log("putting item into table...")
	putDDBItemDirect(t, srv)

	// Then: the ESM should deliver the stream event to the Lambda
	t.Log("waiting for ESM to deliver stream event to Lambda...")
	lastResult := waitForESMProcessingResult(t, srv, esmUUID, 30*time.Second)
	t.Logf("ESM LastProcessingResult = %q", lastResult)

	if lastResult == "No records processed" || lastResult == "" {
		t.Errorf("expected ESM LastProcessingResult to change after item put, got %q", lastResult)
	} else {
		t.Logf("SUCCESS: stream event delivered to Lambda, result: %q", lastResult)
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func cfnQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-05-15")
	body := strings.NewReader(params.Encode())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cfnQuery %s: %v", action, err)
	}
	return resp
}

func waitForStackStatus(t *testing.T, srv *helpers.TestServer, stackName, wantStatus string) {
	t.Helper()
	for i := 0; i < 120; i++ {
		resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
			"StackName": []string{stackName},
		})
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		status := extractXMLTag(string(body), "StackStatus")
		if status != "" && status != "CREATE_IN_PROGRESS" {
			t.Logf("stack status: %s (iteration %d)", status, i)
		}
		if status == wantStatus {
			return
		}
		if strings.Contains(status, "FAILED") || strings.Contains(status, "ROLLBACK") {
			reason := extractXMLTag(string(body), "ResourceStatusReason")
			t.Fatalf("stack failed: %s — %s", status, reason)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("stack did not reach %s within timeout", wantStatus)
}

func extractXMLTag(xmlStr, tag string) string {
	start := strings.Index(xmlStr, "<"+tag+">")
	if start < 0 {
		return ""
	}
	start += len("<" + tag + ">")
	end := strings.Index(xmlStr[start:], "</"+tag+">")
	if end < 0 {
		return ""
	}
	return xmlStr[start : start+end]
}

func verifyStreamESMExists(t *testing.T, srv *helpers.TestServer) (esmUUID, streamArn string) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/2015-03-31/event-source-mappings/")
	if err != nil {
		t.Fatalf("ListEventSourceMappings: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var list struct {
		EventSourceMappings []struct {
			UUID                string `json:"UUID"`
			State               string `json:"State"`
			EventSourceArn      string `json:"EventSourceArn"`
			FunctionArn         string `json:"FunctionArn"`
			StartingPosition    string `json:"StartingPosition"`
			LastProcessingResult string `json:"LastProcessingResult"`
		} `json:"EventSourceMappings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode ESM list: %v", err)
	}

	if len(list.EventSourceMappings) != 1 {
		t.Fatalf("expected 1 ESM, got %d", len(list.EventSourceMappings))
	}
	esm := list.EventSourceMappings[0]
	if esm.UUID == "" {
		t.Error("expected ESM UUID")
	}
	if esm.State != "Enabled" {
		t.Errorf("expected Enabled, got %s", esm.State)
	}
	if !strings.Contains(esm.EventSourceArn, "/stream/") {
		t.Errorf("expected stream ARN, got %s", esm.EventSourceArn)
	}
	return esm.UUID, esm.EventSourceArn
}

func verifyDDBStreamEnabled(t *testing.T, srv *helpers.TestServer) {
	t.Helper()
	body := fmt.Sprintf(`{"TableName":"%s"}`, "ddb-stream-esm-table")
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.DescribeTable")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var td struct {
		Table struct {
			StreamSpecification *struct {
				StreamEnabled bool `json:"StreamEnabled"`
			} `json:"StreamSpecification"`
		} `json:"Table"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&td); err != nil {
		t.Fatalf("decode DescribeTable: %v", err)
	}
	if td.Table.StreamSpecification == nil || !td.Table.StreamSpecification.StreamEnabled {
		t.Fatal("expected stream to be enabled on table")
	}
}

func putDDBItemDirect(t *testing.T, srv *helpers.TestServer) {
	t.Helper()
	body := fmt.Sprintf(`{"TableName":"ddb-stream-esm-table","Item":{"id":{"S":"stream-%d"}}}`, time.Now().UnixMilli())
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PutItem: %v", err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func waitForESMProcessingResult(t *testing.T, srv *helpers.TestServer, esmUUID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(srv.URL + "/2015-03-31/event-source-mappings/" + esmUUID)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var esm struct {
			LastProcessingResult string `json:"LastProcessingResult"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&esm)
		resp.Body.Close()

		if esm.LastProcessingResult != "" && esm.LastProcessingResult != "No records processed" {
			return esm.LastProcessingResult
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Final attempt
	resp, _ := http.Get(srv.URL + "/2015-03-31/event-source-mappings/" + esmUUID)
	if resp != nil {
		defer resp.Body.Close()
		var esm struct {
			LastProcessingResult string `json:"LastProcessingResult"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&esm)
		return esm.LastProcessingResult
	}
	return ""
}
