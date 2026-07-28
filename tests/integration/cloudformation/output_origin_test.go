package cloudformation_test

// output_origin_test.go — stack outputs that name Overcast's *own* path-style
// URLs are re-minted onto the origin the caller reached Overcast on.
//
// The host-routed case is covered by output_rehost_test.go. This is its
// path-style sibling: a queue URL is `http://<origin>/<account>/<name>`, whose
// host is a plain address rather than a routing label, so the host-route
// grammar does not claim it.
//
// It matters because AWS SDKs resolve the SQS endpoint from the QueueUrl
// rather than from client configuration — @aws-sdk/middleware-sdk-sqs swaps in
// the QueueUrl's origin whenever it differs from the resolved endpoint and the
// client was not given an explicit one. So a queue URL Overcast hands back has
// to be dialable by whoever receives it, which is the same contract
// per-caller minting already established for SQS's own API. A stack output
// built from the configured port instead is unreachable for any caller who
// reached Overcast on a different one.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const queueURLOutputTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Q": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "origin-out-q" }
    }
  },
  "Outputs": {
    "QueueUrl": { "Value": { "Ref": "Q" } },
    "QueueArn": { "Value": { "Fn::GetAtt": ["Q", "Arn"] } }
  }
}`

func TestDescribeStacks_queueURLOutputUsesTheCallersOrigin(t *testing.T) {
	// Given: a stack whose output is a queue URL, on a server with a configured
	// origin ("localhost") that differs from where this caller reaches it
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"queue-origin-stack"},
		"TemplateBody": []string{queueURLOutputTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "queue-origin-stack", "CREATE_COMPLETE")

	// When: the stack is described by a caller that reached Overcast on a
	// different origin from the configured one — a container published on
	// another host port, or reached over another hostname. The Host header is
	// what the SDK sends and what the caller can dial back.
	const callerHost = "overcast.internal:9999"
	desc := cfnQueryWithHost(t, srv, callerHost, "DescribeStacks", url.Values{
		"StackName": []string{"queue-origin-stack"},
	})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusOK)
	body := string(readBody(t, desc))

	queueURL := outputValue(t, body, "QueueUrl")

	// Then: the output carries the port this caller reached Overcast on, not
	// the configured one. That is the reachability that matters — AWS SDKs
	// resolve the SQS endpoint from the QueueUrl, so an output built from the
	// configured port is simply undialable by a caller who arrived on another
	// (a container published on a different host port, say).
	//
	// The hostname deliberately stays the configured one: clientBaseURL treats
	// OVERCAST_HOSTNAME as authoritative and keeps only the caller's scheme and
	// port, which is the precedence #351 established. So the expected origin is
	// the configured host on the caller's port.
	if !strings.Contains(queueURL, ":9999/") {
		t.Errorf("queue URL output = %q, want the caller's port 9999", queueURL)
	}
	// And the path survives, so it is a re-origin rather than a rebuild.
	if !strings.HasSuffix(queueURL, "/origin-out-q") {
		t.Errorf("queue URL output lost its path: %q", queueURL)
	}
}

// cfnQueryWithHost is cfnQuery with an explicit Host header, so a test can act
// as a caller that reached Overcast somewhere other than its configured origin.
func cfnQueryWithHost(t *testing.T, srv *helpers.TestServer, host, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	params.Set("Version", "2010-05-15")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("build %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cfnQueryWithHost %s: %v", action, err)
	}
	return resp
}

// The ARN output must not be touched: it is not origin-bearing, and rewriting
// it would corrupt an identifier callers compare literally.
func TestDescribeStacks_arnOutputIsNotReorigined(t *testing.T) {
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"arn-untouched-stack"},
		"TemplateBody": []string{queueURLOutputTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "arn-untouched-stack", "CREATE_COMPLETE")

	desc := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"arn-untouched-stack"},
	})
	defer desc.Body.Close()
	body := string(readBody(t, desc))

	arn := outputValue(t, body, "QueueArn")
	if !strings.HasPrefix(arn, "arn:aws:sqs:") {
		t.Errorf("QueueArn output = %q, want an untouched SQS ARN", arn)
	}
}

// outputValue pulls one named output value out of a DescribeStacks body.
func outputValue(t *testing.T, body, key string) string {
	t.Helper()
	marker := "<OutputKey>" + key + "</OutputKey><OutputValue>"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("output %q not found in body:\n%s", key, body)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, "</OutputValue>")
	if j < 0 {
		t.Fatalf("output %q value not terminated in body:\n%s", key, body)
	}
	return rest[:j]
}
