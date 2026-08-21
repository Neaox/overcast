package cloudformation

// provisioner_custom_resource_dynamic_refs_test.go — dynamic references in a
// custom resource's properties (issue #606).
//
// AWS's general considerations for dynamic references say plainly: "Dynamic
// references can't be used for secure values (like those stored in Parameter
// Store or Secrets Manager) in custom resources."
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html#dynamic-references-considerations
// The restriction exists for the same reason UserData and
// AWS::CloudFormation::Init carry it: a custom resource's Lambda may log its
// own event payload, so a secret placed there is not "in CloudFormation"
// anymore.
//
// Before this fix, customResourceHandler resolved secretsmanager/ssm-secure
// references into ResourceProperties instead of rejecting them, and its
// Update method discarded its OldResourceProperties argument outright — every
// update sent an empty OldResourceProperties (actually omitted by the JSON
// marshaller, since an empty map is indistinguishable from an absent one),
// which the custom resource protocol requires ("OldResourceProperties …
// Required: Yes" for an Update request) and which made every update look
// like every property changed.
//
// These tests drive the real customResourceHandler through the provisioner
// (not a stand-in), with a fake receiver standing in for the Lambda function
// so the exact JSON payload the provisioner sends can be inspected.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeCustomResourceReceiver stands in for the Lambda function backing a
// custom resource: it records the exact cfnCustomResourceRequest payload
// the provisioner sends for each invocation, answers SUCCESS, and answers
// AmazonSSM.GetParameter / secretsmanager.GetSecretValue so a template's
// dynamic references have something to resolve against. secretsCalls counts
// GetSecretValue calls so a test can assert the secret was never even read,
// not just never sent onward.
type fakeCustomResourceReceiver struct {
	mu           sync.Mutex
	requests     []cfnCustomResourceRequest
	ssmValues    map[string]string
	secretsCalls int
}

func (f *fakeCustomResourceReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch {
	case strings.HasPrefix(r.URL.Path, "/2015-03-31/functions/"):
		var req cfnCustomResourceRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, req)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Status":"SUCCESS","PhysicalResourceId":"custom-phys-1","Data":{}}`)

	case r.Header.Get("X-Amz-Target") == "AmazonSSM.GetParameter":
		var in struct {
			Name string `json:"Name"`
		}
		_ = json.Unmarshal(body, &in)
		f.mu.Lock()
		value := f.ssmValues[in.Name]
		f.mu.Unlock()
		fmt.Fprintf(w, `{"Parameter":{"Value":%q}}`, value)

	case r.Header.Get("X-Amz-Target") == "secretsmanager.GetSecretValue":
		f.mu.Lock()
		f.secretsCalls++
		f.mu.Unlock()
		// A real value here would prove the fix does not merely hide the
		// resolved secret from the Lambda payload — it must never be read.
		fmt.Fprint(w, `{"SecretString":"leaked-secret-value"}`)

	default:
		http.Error(w, "unexpected internal request "+r.URL.Path+" "+r.Header.Get("X-Amz-Target"), http.StatusBadRequest)
	}
}

func (f *fakeCustomResourceReceiver) requestsSnapshot() []cfnCustomResourceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cfnCustomResourceRequest(nil), f.requests...)
}

// customResourceStack builds a one-resource stack + template for a
// Custom::TestResource whose properties are exactly extraProps plus a literal
// ServiceToken (itself never a dynamic reference — that would just be a
// second copy of the same test).
func customResourceStack(name string, extraProps map[string]any) (*Stack, *Template) {
	stack := &Stack{
		StackName:       name,
		StackID:         "arn:aws:cloudformation:us-east-1:000000000000:stack/" + name + "/1111",
		Region:          "us-east-1",
		DisableRollback: true,
	}
	props := map[string]any{
		"ServiceToken": "arn:aws:lambda:us-east-1:000000000000:function:custom-handler-fn",
	}
	for k, v := range extraProps {
		props[k] = v
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"MyCustom": {Type: "Custom::TestResource", Properties: props},
	}}
	return stack, tmpl
}

// Problem 1: a secure dynamic reference in a custom resource's properties
// must fail the resource, naming the restriction, rather than being resolved.
func TestCustomResourceCreate_rejectsSecureDynamicReference(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service string
		ref     string
	}{
		{name: "secretsmanager", service: "secretsmanager",
			ref: "{{resolve:secretsmanager:db-creds:SecretString:password}}"},
		{name: "ssm-secure", service: "ssm-secure",
			ref: "{{resolve:ssm-secure:/app/api-key}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &fakeCustomResourceReceiver{}
			p, _ := newTestProvisioner(t, receiver)
			stack, tmpl := customResourceStack("secure-ref-"+tc.name, map[string]any{
				"DbPassword": tc.ref,
			})

			ctx := p.regionCtx(stack.Region)
			p.provisionStackResourcesCtx(ctx, stack, tmpl)

			if len(stack.Resources) != 1 {
				t.Fatalf("stack.Resources = %+v, want one recorded resource", stack.Resources)
			}
			res := stack.Resources[0]
			if res.Status != ResourceCreateFailed {
				t.Fatalf("resource status = %q, want %q", res.Status, ResourceCreateFailed)
			}
			if !strings.Contains(res.StatusReason, tc.service) {
				t.Errorf("failure reason = %q, want it to name the restricted service %q", res.StatusReason, tc.service)
			}
			if !strings.Contains(res.StatusReason, "not supported") {
				t.Errorf("failure reason = %q, want it to name the restriction", res.StatusReason)
			}

			// The whole point: the secret was never read, and the Lambda was
			// never invoked with it.
			if receiver.secretsCalls != 0 {
				t.Errorf("secretsmanager/ssm-secure was read %d times, want 0 — a secure reference must be rejected, not resolved", receiver.secretsCalls)
			}
			if got := receiver.requestsSnapshot(); len(got) != 0 {
				t.Errorf("Lambda invoked %d times, want 0 — a rejected resource must not dispatch to its handler", len(got))
			}
		})
	}
}

// A plain (non-secure) SSM reference is unaffected by the restriction and
// still resolves into the Lambda's request, matching what a template author
// would expect of any other resource type.
func TestCustomResourceCreate_resolvesPlainSSMReference(t *testing.T) {
	receiver := &fakeCustomResourceReceiver{ssmValues: map[string]string{"/app/tier": "prod"}}
	p, _ := newTestProvisioner(t, receiver)
	stack, tmpl := customResourceStack("plain-ssm-ref", map[string]any{
		"Tier": "{{resolve:ssm:/app/tier}}",
	})

	ctx := p.regionCtx(stack.Region)
	p.provisionStackResourcesCtx(ctx, stack, tmpl)

	if stack.Status != StatusCreateComplete {
		t.Fatalf("stack status = %q reason %q, want CREATE_COMPLETE", stack.Status, stack.StatusReason)
	}
	reqs := receiver.requestsSnapshot()
	if len(reqs) != 1 {
		t.Fatalf("Lambda invocations = %d, want 1", len(reqs))
	}
	if got := reqs[0].ResourceProperties["Tier"]; got != "prod" {
		t.Errorf("ResourceProperties[Tier] = %v, want the resolved parameter value %q", got, "prod")
	}
}

// Problem 2, and the required acceptance test: an update of a custom resource
// whose properties contain a dynamic reference must send ResourceProperties
// and OldResourceProperties in the same form, so the reference itself never
// reads as a spurious change — while a property that genuinely changed still
// shows up as a real difference between the two.
func TestCustomResourceUpdate_oldAndNewPropertiesAgreeInForm(t *testing.T) {
	receiver := &fakeCustomResourceReceiver{ssmValues: map[string]string{"/app/tier": "prod"}}
	p, _ := newTestProvisioner(t, receiver)
	stack, tmpl := customResourceStack("update-form-agreement", map[string]any{
		"Tier":    "{{resolve:ssm:/app/tier}}",
		"Version": "v1",
	})

	ctx := p.regionCtx(stack.Region)
	p.provisionStackResourcesCtx(ctx, stack, tmpl)
	if stack.Status != StatusCreateComplete {
		t.Fatalf("create: stack status = %q reason %q, want CREATE_COMPLETE", stack.Status, stack.StatusReason)
	}

	// When: the stack is updated, changing an unrelated property but leaving
	// the dynamic reference exactly as the template already had it.
	previous := captureStackGeneration(stack)
	_, tmpl2 := customResourceStack(stack.StackName, map[string]any{
		"Tier":    "{{resolve:ssm:/app/tier}}",
		"Version": "v2",
	})
	p.updateStackResourcesCtx(ctx, stack, tmpl2, previous)

	if stack.Status != StatusUpdateComplete {
		t.Fatalf("update: stack status = %q reason %q, want UPDATE_COMPLETE", stack.Status, stack.StatusReason)
	}
	reqs := receiver.requestsSnapshot()
	if len(reqs) != 2 {
		t.Fatalf("Lambda invocations = %d, want 2 (Create then Update)", len(reqs))
	}
	update := reqs[1]
	if update.RequestType != "Update" {
		t.Fatalf("second invocation RequestType = %q, want %q", update.RequestType, "Update")
	}
	if update.OldResourceProperties == nil {
		t.Fatal("OldResourceProperties is absent — the custom resource protocol requires it on every Update request")
	}

	// The reference did not change in the template, so it must not appear to
	// change in the payload either.
	if update.ResourceProperties["Tier"] != update.OldResourceProperties["Tier"] {
		t.Errorf("Tier: ResourceProperties = %v, OldResourceProperties = %v — an unchanged dynamic reference must agree",
			update.ResourceProperties["Tier"], update.OldResourceProperties["Tier"])
	}
	if update.ResourceProperties["Tier"] != "prod" {
		t.Errorf("ResourceProperties[Tier] = %v, want the resolved parameter value %q", update.ResourceProperties["Tier"], "prod")
	}

	// A property that did change must still show up as a real difference.
	if update.ResourceProperties["Version"] != "v2" {
		t.Errorf("ResourceProperties[Version] = %v, want %q", update.ResourceProperties["Version"], "v2")
	}
	if update.OldResourceProperties["Version"] != "v1" {
		t.Errorf("OldResourceProperties[Version] = %v, want %q", update.OldResourceProperties["Version"], "v1")
	}
}

// A secure reference is rejected on Update exactly as it is on Create — the
// restriction is about the reference appearing in a custom resource's
// properties at all, not about which request type carries it.
func TestCustomResourceUpdate_rejectsSecureDynamicReference(t *testing.T) {
	receiver := &fakeCustomResourceReceiver{}
	p, _ := newTestProvisioner(t, receiver)
	stack, tmpl := customResourceStack("update-secure-ref", map[string]any{
		"Version": "v1",
	})

	ctx := p.regionCtx(stack.Region)
	p.provisionStackResourcesCtx(ctx, stack, tmpl)
	if stack.Status != StatusCreateComplete {
		t.Fatalf("create: stack status = %q reason %q, want CREATE_COMPLETE", stack.Status, stack.StatusReason)
	}

	previous := captureStackGeneration(stack)
	_, tmpl2 := customResourceStack(stack.StackName, map[string]any{
		"Version":    "v2",
		"DbPassword": "{{resolve:secretsmanager:db-creds:SecretString:password}}",
	})
	p.updateStackResourcesCtx(ctx, stack, tmpl2, previous)

	if stack.Status != StatusUpdateFailed {
		t.Fatalf("update: stack status = %q, want %q", stack.Status, StatusUpdateFailed)
	}
	res := stackResourceByLogicalID(t, stack.Resources, "MyCustom")
	if !strings.Contains(res.StatusReason, "secretsmanager") || !strings.Contains(res.StatusReason, "not supported") {
		t.Errorf("failure reason = %q, want it to name the restricted service and restriction", res.StatusReason)
	}
	if receiver.secretsCalls != 0 {
		t.Errorf("secretsmanager was read %d times, want 0", receiver.secretsCalls)
	}
	// Only the Create invocation should have reached the Lambda; the Update
	// must never dispatch.
	if got := receiver.requestsSnapshot(); len(got) != 1 {
		t.Errorf("Lambda invocations = %d, want 1 (Create only)", len(got))
	}
}
