package cloudformation

// provisioner_lambda_stabilize_test.go — when an AWS::Lambda::Function resource
// is done.
//
// CreateFunction answers immediately with the function in "Pending": Lambda is
// still provisioning it, and AWS documents that an invoke against a pending
// function fails. Real CloudFormation waits for "Active" before the resource
// completes; Overcast returned as soon as the API answered, so a stack reported
// CREATE_COMPLETE while the image was still being pulled, and the first thing
// downstream to invoke the function got the failure instead of the deploy.
//
// What is deliberately NOT tested here is whether the function works. "Active"
// means deployed, not correct — a function with a broken handler is Active on
// real AWS too, and fails at invoke. Verifying the handler would be a fidelity
// bug of its own, not a fix.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// fakeLambda is a Lambda REST endpoint that walks one function through a
// scripted sequence of states — one per GetFunctionConfiguration, with the last
// repeating.
type fakeLambda struct {
	script statusScript
	// stateReason is what the function reports about the state it is in, which
	// is where a failed image pull ends up.
	stateReason string
	// stateless answers the configuration read with no State field at all, as a
	// service that does not model function states does.
	stateless bool
}

func (f *fakeLambda) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/2015-03-31/functions":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"FunctionName": "app-fn",
			"FunctionArn":  "arn:aws:lambda:us-east-1:000000000000:function:app-fn",
			"State":        "Pending",
		})

	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/configuration"):
		body := map[string]any{"FunctionName": "app-fn"}
		if !f.stateless {
			body["State"] = f.script.next("Active")
			if f.stateReason != "" {
				body["StateReason"] = f.stateReason
				body["StateReasonCode"] = "ImagePullError"
			}
		}
		_ = json.NewEncoder(w).Encode(body)

	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
	}
}

func lambdaFunctionProps() map[string]any {
	return map[string]any{
		"FunctionName": "app-fn",
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda",
		"Code":         map[string]any{"ZipFile": "exports.handler = () => 1"},
	}
}

// A created function is not done until Lambda says it is invocable. The
// resource must keep asking rather than take CreateFunction's own "Pending" as
// the end of it.
func TestLambdaFunctionCreate_waitsForTheFunctionToBecomeActive(t *testing.T) {
	// Given: a function still pending for its first two state checks
	f := &fakeLambda{script: statusScript{statuses: []string{"Pending", "Pending", "Active"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Function",
		TemplateResource{Type: "AWS::Lambda::Function"}, lambdaFunctionProps(), rCtx)

	// Then: it completed only once the function was Active
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "app-fn" {
		t.Errorf("physical ID = %q, want %q", id, "app-fn")
	}
	if got := f.script.count(); got != 3 {
		t.Errorf("GetFunctionConfiguration calls = %d, want 3 — the resource completed while the "+
			"function was still Pending", got)
	}
	// The ARN CreateFunction answered with has to survive the wait; a GetAtt on
	// it is how most resources depend on a function.
	if arn := rCtx.Attributes["Function"]["Arn"]; arn == "" {
		t.Errorf("Arn attribute is empty; got %v", rCtx.Attributes["Function"])
	}
}

// A function that reaches "Failed" must fail the resource with Lambda's own
// account of it. AWS documents Failed as terminal — the function has to be
// deleted and recreated — so waiting one out is time spent on a resource that
// is not coming back.
func TestLambdaFunctionCreate_failsWithTheFunctionsOwnStateReason(t *testing.T) {
	// Given: a function whose image cannot be pulled
	f := &fakeLambda{
		script:      statusScript{statuses: []string{"Pending", "Failed"}},
		stateReason: "Failed to pull container image: manifest unknown",
	}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Function",
		TemplateResource{Type: "AWS::Lambda::Function"}, lambdaFunctionProps(), rCtx)

	// Then: the resource fails, carrying what Lambda recorded against it
	if err == nil {
		t.Fatal("expected the resource to fail for a function that went to Failed")
	}
	if !strings.Contains(err.Error(), "manifest unknown") {
		t.Errorf("expected the function's own StateReason, got %v", err)
	}
	if strings.Contains(err.Error(), "did not become Active within") {
		t.Errorf("a terminal state was reported as a timeout: %v", err)
	}
	// The function exists — create named it before the pull failed — and
	// rollback deletes it by that name.
	if id != "app-fn" {
		t.Errorf("physical ID = %q, want %q — a function that failed to provision still exists",
			id, "app-fn")
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("GetFunctionConfiguration calls = %d, want 2 — the wait kept polling past a "+
			"terminal state", got)
	}
}

// A function that never becomes Active must not leave the stack complete around
// it: the resource fails, saying what it was waiting for and where it got to.
func TestLambdaFunctionCreate_neverActiveFailsTheResource(t *testing.T) {
	f := &fakeLambda{script: statusScript{statuses: []string{"Pending"}}}
	p, rCtx := newTestProvisioner(t, f, newPollDrivenClock())

	_, err := p.provisionResource(context.Background(), "Function",
		TemplateResource{Type: "AWS::Lambda::Function"}, lambdaFunctionProps(), rCtx)
	if err == nil {
		t.Fatal("expected the resource to fail for a function that never became Active")
	}
	for _, want := range []string{"app-fn", "become Active", "Pending"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure reason %q does not mention %q", err.Error(), want)
		}
	}
}

// A response that carries no State at all is not a function that is not ready:
// the state field is the whole basis of the wait, so its absence has to mean
// "this is not asynchronous" rather than hold the stack open for ten minutes
// over a function that is fine.
func TestLambdaFunctionCreate_completesWhenNoStateIsReported(t *testing.T) {
	f := &fakeLambda{stateless: true}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "Function",
		TemplateResource{Type: "AWS::Lambda::Function"}, lambdaFunctionProps(), rCtx); err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
}
