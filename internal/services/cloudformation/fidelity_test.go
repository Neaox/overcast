package cloudformation

// fidelity_test.go — pins classifyResourceFidelity's three categories and the
// provisioner wiring that turns them into a resource's status reason. See
// https://github.com/overcast-sh/overcast/issues/760 and fidelity.go.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── classifyResourceFidelity: unit-level ────────────────────────────────────

func TestClassifyResourceFidelity_unrecognizedType(t *testing.T) {
	got := classifyResourceFidelity("AWS::Nonexistent::Thing", nil, false)
	if !strings.HasPrefix(got, "Overcast:") {
		t.Fatalf("classifyResourceFidelity = %q, want it to start with %q", got, "Overcast:")
	}
	if !strings.Contains(got, "AWS::Nonexistent::Thing") {
		t.Errorf("classifyResourceFidelity = %q, want it to name the unrecognised type", got)
	}
}

func TestClassifyResourceFidelity_stubHandler(t *testing.T) {
	got := classifyResourceFidelity("AWS::CDK::Metadata", &stubResourceHandler{}, true)
	if !strings.HasPrefix(got, "Overcast:") {
		t.Fatalf("classifyResourceFidelity = %q, want it to start with %q", got, "Overcast:")
	}
	if !strings.Contains(got, "no-op") {
		t.Errorf("classifyResourceFidelity = %q, want it to say this is a no-op", got)
	}
}

// fidelityFakeHandler is a resourceHandler that does nothing — it exists so
// tests can register it under a resource type whose service segment resolves
// to a known tier, without needing a real backing service to dispatch to.
type fidelityFakeHandler struct{}

func (h *fidelityFakeHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	return "fake-physical-id", nil, nil
}

func (h *fidelityFakeHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

func TestClassifyResourceFidelity_realHandlerOnInertTierService(t *testing.T) {
	// AWS::Route53::* -> "route53", which resourceServiceTiers carries as inert.
	got := classifyResourceFidelity("AWS::Route53::TestThing", &fidelityFakeHandler{}, true)
	if !strings.HasPrefix(got, "Overcast: created, but") {
		t.Fatalf("classifyResourceFidelity = %q, want it to start with %q", got, "Overcast: created, but")
	}
	if !strings.Contains(got, "route53") || !strings.Contains(got, "inert tier") {
		t.Errorf("classifyResourceFidelity = %q, want it to name route53 and inert tier", got)
	}
}

func TestClassifyResourceFidelity_realHandlerOnStubTierService(t *testing.T) {
	// AWS::WAFv2::* aliases to "waf", which resourceServiceTiers carries as stub.
	got := classifyResourceFidelity("AWS::WAFv2::TestThing", &fidelityFakeHandler{}, true)
	if !strings.Contains(got, "waf") || !strings.Contains(got, "stub tier") {
		t.Errorf("classifyResourceFidelity = %q, want it to name waf and stub tier", got)
	}
}

func TestClassifyResourceFidelity_fullyBackedResourceIsSilent(t *testing.T) {
	// AWS::SQS::* -> "sqs", which is TierFull and has no entry in
	// resourceServiceTiers — a fully-backed resource gets no fidelity notice.
	got := classifyResourceFidelity("AWS::SQS::Queue", &sqsQueueHandler{}, true)
	if got != "" {
		t.Errorf("classifyResourceFidelity = %q, want empty for a fully-backed resource", got)
	}
}

func TestClassifyResourceFidelity_customResourceIsSilent(t *testing.T) {
	// Custom::* does not have the AWS::Service::Type shape, so it never
	// resolves to a service tier — its fidelity depends on the caller's own
	// Lambda, not on anything Overcast's tier registry can say.
	got := classifyResourceFidelity("Custom::MyResource", &fidelityFakeHandler{}, true)
	if got != "" {
		t.Errorf("classifyResourceFidelity = %q, want empty for a Custom:: resource", got)
	}
}

// ── cfnResourceService: naming mismatches ───────────────────────────────────

func TestCfnResourceService_aliasesAndLowercasing(t *testing.T) {
	cases := map[string]string{
		"AWS::Events::Rule":                         "eventbridge",
		"AWS::WAFv2::WebACL":                        "waf",
		"AWS::CertificateManager::Certificate":      "acm",
		"AWS::KinesisFirehose::DeliveryStream":      "firehose",
		"AWS::OpenSearchService::Domain":            "opensearch",
		"AWS::ElasticLoadBalancingV2::LoadBalancer": "elbv2",
		"AWS::Route53::HostedZone":                  "route53",
		"AWS::IAM::Role":                            "iam",
	}
	for resType, want := range cases {
		got, ok := cfnResourceService(resType)
		if !ok {
			t.Errorf("cfnResourceService(%q) reported not-ok, want %q", resType, want)
			continue
		}
		if got != want {
			t.Errorf("cfnResourceService(%q) = %q, want %q", resType, got, want)
		}
	}
}

func TestCfnResourceService_customResourceHasNoService(t *testing.T) {
	if _, ok := cfnResourceService("Custom::Thing"); ok {
		t.Error("cfnResourceService(\"Custom::Thing\") reported ok, want false — Custom:: has no AWS::Service::Type shape")
	}
}

// ── addFidelityFallback: a specific reason is never drowned out ────────────

// fidelityHeaderHandler reports a resource-specific limitation exactly the
// way the CloudWatch alarm handler does — through the response header
// protocol.MarkLimitation writes and limitation.go's noteLimitations collects
// — so the test can pin that classifyResourceFidelity's generic sentence
// never overrides a handler's own, more specific one.
type fidelityHeaderHandler struct{ message string }

func (h *fidelityHeaderHandler) Create(ctx context.Context, _ http.Handler, _ *config.Config, _ map[string]any, _ *resolveContext) (string, map[string]string, error) {
	rec := httptest.NewRecorder()
	protocol.MarkLimitation(rec, h.message)
	noteLimitations(ctx, rec)
	return "fake-physical-id", nil, nil
}

func (h *fidelityHeaderHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

func TestProvisionResource_specificReasonNotOverriddenByGenericFidelityMessage(t *testing.T) {
	const resType = "AWS::IAM::TestSpecific"
	const specific = "this alarm's condition cannot be evaluated"
	resourceHandlers[resType] = &fidelityHeaderHandler{message: specific}
	t.Cleanup(func() { delete(resourceHandlers, resType) })

	p := newProvisionerTestFixture(t)
	rCtx := &resolveContext{Region: "us-east-1", StackName: "s", Resources: map[string]string{}}
	physID, err := p.provisionResource(context.Background(), "R", TemplateResource{Type: resType}, map[string]any{}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if physID != "fake-physical-id" {
		t.Fatalf("physID = %q, want %q", physID, "fake-physical-id")
	}
	if rCtx.EmulationLimitation != specific {
		t.Errorf("EmulationLimitation = %q, want the handler's own reason %q with nothing appended", rCtx.EmulationLimitation, specific)
	}
}

// ── provisionResource: the three categories reach StatusReason ─────────────

func TestProvisionResource_unrecognizedTypeSetsEmulationLimitation(t *testing.T) {
	p := newProvisionerTestFixture(t)
	rCtx := &resolveContext{Region: "us-east-1", StackName: "s", Resources: map[string]string{}}
	physID, err := p.provisionResource(context.Background(), "R", TemplateResource{Type: "AWS::Nonexistent::Thing"},
		map[string]any{}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if !strings.HasSuffix(physID, "-stub") {
		t.Errorf("physID = %q, want it to end in -stub", physID)
	}
	if !strings.Contains(rCtx.EmulationLimitation, "not a recognised resource type") {
		t.Errorf("EmulationLimitation = %q, want it to explain the type is unrecognised", rCtx.EmulationLimitation)
	}
}

func TestProvisionResource_stubHandlerSetsEmulationLimitation(t *testing.T) {
	p := newProvisionerTestFixture(t)
	rCtx := &resolveContext{Region: "us-east-1", StackName: "s", Resources: map[string]string{}}
	_, err := p.provisionResource(context.Background(), "R", TemplateResource{Type: "AWS::CDK::Metadata"},
		map[string]any{}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if !strings.Contains(rCtx.EmulationLimitation, "no-op") {
		t.Errorf("EmulationLimitation = %q, want it to say this is a no-op", rCtx.EmulationLimitation)
	}
}

func TestProvisionResource_inertTierRealHandlerSetsEmulationLimitation(t *testing.T) {
	const resType = "AWS::Route53::TestFixture"
	resourceHandlers[resType] = &fidelityFakeHandler{}
	t.Cleanup(func() { delete(resourceHandlers, resType) })

	p := newProvisionerTestFixture(t)
	rCtx := &resolveContext{Region: "us-east-1", StackName: "s", Resources: map[string]string{}}
	_, err := p.provisionResource(context.Background(), "R", TemplateResource{Type: resType},
		map[string]any{}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if !strings.Contains(rCtx.EmulationLimitation, "route53") || !strings.Contains(rCtx.EmulationLimitation, "inert tier") {
		t.Errorf("EmulationLimitation = %q, want it to name route53 and inert tier", rCtx.EmulationLimitation)
	}
}

// The negative case the issue calls for explicitly: a fully-backed resource
// gets no new signal at all.
func TestProvisionResource_fullyBackedResourceHasNoEmulationLimitation(t *testing.T) {
	const resType = "AWS::S3::TestFixture"
	resourceHandlers[resType] = &fidelityFakeHandler{}
	t.Cleanup(func() { delete(resourceHandlers, resType) })

	p := newProvisionerTestFixture(t)
	rCtx := &resolveContext{Region: "us-east-1", StackName: "s", Resources: map[string]string{}}
	_, err := p.provisionResource(context.Background(), "R", TemplateResource{Type: resType},
		map[string]any{}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if rCtx.EmulationLimitation != "" {
		t.Errorf("EmulationLimitation = %q, want empty for a fully-backed resource", rCtx.EmulationLimitation)
	}
}

// ── the full create path: StatusReason and the CREATE_COMPLETE event ───────

func TestProvisionStackResources_recordsFidelityReasonOnStackResourceAndEvent(t *testing.T) {
	// Given: a stack with a stub resource, an inert-tier resource, and a
	// fully-backed resource. All three use fake handlers that create nothing
	// over the network — newProvisionerTestFixture wires the router to
	// http.NotFoundHandler, so a handler that actually dispatched (the real
	// SQS or Route53 handlers do) would fail here for an unrelated reason.
	// What is under test is the classification, not the dispatch.
	const inertType = "AWS::Route53::StackFixture"
	const realType = "AWS::S3::StackFixture"
	resourceHandlers[inertType] = &fidelityFakeHandler{}
	resourceHandlers[realType] = &fidelityFakeHandler{}
	t.Cleanup(func() {
		delete(resourceHandlers, inertType)
		delete(resourceHandlers, realType)
	})

	p := newProvisionerTestFixture(t)
	stack := &Stack{
		StackName: "fidelity-stack",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/fidelity-stack/1111",
		Region:    "us-east-1",
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"StubThing":  {Type: "AWS::CDK::Metadata"},
		"InertThing": {Type: inertType},
		"RealThing":  {Type: realType},
	}}

	// When: the stack is provisioned.
	p.provisionStackResources(stack, tmpl)

	// Then: the stack reached CREATE_COMPLETE...
	if stack.Status != StatusCreateComplete {
		t.Fatalf("stack status = %q, want %q (reason: %s)", stack.Status, StatusCreateComplete, stack.StatusReason)
	}

	// ...the stub resource says it is a no-op...
	stub := stackResourceByLogicalID(t, stack.Resources, "StubThing")
	if !strings.Contains(stub.StatusReason, "no-op") {
		t.Errorf("StubThing.StatusReason = %q, want it to say this is a no-op", stub.StatusReason)
	}

	// ...the inert-tier resource says why...
	inert := stackResourceByLogicalID(t, stack.Resources, "InertThing")
	if !strings.Contains(inert.StatusReason, "route53") || !strings.Contains(inert.StatusReason, "inert tier") {
		t.Errorf("InertThing.StatusReason = %q, want it to name route53 and inert tier", inert.StatusReason)
	}

	// ...and the fully-backed resource says nothing new.
	real := stackResourceByLogicalID(t, stack.Resources, "RealThing")
	if real.StatusReason != "" {
		t.Errorf("RealThing.StatusReason = %q, want empty for a fully-backed resource", real.StatusReason)
	}

	// And: the reason rides the CREATE_COMPLETE event too, not only the
	// resource record — this is the deploy-path signal the issue asks for.
	stackEvents, err := p.store.getStackEvents(context.Background(), stack.StackID)
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	var sawStubEvent, sawInertEvent bool
	for _, e := range stackEvents {
		if e.LogicalResourceID == "StubThing" && e.ResourceStatus == ResourceCreateComplete {
			sawStubEvent = true
			if !strings.Contains(e.ResourceStatusReason, "no-op") {
				t.Errorf("StubThing CREATE_COMPLETE event reason = %q, want it to say this is a no-op", e.ResourceStatusReason)
			}
		}
		if e.LogicalResourceID == "InertThing" && e.ResourceStatus == ResourceCreateComplete {
			sawInertEvent = true
			if !strings.Contains(e.ResourceStatusReason, "inert tier") {
				t.Errorf("InertThing CREATE_COMPLETE event reason = %q, want it to name inert tier", e.ResourceStatusReason)
			}
		}
	}
	if !sawStubEvent {
		t.Error("no CREATE_COMPLETE event found for StubThing")
	}
	if !sawInertEvent {
		t.Error("no CREATE_COMPLETE event found for InertThing")
	}
}
