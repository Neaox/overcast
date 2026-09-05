package eventbridge_test

// lambda_permission_test.go — EventBridge → Lambda under
// OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY.
//
// "For Lambda, Amazon SNS, and Amazon SQS targets, you can use either an IAM
// execution role or a resource-based policy… If no execution role is
// configured, EventBridge uses resource-based policies on the target resource."
// A target the policy refuses is an invocation that fails permanently — the
// FailedInvocations metric on AWS, and here the same dropped/dead-lettered
// outcome every other undeliverable target gets.
//
// https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-use-resource-based.html

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// seedFunctionForPermission writes a function record straight into the store;
// these assert on whether the target is authorised, not on what it returns.
func seedFunctionForPermission(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	arn := "arn:aws:lambda:us-east-1:000000000000:function:" + name
	fn := map[string]any{
		"name": name, "arn": arn, "runtime": "nodejs20.x", "handler": "index.handler",
		"state": "Active", "timeout": 30, "memory_size": 128,
	}
	encoded, err := json.Marshal(fn)
	if err != nil {
		t.Fatalf("marshalling the seeded function: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "lambda:functions", "us-east-1/"+name, string(encoded)); err != nil {
		t.Fatalf("seeding the function: %v", err)
	}
	return arn
}

func addPermissionForRule(t *testing.T, srv *helpers.TestServer, functionName string, body map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the permission: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/2015-03-31/functions/"+functionName+"/policy", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("AddPermission: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
}

func TestPutEvents_lambdaTargetWithoutPermissionIsDropped(t *testing.T) {
	// Given: enforcement on and a Lambda target whose function grants nothing
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedFunctionForPermission(t, srv, "eb-unpermitted")
	putRuleWithTarget(t, srv, "eb-unpermitted-rule", map[string]any{"Id": "fn", "Arn": arn})

	// When: a matching event is published
	putFanoutEvent(t, srv, `{"orderId":"o-1"}`)

	// Then: PutEvents still succeeds and the delivery is reported as failed
	rec := waitForDelivery(t, srv, "default", "eb-unpermitted-rule", "fn")
	if rec.Outcome != "dropped" {
		t.Fatalf("outcome = %q, want dropped", rec.Outcome)
	}
	if rec.Error == "" {
		t.Fatal("expected the dropped delivery to record why it failed")
	}

	// And: the function was never invoked
	kvs, err := srv.Store.Scan(context.Background(), "lambda:invocations", "us-east-1/eb-unpermitted:")
	if err != nil {
		t.Fatalf("scanning invocations: %v", err)
	}
	if len(kvs) > 0 {
		t.Fatalf("expected no invocation, got %d", len(kvs))
	}
}

func TestPutEvents_lambdaTargetWithPermissionDelivers(t *testing.T) {
	// Given: the permission AWS's own EventBridge statement carries — the rule
	// ARN as aws:SourceArn
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedFunctionForPermission(t, srv, "eb-permitted")
	addPermissionForRule(t, srv, "eb-permitted", map[string]any{
		"StatementId": "InvokeLambdaFunction", "Action": "lambda:InvokeFunction",
		"Principal": "events.amazonaws.com",
		// A wildcard rather than the exact rule ARN: Overcast mints a default-bus
		// rule as rule/default/{name} where AWS spells it rule/{name}, and that
		// divergence belongs to EventBridge's own ARN minting rather than to
		// this decision (#1769).
		"SourceArn": "arn:aws:events:us-east-1:000000000000:rule/*",
	})
	putRuleWithTarget(t, srv, "eb-permitted-rule", map[string]any{"Id": "fn", "Arn": arn})

	// When: a matching event is published
	putFanoutEvent(t, srv, `{"orderId":"o-2"}`)

	// Then: the delivery succeeds
	rec := waitForDelivery(t, srv, "default", "eb-permitted-rule", "fn")
	if rec.Outcome != "delivered" {
		t.Fatalf("outcome = %q (error %q), want delivered", rec.Outcome, rec.Error)
	}
}

func TestPutEvents_lambdaTargetPermissionForAnotherRuleIsDropped(t *testing.T) {
	// Given: a permission scoped to a different rule
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedFunctionForPermission(t, srv, "eb-other-rule")
	addPermissionForRule(t, srv, "eb-other-rule", map[string]any{
		"StatementId": "InvokeLambdaFunction", "Action": "lambda:InvokeFunction",
		"Principal": "events.amazonaws.com",
		"SourceArn": "arn:aws:events:us-east-1:000000000000:rule/some-other-rule",
	})
	putRuleWithTarget(t, srv, "eb-other-rule-rule", map[string]any{"Id": "fn", "Arn": arn})

	// When: a matching event is published
	putFanoutEvent(t, srv, `{"orderId":"o-3"}`)

	// Then: the SourceArn condition does not match, so the delivery is dropped
	rec := waitForDelivery(t, srv, "default", "eb-other-rule-rule", "fn")
	if rec.Outcome != "dropped" {
		t.Fatalf("outcome = %q, want dropped", rec.Outcome)
	}
}

func TestPutEvents_lambdaTargetWithRoleARNSkipsThePolicyCheck(t *testing.T) {
	// Given: a target that names an execution role, which is the other way AWS
	// lets EventBridge invoke a function
	srv := helpers.NewTestServer(t, helpers.WithEnforceLambdaResourcePolicy(true))
	arn := seedFunctionForPermission(t, srv, "eb-role-target")
	putRuleWithTarget(t, srv, "eb-role-rule", map[string]any{
		"Id": "fn", "Arn": arn,
		"RoleArn": "arn:aws:iam::000000000000:role/eventbridge-invoke",
	})

	// When: a matching event is published, with no resource policy at all
	putFanoutEvent(t, srv, `{"orderId":"o-4"}`)

	// Then: the resource policy is not consulted and the delivery succeeds
	rec := waitForDelivery(t, srv, "default", "eb-role-rule", "fn")
	if rec.Outcome != "delivered" {
		t.Fatalf("outcome = %q (error %q), want delivered", rec.Outcome, rec.Error)
	}
}

func TestPutEvents_lambdaTargetEnforcementOffNeedsNoPermission(t *testing.T) {
	// Given: the default server, with enforcement off
	srv := helpers.NewTestServer(t)
	arn := seedFunctionForPermission(t, srv, "eb-unenforced")
	putRuleWithTarget(t, srv, "eb-unenforced-rule", map[string]any{"Id": "fn", "Arn": arn})

	// When: a matching event is published with no permission anywhere
	putFanoutEvent(t, srv, `{"orderId":"o-5"}`)

	// Then: delivery is unchanged
	rec := waitForDelivery(t, srv, "default", "eb-unenforced-rule", "fn")
	if rec.Outcome != "delivered" {
		t.Fatalf("outcome = %q (error %q), want delivered", rec.Outcome, rec.Error)
	}
}
