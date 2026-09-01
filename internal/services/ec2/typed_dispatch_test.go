package ec2

// typed_dispatch_test.go — the typed registry is reachable, and answers what
// legacy answers.
//
// Each test below fails against the typed bodies as they stood before #754:
// the Describe* twins declared empty request structs, so a filter or an ID list
// was decoded into nothing and every one of them replied with the whole region.
// They are written against h.typedOp rather than the legacy handler on purpose —
// a test that goes through DispatchQuery alone would have passed throughout,
// which is exactly how the divergence survived.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// invokeTyped drives one operation through the typed registry the way
// DispatchQuery does, and returns the recorder.
func invokeTyped(t *testing.T, h *Handler, action string, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	operation, ok := h.typedOp[action]
	if !ok {
		t.Fatalf("no typed operation %s", action)
	}
	params.Set("Action", action)
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	w := httptest.NewRecorder()
	operation.Invoke(w, r, ec2ErrorCodec{Codec: codec.QueryXML})
	return w
}

// ── The registry is classified, exhaustively ────────────────────────────────

// Every registered typed operation is either routed or has a recorded reason it
// is not. The failure this guards is the one #754 describes: 69 operations
// registered, none dispatched to, and nothing failing to say so.
func TestTypedDispatch_everyOpIsClassified(t *testing.T) {
	h := defaultVPCHandler(t)
	for name := range h.typedOp {
		routed := ec2TypedOps[name]
		reason, deferred := ec2TypedDispatchRemainder[name]
		switch {
		case routed && deferred:
			t.Errorf("%s is both routed to the typed path and listed as remaining work", name)
		case !routed && !deferred:
			t.Errorf("%s is in neither ec2TypedOps nor ec2TypedDispatchRemainder: "+
				"route it once its typed body shares the legacy one, or record why it cannot be", name)
		case deferred && strings.TrimSpace(reason) == "":
			t.Errorf("%s is deferred with no reason given", name)
		}
	}
	for name := range ec2TypedOps {
		if _, ok := h.typedOp[name]; !ok {
			t.Errorf("ec2TypedOps names %s, which is not a registered typed operation", name)
		}
	}
	for name := range ec2TypedDispatchRemainder {
		if _, ok := h.typedOp[name]; !ok {
			t.Errorf("ec2TypedDispatchRemainder names %s, which is not a registered typed operation", name)
		}
	}
}

// ── Filters reach the typed path ────────────────────────────────────────────

func TestTypedDescribeSubnets_honoursTheVpcFilter(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	for _, sub := range []*Subnet{
		{SubnetID: "subnet-aaaa", VpcID: "vpc-aaaa", CidrBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a", State: "available"},
		{SubnetID: "subnet-bbbb", VpcID: "vpc-bbbb", CidrBlock: "10.1.1.0/24", AvailabilityZone: "us-east-1a", State: "available"},
	} {
		if aerr := h.store.putSubnet(ctx, sub); aerr != nil {
			t.Fatalf("putSubnet: %v", aerr.Message)
		}
	}

	w := invokeTyped(t, h, "DescribeSubnets", filterParams("vpc-id", "vpc-aaaa"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Subnets []struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnetSet>item"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
	}
	if len(resp.Subnets) != 1 || resp.Subnets[0].SubnetID != "subnet-aaaa" {
		t.Fatalf("vpc-id filter ignored on the typed path: got %v", resp.Subnets)
	}
}

func TestTypedDescribeInstances_honoursTheStateFilter(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	for _, inst := range []*Instance{
		{InstanceID: "i-running", ImageID: "ami-1", InstanceType: "t3.micro", State: InstanceState{Code: 16, Name: "running"}},
		{InstanceID: "i-stopped", ImageID: "ami-1", InstanceType: "t3.micro", State: InstanceState{Code: 80, Name: "stopped"}},
	} {
		if aerr := h.store.putInstance(ctx, inst); aerr != nil {
			t.Fatalf("putInstance: %v", aerr.Message)
		}
	}

	w := invokeTyped(t, h, "DescribeInstances", filterParams("instance-state-name", "running"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"reservationSet>item>instancesSet>item"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
	}
	if len(resp.Instances) != 1 || resp.Instances[0].InstanceID != "i-running" {
		t.Fatalf("instance-state-name filter ignored on the typed path: got %v", resp.Instances)
	}
}

func TestTypedDescribeImages_honoursTheIDSelection(t *testing.T) {
	h := defaultVPCHandler(t)
	params := url.Values{}
	params.Set("ImageId.1", "ami-12345678")

	w := invokeTyped(t, h, "DescribeImages", params)
	var resp struct {
		Images []struct {
			ImageID string `xml:"imageId"`
		} `xml:"imagesSet>item"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
	}
	if len(resp.Images) != 1 || resp.Images[0].ImageID != "ami-12345678" {
		t.Fatalf("ImageId.N ignored on the typed path: got %v", resp.Images)
	}
}

// The tag selector is the filter #1032 was reported against, and it is
// answered by parseTagFilters rather than by a filterSpec accessor — so it
// needs its own evidence that the typed path reads it.
func TestTypedDescribeVpcs_honoursATagSelector(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	for _, vpc := range []*VPC{
		{VpcID: "vpc-wanted", CidrBlock: "10.0.0.0/16", State: "available"},
		{VpcID: "vpc-other", CidrBlock: "10.1.0.0/16", State: "available"},
	} {
		if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
			t.Fatalf("putVPC: %v", aerr.Message)
		}
	}
	if aerr := h.putResourceTags(ctx, "vpc-wanted", []Tag{{Key: "Name", Value: "overcast-private"}}); aerr != nil {
		t.Fatalf("putResourceTags: %v", aerr.Message)
	}

	w := invokeTyped(t, h, "DescribeVpcs", filterParams("tag:Name", "overcast-*"))
	var resp struct {
		Vpcs []struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpcSet>item"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
	}
	if len(resp.Vpcs) != 1 || resp.Vpcs[0].VpcID != "vpc-wanted" {
		t.Fatalf("tag:Name selector ignored on the typed path: got %v", resp.Vpcs)
	}
}

// #1032's rule has to hold on both paths or a caller gets a different answer
// depending on which one happened to be routed — the divergence the whole
// migration exists to avoid.
func TestTypedDescribes_refuseAnUnrecognisedFilter(t *testing.T) {
	h := defaultVPCHandler(t)
	for name := range ec2TypedOps {
		if _, declared := supportedFiltersForTest(name); !declared {
			continue
		}
		t.Run(name, func(t *testing.T) {
			w := invokeTyped(t, h, name, filterParams("not-a-filter", "anything"))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			var resp struct {
				Code    string `xml:"Errors>Error>Code"`
				Message string `xml:"Errors>Error>Message"`
			}
			if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
			}
			if resp.Code != "InvalidParameterValue" {
				t.Fatalf("code = %q, want InvalidParameterValue", resp.Code)
			}
			if !strings.HasPrefix(resp.Message, "The filter 'not-a-filter' is invalid.") {
				t.Fatalf("message = %q", resp.Message)
			}
		})
	}
}

// supportedFiltersForTest reports whether an operation declares a filterSpec.
// supportedFilters is populated by declareFilters at init, so this is a plain
// lookup — it exists only to keep the intent readable at the call site.
func supportedFiltersForTest(op string) ([]string, bool) {
	names, ok := supportedFilters[op]
	return names, ok
}

// ── The error envelope ──────────────────────────────────────────────────────

// EC2's error shape is a plural <Errors> wrapper the generic Query codec does
// not write. ec2ErrorCodec is what keeps a typed operation's error in EC2's
// shape, and it was deleted when the typed branch was switched off — so this
// asserts the recovered wrapper is actually wired, not just declared.
func TestTypedDispatch_errorsUseEC2sEnvelope(t *testing.T) {
	h := defaultVPCHandler(t)
	params := url.Values{}
	params.Set("VpcId", "vpc-does-not-exist")
	params.Set("Attribute", "enableDnsSupport")

	w := invokeTyped(t, h, "DescribeVpcAttribute", params)
	if w.Code == http.StatusOK {
		t.Fatalf("expected an error for an unknown VPC, got 200: %s", w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<Errors>") || !strings.Contains(body, "<Error>") {
		t.Fatalf("typed error is not in EC2's <Errors><Error> envelope: %s", body)
	}
	if strings.Contains(body, "<Type>") {
		t.Fatalf("typed error used the generic Query envelope: %s", body)
	}
}

// DescribeVpcAttribute answered `true` for both DNS attributes on the typed
// path whatever the VPC held — the divergence #1144 worked around by staying on
// legacy. It has to read the store now that the typed path answers.
func TestTypedDescribeVpcAttribute_readsTheStoredValue(t *testing.T) {
	h := defaultVPCHandler(t)
	ctx := context.Background()
	vpc := &VPC{VpcID: "vpc-dns", CidrBlock: "10.0.0.0/16", State: "available", EnableDnsSupport: true}
	if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
		t.Fatalf("putVPC: %v", aerr.Message)
	}

	params := url.Values{}
	params.Set("VpcId", "vpc-dns")
	params.Set("Attribute", "enableDnsHostnames")
	w := invokeTyped(t, h, "DescribeVpcAttribute", params)

	var resp struct {
		EnableDnsHostnames *struct {
			Value bool `xml:"value"`
		} `xml:"enableDnsHostnames"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, w.Body.String())
	}
	if resp.EnableDnsHostnames == nil {
		t.Fatalf("no enableDnsHostnames in %s", w.Body.String())
	}
	if resp.EnableDnsHostnames.Value {
		t.Fatalf("enableDnsHostnames = true for a VPC that has it off; the typed body is not reading the store")
	}
}

// ── The allow-list decides the path ─────────────────────────────────────────

// The two dispatch paths answer the same bytes by construction, which is the
// point but also makes "which one ran" invisible from the response. The
// protocol-drift check is the one behaviour only the typed branch has: under
// ProtocolStrict a codec EC2 does not declare is refused there, and passes
// straight through on the legacy path. That asymmetry is what this reads.
func TestDispatchQuery_consultsTheAllowList(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", ProtocolStrict: true},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	dispatch := func(action string) *httptest.ResponseRecorder {
		params := url.Values{}
		params.Set("Action", action)
		params.Set("Version", "2016-11-15")
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// JSON 1.0 is a protocol EC2 does not declare, so the typed branch's
		// drift check refuses it and the legacy branch never looks.
		r = r.WithContext(codec.WithDispatch(r.Context(), codec.JSON10, action))
		w := httptest.NewRecorder()
		svc.DispatchQuery(w, r)
		return w
	}

	routed := dispatch("DescribeRegions")
	if routed.Code != http.StatusUnsupportedMediaType {
		t.Errorf("DescribeRegions is in ec2TypedOps but did not reach the typed branch: status %d", routed.Code)
	}
	if !strings.Contains(routed.Body.String(), "<Errors>") {
		t.Errorf("the typed branch's protocol refusal is not in EC2's envelope: %s", routed.Body.String())
	}

	legacy := dispatch("DescribeVpnGatewayRoutePropagation")
	if legacy.Code != http.StatusNotImplemented {
		t.Errorf("an unregistered action should reach the legacy fallback: status %d", legacy.Code)
	}

	// Wave 2 (#754) closed ec2TypedDispatchRemainder, so no operation is
	// permanently deferred to pin this against. Prove the mechanism instead:
	// pull a routed operation off the allow-list for the duration of this
	// test and confirm DispatchQuery falls through to legacy for it — the
	// legacy branch never consults ProtocolStrict, so it answers 200 rather
	// than refusing JSON 1.0 the way the typed branch does above.
	const deferredForTest = "AllocateAddress"
	delete(ec2TypedOps, deferredForTest)
	defer func() { ec2TypedOps[deferredForTest] = true }()

	deferred := dispatch(deferredForTest)
	if deferred.Code != http.StatusOK {
		t.Errorf("an operation pulled off the allow-list must fall through to legacy regardless of protocol: status %d, body %s",
			deferred.Code, deferred.Body.String())
	}
}

// ── The decode seam ─────────────────────────────────────────────────────────

// eachDecodedFilter has to agree with eachFilter on where a filter list ends.
// The codec renders a gap in Filter.N as a zero-valued element, and reading one
// as a filter named "" would refuse the request as unrecognised.
func TestEachDecodedFilter_stopsAtAnUnnamedFilter(t *testing.T) {
	filters := []ec2Filter{
		{Name: "vpc-id", Values: []string{"vpc-1"}},
		{},
		{Name: "state", Values: []string{"available"}},
	}
	var got []string
	for name := range eachDecodedFilter(filters) {
		got = append(got, name)
	}
	if fmt.Sprint(got) != "[vpc-id]" {
		t.Fatalf("names = %v, want [vpc-id]", got)
	}
}

func TestSelectedIDs_stopsAtAnEmptyEntry(t *testing.T) {
	sel := selectedIDs([]string{"subnet-1", "", "subnet-3"})
	if len(sel) != 1 || !sel["subnet-1"] {
		t.Fatalf("selection = %v, want just subnet-1", sel)
	}
	if sel.has("subnet-3") {
		t.Fatalf("subnet-3 was selected past the gap")
	}
}

// ── What the flip costs ─────────────────────────────────────────────────────

// Routing a describe to the typed registry replaces lazy r.FormValue reads with
// one reflective decode of the whole request. These two benchmarks price that,
// and are sized to show it: a request with an ID list and two filters is what
// the decoder has to walk, so a request carrying only Action would price
// nothing. Run with -benchmem.
func benchmarkDescribeSubnets(b *testing.B, useTyped bool) {
	h := newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		serviceutilLogger(),
		clock.New(),
	)
	ctx := context.Background()
	for i := range 200 {
		id := fmt.Sprintf("subnet-bench%03d", i)
		if aerr := h.store.putSubnet(ctx, &Subnet{
			SubnetID: id, VpcID: "vpc-bench", CidrBlock: "10.0.0.0/24",
			AvailabilityZone: "us-east-1a", State: "available",
		}); aerr != nil {
			b.Fatalf("putSubnet: %s", aerr.Message)
		}
	}

	params := url.Values{}
	params.Set("Action", "DescribeSubnets")
	params.Set("Version", "2016-11-15")
	for i := range 20 {
		params.Set(fmt.Sprintf("SubnetId.%d", i+1), fmt.Sprintf("subnet-bench%03d", i))
	}
	params.Set("Filter.1.Name", "vpc-id")
	params.Set("Filter.1.Value.1", "vpc-bench")
	params.Set("Filter.2.Name", "state")
	params.Set("Filter.2.Value.1", "available")
	params.Set("Filter.2.Value.2", "pending")
	body := params.Encode()

	operation := h.typedOp["DescribeSubnets"]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			b.Fatalf("ParseForm: %v", err)
		}
		w := httptest.NewRecorder()
		if useTyped {
			operation.Invoke(w, r, ec2ErrorCodec{Codec: codec.QueryXML})
		} else {
			h.DescribeSubnets(w, r)
		}
	}
}

func BenchmarkDescribeSubnets_legacyDispatch(b *testing.B) { benchmarkDescribeSubnets(b, false) }
func BenchmarkDescribeSubnets_typedDispatch(b *testing.B)  { benchmarkDescribeSubnets(b, true) }

func serviceutilLogger() *serviceutil.ServiceLogger {
	return serviceutil.NewServiceLogger(zap.NewNop(), "ec2")
}
