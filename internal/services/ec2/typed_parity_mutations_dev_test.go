//go:build dev

package ec2

// typed_parity_mutations_dev_test.go — the differential test for wave 2's
// mutations, typed_parity_dev_test.go's answerBoth cannot cover them.
//
// answerBoth (typed_parity_dev_test.go) puts one request to both dispatch
// paths against *one* handler, in sequence, because a Describe* is read-only:
// running it twice changes nothing between the two runs. A mutation is the
// opposite case by definition — running DeleteVpc through legacy and then
// again through typed against the same store deletes the same VPC twice, and
// the second call fails not because the two paths disagree but because there
// is nothing left to delete. So every case here gets *two* handlers, seeded
// identically, one driven through legacy and one through typed, and the two
// results compared — same shape as answerBoth's guarantee, adapted to the
// thing that makes a mutation different from a read.
//
// A second difference: creates mint a fresh, random resource ID
// (`shortID()`), so the two independently-seeded handlers can never agree on
// *that* byte, only on everything the ID doesn't carry. stableMutation masks
// every minted-ID shape a case in this file's table can produce, on top of
// answerBoth's own r-/dopt- masking.
//
// A third: a mutation can agree on its own response and disagree on what it
// wrote — Authorize returns `Return: true` on both paths whether or not the
// rule was appended. Where the bare response cannot show that, the case's
// `after` hook drives a follow-up Describe* (through legacy — describe.go's
// shared body is what already proves the two paths read a store identically,
// so this file's job is checking what a mutation *wrote*, not relitigating
// reads) and its result is folded into the compared string.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

// mutationCase is one mutation request, checked against two independently
// seeded handlers — one driven through legacy, one through typed.
type mutationCase struct {
	// name distinguishes the case in test output.
	name string
	// seed populates a freshly constructed handler with whatever state the
	// case's params reference by a fixed ID (never a minted one — seed writes
	// the store directly, bypassing the ID-minting the call under test does).
	// Called once per handler, so both start identical.
	seed func(t *testing.T, h *Handler)
	// params are the request's parameters, less Action and Version.
	params url.Values
	// after, if set, is handed the mutation's own raw (unmasked) response
	// body and returns a further legacy-driven Describe* response to fold
	// into the compared string — for a case whose bare response does not
	// itself show what the mutation wrote. May extract a minted ID out of the
	// raw body (e.g. CreateVpc's vpcId) to query it back.
	after func(t *testing.T, h *Handler, rawResponse string) string
}

// mintedMutationIDs extends answerBoth's r-/dopt- masking (typed_parity_dev_test.go)
// with every ID shape a case below can mint fresh per call. Fixture IDs this
// file's seed funcs write are never in this shape (they use a "-mut-" or
// "-mut2-" infix, never eight bare hex characters), so this cannot mask
// anything a case seeded on purpose.
var mintedMutationIDs = regexp.MustCompile(
	`\b(vpc-cidr-assoc|eipassoc|eipalloc|rtbassoc|vpce|vgw|rtb|igw|pcx|nat|dopt|key|eni|sg|vpc|subnet|i|r)-[0-9a-f]{8}\b`)

// mintedMacAddress masks CreateNetworkInterface's fabricated MAC — six bytes
// of crypto/rand, never equal between two independently-seeded handlers.
var mintedMacAddress = regexp.MustCompile(`\b02(?::[0-9a-f]{2}){5}\b`)

// deterministicKeyMaterial replaces Handler.keyMaterialFor on both of a
// mutation case's independently-seeded handlers (newMutationHandler below),
// so CreateKeyPair mints byte-identical fingerprint and key material on the
// legacy and typed dispatch paths instead of two independent crypto/rand
// draws. That used to be reconciled by mintedKeyFingerprint/mintedKeyMaterial
// regexes masking the random content out of the compared string — regexes
// that, on the CI runner, sometimes failed to redact one side fully (#1302),
// comparing genuinely random bytes as if they were meant to agree. Minting
// identical material on both sides removes the need to mask it at all.
func deterministicKeyMaterial() (fingerprint, material string) {
	return "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99",
		"-----BEGIN RSA PRIVATE KEY-----\n" +
			"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" +
			"\n-----END RSA PRIVATE KEY-----"
}

// mintedSyntheticIP masks AllocateAddress's public IP and the private-IP
// fallback allocatePrivateIPForSubnet mints for a subnet the VPC-network
// strategy could not allocate through (no Docker in these tests, so every
// call takes this branch) — both draw from the same process-wide
// syntheticIPCounter (handler.go), so the two independently-constructed
// handlers in one case never mint the same value even though nothing else
// about the request differed. 10.0.0.0/16 is deliberately not touched by a
// narrower pattern: every case that seeds a VPC uses that exact CIDR, and
// masking only the fourth octet after a literal "10.0.0." leaves a subnet's
// own distinct CIDR (10.0.1.0/24 and up) untouched.
var mintedSyntheticIP = regexp.MustCompile(`\b(?:203\.0\.113|10\.0\.0)\.\d{1,3}\b`)

// stableMutation masks every value two independently-seeded handlers cannot
// be expected to agree on, leaving what a real divergence would show up in.
func stableMutation(body string) string {
	body = mintedMutationIDs.ReplaceAllString(body, "$1-XXXXXXXX")
	body = mintedMacAddress.ReplaceAllString(body, "02:XX:XX:XX:XX:XX")
	body = mintedSyntheticIP.ReplaceAllString(body, "X.X.X.X")
	return body
}

// xmlField extracts one flat <tag>value</tag> element's content — enough to
// pull a minted ID (e.g. CreateVpc's vpcId) out of a raw response for an
// `after` hook to query back, without pulling in a full XML decode for a
// single field.
func xmlField(body, tag string) string {
	re := regexp.MustCompile(`<` + tag + `>([^<]*)</` + tag + `>`)
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// newMutationHandler builds a handler for one side of a mutation parity case.
// The clock is shared across both sides (a *mock, never advanced during a
// case) so a stored timestamp — CreateVpc's CreateTime, CreateNatGateway's —
// is the same wall-clock value on both, rather than two calls to the real
// clock microseconds apart.
func newMutationHandler(t *testing.T, mock clock.Clock) *Handler {
	t.Helper()
	h := newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Network: "overcast"},
		state.NewMemoryStore(),
		serviceutil.NewServiceLogger(zap.NewNop(), "ec2"),
		mock,
	)
	// Both of a mutation case's handlers get the same deterministic key
	// material — see deterministicKeyMaterial's comment — so CreateKeyPair's
	// legacy and typed responses always agree byte-for-byte, the same way
	// sharing mock as their clock keeps a stored timestamp agreeing.
	h.keyMaterialFor = deterministicKeyMaterial
	return h
}

// dispatchLegacy drives one request through h's legacy handler map — used
// both as the mutation-under-test's legacy side and as every `after` hook's
// Describe* probe, since describe.go's shared body is what already proves a
// describe reads the store identically on both paths.
func dispatchLegacy(t *testing.T, h *Handler, action string, params url.Values) string {
	t.Helper()
	p := url.Values{}
	for k, vs := range params {
		p[k] = append([]string(nil), vs...)
	}
	p.Set("Action", action)
	p.Set("Version", "2016-11-15")
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(p.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(protocol.ContextWithRequestID(r.Context(), "parity-request-id"))
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	w := httptest.NewRecorder()
	handler, ok := h.ops[action]
	if !ok {
		t.Fatalf("no legacy handler %s", action)
	}
	handler(w, r)
	return w.Body.String()
}

// answerBothMutation runs one mutation case against two independently seeded
// handlers — legacy on one, typed on the other — and returns each side's
// full comparable string (status, response, and any `after` probe), masked.
func answerBothMutation(t *testing.T, action string, c mutationCase) (legacy, typed string) {
	t.Helper()
	mock := clock.NewMock()
	hLegacy := newMutationHandler(t, mock)
	hTyped := newMutationHandler(t, mock)
	if c.seed != nil {
		c.seed(t, hLegacy)
		c.seed(t, hTyped)
	}

	run := func(h *Handler, useTyped bool) string {
		p := url.Values{}
		for k, vs := range c.params {
			p[k] = append([]string(nil), vs...)
		}
		p.Set("Action", action)
		p.Set("Version", "2016-11-15")

		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(p.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(protocol.ContextWithRequestID(r.Context(), "parity-request-id"))
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		w := httptest.NewRecorder()

		if useTyped {
			operation, ok := h.typedOp[action]
			if !ok {
				t.Fatalf("no typed operation %s", action)
			}
			operation.Invoke(w, r, ec2ErrorCodec{Codec: codec.QueryXML})
		} else {
			handler, ok := h.ops[action]
			if !ok {
				t.Fatalf("no legacy handler %s", action)
			}
			handler(w, r)
		}

		raw := w.Body.String()
		combined := strconv.Itoa(w.Code) + "\n" + raw
		if c.after != nil {
			combined += "\n--after--\n" + c.after(t, h, raw)
		}
		return stableMutation(combined)
	}

	return run(hLegacy, false), run(hTyped, true)
}

// ── Seed helpers ─────────────────────────────────────────────────────────

func seedVPC(t *testing.T, h *Handler, id string) *VPC {
	t.Helper()
	vpc := &VPC{VpcID: id, CidrBlock: "10.0.0.0/16", State: "available", EnableDnsSupport: true}
	if aerr := h.store.putVPC(context.Background(), vpc); aerr != nil {
		t.Fatalf("putVPC: %s", aerr.Message)
	}
	return vpc
}

func seedSubnet(t *testing.T, h *Handler, id, vpcID string) *Subnet {
	t.Helper()
	return seedSubnetInZone(t, h, id, vpcID, "us-east-1a")
}

// seedSubnetInZone is seedSubnet with the zone named, for a case whose whole
// point is a subnet that is *not* in the region's first zone.
func seedSubnetInZone(t *testing.T, h *Handler, id, vpcID, zone string) *Subnet {
	t.Helper()
	sub := &Subnet{SubnetID: id, VpcID: vpcID, CidrBlock: "10.0.1.0/24", AvailabilityZone: zone, State: "available"}
	if aerr := h.store.putSubnet(context.Background(), sub); aerr != nil {
		t.Fatalf("putSubnet: %s", aerr.Message)
	}
	return sub
}

func seedSG(t *testing.T, h *Handler, id, vpcID string) *SecurityGroup {
	t.Helper()
	sg := &SecurityGroup{GroupID: id, GroupName: "mut-" + id, Description: "mutation parity", VpcID: vpcID}
	if aerr := h.store.putSecurityGroup(context.Background(), sg); aerr != nil {
		t.Fatalf("putSecurityGroup: %s", aerr.Message)
	}
	return sg
}

func seedInstance(t *testing.T, h *Handler, id string, code int, name string) *Instance {
	t.Helper()
	inst := &Instance{
		InstanceID: id, ImageID: "ami-12345678", InstanceType: "t3.micro",
		State: InstanceState{Code: code, Name: name}, LaunchTime: "2026-01-01T00:00:00Z",
	}
	if aerr := h.store.putInstance(context.Background(), inst); aerr != nil {
		t.Fatalf("putInstance: %s", aerr.Message)
	}
	return inst
}

// seedLaunchTemplate writes a one-version launch template straight to the
// store, so a parity case can reference it by a fixed ID rather than by one
// CreateLaunchTemplate would have minted.
func seedLaunchTemplate(t *testing.T, h *Handler, id string) *LaunchTemplate {
	t.Helper()
	ctx := context.Background()
	lt := &LaunchTemplate{
		LaunchTemplateID:     id,
		LaunchTemplateName:   "mut-" + id,
		CreateTime:           "2026-01-01T00:00:00Z",
		CreatedBy:            "arn:aws:iam::123456789012:root",
		DefaultVersionNumber: 1,
		LatestVersionNumber:  1,
	}
	version := &LaunchTemplateVersion{
		LaunchTemplateID:   lt.LaunchTemplateID,
		LaunchTemplateName: lt.LaunchTemplateName,
		VersionNumber:      1,
		CreateTime:         lt.CreateTime,
		CreatedBy:          lt.CreatedBy,
		Data: LaunchTemplateData{
			ImageID:      "ami-12345678",
			InstanceType: "t3.micro",
			TagSpecifications: []LaunchTemplateTagSpecification{{
				ResourceType: "instance",
				Tags:         []Tag{{Key: "Source", Value: "template"}},
			}},
		},
	}
	if aerr := h.store.putLaunchTemplateVersion(ctx, version); aerr != nil {
		t.Fatalf("putLaunchTemplateVersion: %s", aerr.Message)
	}
	if aerr := h.store.putLaunchTemplate(ctx, lt); aerr != nil {
		t.Fatalf("putLaunchTemplate: %s", aerr.Message)
	}
	return lt
}

func seedRouteTable(t *testing.T, h *Handler, id, vpcID string) *RouteTable {
	t.Helper()
	rt := &RouteTable{
		RouteTableID: id, VpcID: vpcID,
		Routes: []Route{{DestinationCidrBlock: "10.0.0.0/16", GatewayID: "local", Origin: "CreateRouteTable"}},
	}
	if aerr := h.store.putRouteTable(context.Background(), rt); aerr != nil {
		t.Fatalf("putRouteTable: %s", aerr.Message)
	}
	return rt
}

func seedIGW(t *testing.T, h *Handler, id string, attachedTo string) *InternetGateway {
	t.Helper()
	igw := &InternetGateway{InternetGatewayID: id}
	if attachedTo != "" {
		igw.Attachments = []IGWAttachment{{VpcID: attachedTo, State: "attached"}}
	}
	if aerr := h.store.putInternetGateway(context.Background(), igw); aerr != nil {
		t.Fatalf("putInternetGateway: %s", aerr.Message)
	}
	return igw
}

func seedVGW(t *testing.T, h *Handler, id string, attachedTo string) *VpnGateway {
	t.Helper()
	vgw := &VpnGateway{VpnGatewayID: id, State: "available", Type: "ipsec.1", AmazonSideAsn: 64512}
	if attachedTo != "" {
		vgw.Attachments = []VpnGatewayAttachment{{VpcID: attachedTo, State: "attached"}}
	}
	if aerr := h.store.putVpnGateway(context.Background(), vgw); aerr != nil {
		t.Fatalf("putVpnGateway: %s", aerr.Message)
	}
	return vgw
}

func seedEIP(t *testing.T, h *Handler, allocID string, associated bool) *ElasticIP {
	t.Helper()
	eip := &ElasticIP{AllocationID: allocID, PublicIP: "203.0.113.9", Domain: "vpc"}
	if associated {
		eip.AssociationID = "eipassoc-mut-fixed"
		eip.InstanceID = "i-mut-assoc"
	}
	if aerr := h.store.putElasticIP(context.Background(), eip); aerr != nil {
		t.Fatalf("putElasticIP: %s", aerr.Message)
	}
	return eip
}

func seedNatGateway(t *testing.T, h *Handler, id, subnetID, vpcID string) *NatGateway {
	t.Helper()
	ngw := &NatGateway{NatGatewayID: id, SubnetID: subnetID, VpcID: vpcID, State: "available", CreateTime: "2026-01-01T00:00:00.000Z"}
	if aerr := h.store.putNatGateway(context.Background(), ngw); aerr != nil {
		t.Fatalf("putNatGateway: %s", aerr.Message)
	}
	return ngw
}

func seedNetworkInterface(t *testing.T, h *Handler, id, subnetID, vpcID string) *NetworkInterface {
	t.Helper()
	eni := &NetworkInterface{
		NetworkInterfaceID: id, SubnetID: subnetID, VpcID: vpcID, AvailabilityZone: "us-east-1a",
		PrivateIPAddress: "10.0.1.40", Status: "available", MacAddress: "02:aa:bb:cc:dd:ee",
	}
	if aerr := h.store.putNetworkInterface(context.Background(), eni); aerr != nil {
		t.Fatalf("putNetworkInterface: %s", aerr.Message)
	}
	return eni
}

func seedKeyPair(t *testing.T, h *Handler, name string) *KeyPair {
	t.Helper()
	kp := &KeyPair{KeyName: name, KeyFingerprint: "aa:bb:cc", KeyPairID: "key-mut-fixed"}
	if aerr := h.store.putKeyPair(context.Background(), kp); aerr != nil {
		t.Fatalf("putKeyPair: %s", aerr.Message)
	}
	return kp
}

func seedVpcEndpoint(t *testing.T, h *Handler, id, vpcID string) *VpcEndpoint {
	t.Helper()
	ep := &VpcEndpoint{VpcEndpointID: id, VpcID: vpcID, ServiceName: "com.amazonaws.us-east-1.s3", State: "available", VpcEndpointType: "Gateway"}
	if aerr := h.store.putVpcEndpoint(context.Background(), ep); aerr != nil {
		t.Fatalf("putVpcEndpoint: %s", aerr.Message)
	}
	return ep
}

func seedVpcPeering(t *testing.T, h *Handler, id, vpcA, vpcB, status string) *VpcPeeringConnection {
	t.Helper()
	pcx := &VpcPeeringConnection{
		VpcPeeringConnectionID: id,
		RequesterVpcInfo:       VpcPeeringConnectionVpcInfo{VpcID: vpcA, Region: "us-east-1"},
		AccepterVpcInfo:        VpcPeeringConnectionVpcInfo{VpcID: vpcB, Region: "us-east-1"},
		Status:                 VpcPeeringConnectionStatus{Code: status},
	}
	if aerr := h.store.putVpcPeeringConnection(context.Background(), pcx); aerr != nil {
		t.Fatalf("putVpcPeeringConnection: %s", aerr.Message)
	}
	return pcx
}

func indexed(param string, values ...string) url.Values {
	p := url.Values{}
	for i, v := range values {
		p.Set(param+"."+strconv.Itoa(i+1), v)
	}
	return p
}

func merge(vs ...url.Values) url.Values {
	out := url.Values{}
	for _, v := range vs {
		for k, vals := range v {
			out[k] = append(out[k], vals...)
		}
	}
	return out
}

// ── The table ────────────────────────────────────────────────────────────

// mutationCases is every mutation wave 2 routed, and the request(s) it is
// checked with. Each earns its place the same way parityCases' rows do: a
// case that discriminates — a state transition, a not-found, a duplicate, a
// dependency the delete must refuse — not a bare success round-trip proving
// only that two implementations agree when nothing could have gone wrong.
func mutationCases() map[string][]mutationCase {
	return map[string][]mutationCase{
		"CreateVpc": {
			{
				name:   "default-dns-support",
				params: url.Values{"CidrBlock": {"10.9.0.0/16"}},
				after: func(t *testing.T, h *Handler, raw string) string {
					vpcID := xmlField(raw, "vpcId")
					return dispatchLegacy(t, h, "DescribeVpcAttribute", url.Values{"VpcId": {vpcID}, "Attribute": {"enableDnsSupport"}})
				},
			},
			{name: "missing-cidr", params: url.Values{}},
			{
				name:   "with-tags",
				params: withTagSpec(url.Values{"CidrBlock": {"10.9.0.0/16"}}, "vpc", "Name", "mut"),
				after:  tagsAfter("vpcId"),
			},
			{
				name:   "reserved-tag-key-refuses-the-create",
				params: withTagSpec(url.Values{"CidrBlock": {"10.9.0.0/16"}}, "vpc", "aws:reserved", "x"),
			},
		},
		"DeleteVpc": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-del") },
				params: url.Values{"VpcId": {"vpc-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeVpcs", indexed("VpcId", "vpc-mut-del"))
				},
			},
			{
				name: "dependency-violation-subnet-present",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-dep")
					seedSubnet(t, h, "subnet-mut-dep", "vpc-mut-dep")
				},
				params: url.Values{"VpcId": {"vpc-mut-dep"}},
			},
		},
		"CreateSubnet": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: url.Values{"VpcId": {"vpc-mut-a"}, "CidrBlock": {"10.0.2.0/24"}},
			},
			{name: "unknown-vpc", params: url.Values{"VpcId": {"vpc-mut-nope"}, "CidrBlock": {"10.0.2.0/24"}}},
			{
				name:   "with-tags",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(url.Values{"VpcId": {"vpc-mut-a"}, "CidrBlock": {"10.0.2.0/24"}}, "subnet", "Name", "mut"),
				after:  tagsAfter("subnetId"),
			},
			{
				name:   "reserved-tag-key-refuses-the-create",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(url.Values{"VpcId": {"vpc-mut-a"}, "CidrBlock": {"10.0.2.0/24"}}, "subnet", "aws:reserved", "x"),
			},
		},
		"DeleteSubnet": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedSubnet(t, h, "subnet-mut-del", "vpc-mut-a")
				},
				params: url.Values{"SubnetId": {"subnet-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSubnets", indexed("SubnetId", "subnet-mut-del"))
				},
			},
			{
				name: "dependency-violation-eni-present",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedSubnet(t, h, "subnet-mut-dep", "vpc-mut-a")
					seedNetworkInterface(t, h, "eni-mut-block", "subnet-mut-dep", "vpc-mut-a")
				},
				params: url.Values{"SubnetId": {"subnet-mut-dep"}},
			},
		},
		"CreateSecurityGroup": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: url.Values{"GroupName": {"mutation-web"}, "GroupDescription": {"parity"}, "VpcId": {"vpc-mut-a"}},
			},
			{
				name: "with-tags",
				seed: func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(
					url.Values{"GroupName": {"mutation-web"}, "GroupDescription": {"parity"}, "VpcId": {"vpc-mut-a"}},
					"security-group", "Name", "mut"),
				after: tagsAfter("groupId"),
			},
			{
				name: "reserved-tag-key-refuses-the-create",
				seed: func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(
					url.Values{"GroupName": {"mutation-web"}, "GroupDescription": {"parity"}, "VpcId": {"vpc-mut-a"}},
					"security-group", "aws:reserved", "x"),
			},
		},
		"DeleteSecurityGroup": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedSG(t, h, "sg-mut-del", "vpc-mut-a") },
				params: url.Values{"GroupId": {"sg-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSecurityGroups", indexed("GroupId", "sg-mut-del"))
				},
			},
			{
				name: "cannot-delete-default-group",
				seed: func(t *testing.T, h *Handler) {
					sg := &SecurityGroup{GroupID: "sg-mut-default", GroupName: "default", VpcID: "vpc-mut-a"}
					if aerr := h.store.putSecurityGroup(context.Background(), sg); aerr != nil {
						t.Fatalf("putSecurityGroup: %s", aerr.Message)
					}
				},
				params: url.Values{"GroupId": {"sg-mut-default"}},
			},
			{
				name: "dependency-violation-instance-attached",
				seed: func(t *testing.T, h *Handler) {
					seedSG(t, h, "sg-mut-attached", "vpc-mut-a")
					inst := seedInstance(t, h, "i-mut-sg", 16, "running")
					inst.SecurityGroups = []InstanceSG{{GroupID: "sg-mut-attached", GroupName: "mut-sg-mut-attached"}}
					if aerr := h.store.putInstance(context.Background(), inst); aerr != nil {
						t.Fatalf("putInstance: %s", aerr.Message)
					}
				},
				params: url.Values{"GroupId": {"sg-mut-attached"}},
			},
		},
		"AuthorizeSecurityGroupIngress": {
			{
				name:   "appends-the-rule",
				seed:   func(t *testing.T, h *Handler) { seedSG(t, h, "sg-mut-auth-in", "vpc-mut-a") },
				params: merge(url.Values{"GroupId": {"sg-mut-auth-in"}}, ipPermParams(1, "tcp", 443, 443, "0.0.0.0/0")),
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSecurityGroups", indexed("GroupId", "sg-mut-auth-in"))
				},
			},
			{name: "missing-permissions", seed: func(t *testing.T, h *Handler) { seedSG(t, h, "sg-mut-auth-empty", "vpc-mut-a") }, params: url.Values{"GroupId": {"sg-mut-auth-empty"}}},
		},
		"AuthorizeSecurityGroupEgress": {
			{
				name:   "appends-the-rule",
				seed:   func(t *testing.T, h *Handler) { seedSG(t, h, "sg-mut-auth-eg", "vpc-mut-a") },
				params: merge(url.Values{"GroupId": {"sg-mut-auth-eg"}}, ipPermParams(1, "tcp", 22, 22, "10.0.0.0/16")),
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSecurityGroups", indexed("GroupId", "sg-mut-auth-eg"))
				},
			},
		},
		"RevokeSecurityGroupIngress": {
			{
				name: "removes-the-rule",
				seed: func(t *testing.T, h *Handler) {
					sg := seedSG(t, h, "sg-mut-rev-in", "vpc-mut-a")
					sg.IpPermissions = []IpPermission{{IpProtocol: "tcp", FromPort: 443, ToPort: 443, IpRanges: []IpRange{{CidrIp: "0.0.0.0/0"}}}}
					if aerr := h.store.putSecurityGroup(context.Background(), sg); aerr != nil {
						t.Fatalf("putSecurityGroup: %s", aerr.Message)
					}
				},
				params: merge(url.Values{"GroupId": {"sg-mut-rev-in"}}, ipPermParams(1, "tcp", 443, 443, "0.0.0.0/0")),
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSecurityGroups", indexed("GroupId", "sg-mut-rev-in"))
				},
			},
			{
				name:   "rule-not-found",
				seed:   func(t *testing.T, h *Handler) { seedSG(t, h, "sg-mut-rev-nf", "vpc-mut-a") },
				params: merge(url.Values{"GroupId": {"sg-mut-rev-nf"}}, ipPermParams(1, "tcp", 443, 443, "0.0.0.0/0")),
			},
		},
		"RevokeSecurityGroupEgress": {
			{
				name: "removes-the-rule",
				seed: func(t *testing.T, h *Handler) {
					sg := seedSG(t, h, "sg-mut-rev-eg", "vpc-mut-a")
					sg.IpPermissionsEgress = []IpPermission{{IpProtocol: "-1", IpRanges: []IpRange{{CidrIp: "0.0.0.0/0"}}}}
					if aerr := h.store.putSecurityGroup(context.Background(), sg); aerr != nil {
						t.Fatalf("putSecurityGroup: %s", aerr.Message)
					}
				},
				params: merge(url.Values{"GroupId": {"sg-mut-rev-eg"}}, ipPermParams(1, "-1", 0, 0, "0.0.0.0/0")),
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSecurityGroups", indexed("GroupId", "sg-mut-rev-eg"))
				},
			},
		},
		"RunInstances": {
			{
				name: "security-groups-and-tags-applied",
				seed: func(t *testing.T, h *Handler) { seedSG(t, h, "sg-mut-run", "vpc-mut-a") },
				params: merge(
					url.Values{"ImageId": {"ami-12345678"}, "MinCount": {"1"}, "MaxCount": {"1"}},
					indexed("SecurityGroupId", "sg-mut-run"),
					url.Values{"TagSpecification.1.ResourceType": {"instance"}, "TagSpecification.1.Tag.1.Key": {"Name"}, "TagSpecification.1.Tag.1.Value": {"mut"}},
				),
				after: tagsAfter("instanceId"),
			},
			{
				name:   "bad-tag-rejected",
				params: url.Values{"ImageId": {"ami-12345678"}, "MinCount": {"1"}, "MaxCount": {"1"}, "TagSpecification.1.ResourceType": {"instance"}, "TagSpecification.1.Tag.1.Key": {"aws:owner"}, "TagSpecification.1.Tag.1.Value": {"x"}},
			},
			{
				// The launch-template merge (#518) is written once and called
				// from both bodies, and this is what says so: the template
				// supplies the AMI and the instance tags, the request wins on
				// instance type, and both paths have to agree on all three.
				name: "launch-template-merged-under-explicit-params",
				seed: func(t *testing.T, h *Handler) { seedLaunchTemplate(t, h, "lt-mut-run") },
				params: url.Values{
					"LaunchTemplate.LaunchTemplateId": {"lt-mut-run"},
					"InstanceType":                    {"c5.large"},
					"MinCount":                        {"1"},
					"MaxCount":                        {"1"},
				},
				after: tagsAfter("instanceId"),
			},
			{
				name:   "launch-template-not-found",
				params: url.Values{"LaunchTemplate.LaunchTemplateName": {"mut-no-such-template"}, "MinCount": {"1"}, "MaxCount": {"1"}},
			},
			{
				// #1722: the typed body hardcoded Placement.AvailabilityZone's
				// fallback (region+"a") instead of honouring the request
				// parameter, so a caller asking for a specific zone — Auto
				// Scaling's AZ-spreading reconciler, every time — silently got
				// a different one back from the typed path while legacy
				// launched it correctly.
				name:   "availability-zone-honoured",
				params: url.Values{"ImageId": {"ami-12345678"}, "MinCount": {"1"}, "MaxCount": {"1"}, "Placement.AvailabilityZone": {"us-east-1c"}},
			},
			{
				// #1743: the zone a subnet pins is derived by a helper both
				// bodies call rather than by two copies of the same lookup,
				// and this is what keeps it that way — a body that grows its
				// own derivation, or drops the call, reports a different
				// placement here than the other one does.
				name: "availability-zone-derived-from-subnet",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-run-zone")
					seedSubnetInZone(t, h, "subnet-mut-run-zone", "vpc-mut-run-zone", "us-east-1b")
				},
				params: url.Values{"ImageId": {"ami-12345678"}, "MinCount": {"1"}, "MaxCount": {"1"}, "SubnetId": {"subnet-mut-run-zone"}},
			},
			{
				// #1743: a request naming both a subnet and a zone that
				// disagree is refused, and the message names the subnet and
				// both zones — so this row compares the error text, not just
				// the code, between the two bodies.
				name: "availability-zone-conflicts-with-subnet",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-run-zone")
					seedSubnetInZone(t, h, "subnet-mut-run-zone", "vpc-mut-run-zone", "us-east-1b")
				},
				params: url.Values{"ImageId": {"ami-12345678"}, "MinCount": {"1"}, "MaxCount": {"1"}, "SubnetId": {"subnet-mut-run-zone"}, "Placement.AvailabilityZone": {"us-east-1c"}},
			},
		},
		"TerminateInstances": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedInstance(t, h, "i-mut-term", 16, "running") },
				params: indexed("InstanceId", "i-mut-term"),
			},
			{name: "not-found", params: indexed("InstanceId", "i-mut-nope")},
		},
		"StopInstances": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedInstance(t, h, "i-mut-stop", 16, "running") },
				params: indexed("InstanceId", "i-mut-stop"),
			},
			{
				name:   "wrong-state",
				seed:   func(t *testing.T, h *Handler) { seedInstance(t, h, "i-mut-stopped", 48, "terminated") },
				params: indexed("InstanceId", "i-mut-stopped"),
			},
		},
		"StartInstances": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedInstance(t, h, "i-mut-start", 80, "stopped") },
				params: indexed("InstanceId", "i-mut-start"),
			},
			{
				name:   "wrong-state",
				seed:   func(t *testing.T, h *Handler) { seedInstance(t, h, "i-mut-running", 16, "running") },
				params: indexed("InstanceId", "i-mut-running"),
			},
		},
		"ModifyInstanceAttribute": {
			{
				name:   "instance-type-persisted",
				seed:   func(t *testing.T, h *Handler) { seedInstance(t, h, "i-mut-attr", 80, "stopped") },
				params: url.Values{"InstanceId": {"i-mut-attr"}, "InstanceType.Value": {"m5.large"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeInstances", indexed("InstanceId", "i-mut-attr"))
				},
			},
		},
		"ModifySubnetAttribute": {
			{
				name:   "map-public-ip-persisted",
				seed:   func(t *testing.T, h *Handler) { seedSubnet(t, h, "subnet-mut-attr", "vpc-mut-a") },
				params: url.Values{"SubnetId": {"subnet-mut-attr"}, "MapPublicIpOnLaunch.Value": {"true"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeSubnets", indexed("SubnetId", "subnet-mut-attr"))
				},
			},
		},
		"ModifyVpcAttribute": {
			{
				name:   "dns-support-and-hostnames-persisted",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-attr") },
				params: url.Values{"VpcId": {"vpc-mut-attr"}, "EnableDnsSupport.Value": {"false"}, "EnableDnsHostnames.Value": {"true"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					support := dispatchLegacy(t, h, "DescribeVpcAttribute", url.Values{"VpcId": {"vpc-mut-attr"}, "Attribute": {"enableDnsSupport"}})
					hostnames := dispatchLegacy(t, h, "DescribeVpcAttribute", url.Values{"VpcId": {"vpc-mut-attr"}, "Attribute": {"enableDnsHostnames"}})
					return support + "\n" + hostnames
				},
			},
		},
		"CreateTags": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: url.Values{"ResourceId.1": {"vpc-mut-a"}, "Tag.1.Key": {"Name"}, "Tag.1.Value": {"mut"}},
			},
			{name: "missing-resource-id", params: url.Values{"Tag.1.Key": {"Name"}, "Tag.1.Value": {"mut"}}},
		},
		"DeleteTags": {
			{
				name: "removes-the-key",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					if aerr := h.putResourceTags(context.Background(), "vpc-mut-a", []Tag{{Key: "Name", Value: "mut"}, {Key: "Env", Value: "test"}}); aerr != nil {
						t.Fatalf("putResourceTags: %s", aerr.Message)
					}
				},
				params: url.Values{"ResourceId.1": {"vpc-mut-a"}, "Tag.1.Key": {"Name"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeTags", filterParams("resource-id", "vpc-mut-a"))
				},
			},
		},
		"CreateKeyPair": {
			{name: "success", params: url.Values{"KeyName": {"mutation-key"}}},
			{
				name:   "duplicate",
				seed:   func(t *testing.T, h *Handler) { seedKeyPair(t, h, "mutation-dup") },
				params: url.Values{"KeyName": {"mutation-dup"}},
			},
			{
				name:   "with-tags",
				params: withTagSpec(url.Values{"KeyName": {"mutation-key"}}, "key-pair", "Name", "mut"),
				// A key pair's tags are stored against its name, matching
				// DeleteKeyPair's tag cleanup — not against the minted keyPairId.
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeTags", filterParams("resource-id", "mutation-key"))
				},
			},
			{
				name:   "reserved-tag-key-refuses-the-create",
				params: withTagSpec(url.Values{"KeyName": {"mutation-key"}}, "key-pair", "aws:reserved", "x"),
			},
		},
		"DeleteKeyPair": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedKeyPair(t, h, "mutation-del") },
				params: url.Values{"KeyName": {"mutation-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeKeyPairs", indexed("KeyName", "mutation-del"))
				},
			},
			{name: "idempotent-when-absent", params: url.Values{"KeyName": {"mutation-absent"}}},
		},
		"CreateRouteTable": {
			{name: "success", seed: func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") }, params: url.Values{"VpcId": {"vpc-mut-a"}}},
			{name: "unknown-vpc", params: url.Values{"VpcId": {"vpc-mut-nope"}}},
			{
				name:   "with-tags",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(url.Values{"VpcId": {"vpc-mut-a"}}, "route-table", "Name", "mut"),
				after:  tagsAfter("routeTableId"),
			},
			{
				name:   "reserved-tag-key-refuses-the-create",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(url.Values{"VpcId": {"vpc-mut-a"}}, "route-table", "aws:reserved", "x"),
			},
		},
		"DeleteRouteTable": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedRouteTable(t, h, "rtb-mut-del", "vpc-mut-a") },
				params: url.Values{"RouteTableId": {"rtb-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeRouteTables", indexed("RouteTableId", "rtb-mut-del"))
				},
			},
			{
				name: "cannot-delete-main-table",
				seed: func(t *testing.T, h *Handler) {
					rt := seedRouteTable(t, h, "rtb-mut-main", "vpc-mut-a")
					rt.Associations = []RouteTableAssociation{{AssociationID: "rtbassoc-mut-main", RouteTableID: rt.RouteTableID, Main: true}}
					if aerr := h.store.putRouteTable(context.Background(), rt); aerr != nil {
						t.Fatalf("putRouteTable: %s", aerr.Message)
					}
				},
				params: url.Values{"RouteTableId": {"rtb-mut-main"}},
			},
		},
		"CreateRoute": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedRouteTable(t, h, "rtb-mut-route", "vpc-mut-a") },
				params: url.Values{"RouteTableId": {"rtb-mut-route"}, "DestinationCidrBlock": {"0.0.0.0/0"}, "GatewayId": {"igw-mut-fixed"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeRouteTables", indexed("RouteTableId", "rtb-mut-route"))
				},
			},
		},
		"DeleteRoute": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					rt := seedRouteTable(t, h, "rtb-mut-delroute", "vpc-mut-a")
					rt.Routes = append(rt.Routes, Route{DestinationCidrBlock: "0.0.0.0/0", GatewayID: "igw-mut-fixed", Origin: "CreateRoute"})
					if aerr := h.store.putRouteTable(context.Background(), rt); aerr != nil {
						t.Fatalf("putRouteTable: %s", aerr.Message)
					}
				},
				params: url.Values{"RouteTableId": {"rtb-mut-delroute"}, "DestinationCidrBlock": {"0.0.0.0/0"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeRouteTables", indexed("RouteTableId", "rtb-mut-delroute"))
				},
			},
			{
				name:   "not-found",
				seed:   func(t *testing.T, h *Handler) { seedRouteTable(t, h, "rtb-mut-nf", "vpc-mut-a") },
				params: url.Values{"RouteTableId": {"rtb-mut-nf"}, "DestinationCidrBlock": {"172.16.0.0/16"}},
			},
		},
		"AssociateRouteTable": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedRouteTable(t, h, "rtb-mut-assoc", "vpc-mut-a")
					seedSubnet(t, h, "subnet-mut-assoc", "vpc-mut-a")
				},
				params: url.Values{"RouteTableId": {"rtb-mut-assoc"}, "SubnetId": {"subnet-mut-assoc"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeRouteTables", indexed("RouteTableId", "rtb-mut-assoc"))
				},
			},
		},
		"DisassociateRouteTable": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					rt := seedRouteTable(t, h, "rtb-mut-disassoc", "vpc-mut-a")
					rt.Associations = []RouteTableAssociation{{AssociationID: "rtbassoc-mut-fixed", RouteTableID: rt.RouteTableID, SubnetID: "subnet-mut-a", Main: false}}
					if aerr := h.store.putRouteTable(context.Background(), rt); aerr != nil {
						t.Fatalf("putRouteTable: %s", aerr.Message)
					}
				},
				params: url.Values{"AssociationId": {"rtbassoc-mut-fixed"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeRouteTables", indexed("RouteTableId", "rtb-mut-disassoc"))
				},
			},
			{name: "not-found", params: url.Values{"AssociationId": {"rtbassoc-mut-nope"}}},
		},
		"CreateInternetGateway": {
			{name: "success"},
			{
				name:   "with-tags",
				params: withTagSpec(url.Values{}, "internet-gateway", "Name", "mut"),
				after:  tagsAfter("internetGatewayId"),
			},
			{
				name:   "reserved-tag-key-refuses-the-create",
				params: withTagSpec(url.Values{}, "internet-gateway", "aws:reserved", "x"),
			},
		},
		"DeleteInternetGateway": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedIGW(t, h, "igw-mut-del", "") },
				params: url.Values{"InternetGatewayId": {"igw-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeInternetGateways", indexed("InternetGatewayId", "igw-mut-del"))
				},
			},
			{
				name:   "dependency-violation-attached",
				seed:   func(t *testing.T, h *Handler) { seedIGW(t, h, "igw-mut-att", "vpc-mut-a") },
				params: url.Values{"InternetGatewayId": {"igw-mut-att"}},
			},
		},
		"AttachInternetGateway": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedIGW(t, h, "igw-mut-attach", "")
				},
				params: url.Values{"InternetGatewayId": {"igw-mut-attach"}, "VpcId": {"vpc-mut-a"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeInternetGateways", indexed("InternetGatewayId", "igw-mut-attach"))
				},
			},
			{
				name:   "already-attached",
				seed:   func(t *testing.T, h *Handler) { seedIGW(t, h, "igw-mut-already", "vpc-mut-a") },
				params: url.Values{"InternetGatewayId": {"igw-mut-already"}, "VpcId": {"vpc-mut-a"}},
			},
		},
		"DetachInternetGateway": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedIGW(t, h, "igw-mut-detach", "vpc-mut-a") },
				params: url.Values{"InternetGatewayId": {"igw-mut-detach"}, "VpcId": {"vpc-mut-a"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeInternetGateways", indexed("InternetGatewayId", "igw-mut-detach"))
				},
			},
			{
				name:   "not-attached",
				seed:   func(t *testing.T, h *Handler) { seedIGW(t, h, "igw-mut-notattached", "") },
				params: url.Values{"InternetGatewayId": {"igw-mut-notattached"}, "VpcId": {"vpc-mut-a"}},
			},
		},
		"CreateVpcPeeringConnection": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedVPC(t, h, "vpc-mut-b")
				},
				params: url.Values{"VpcId": {"vpc-mut-a"}, "PeerVpcId": {"vpc-mut-b"}},
			},
			{name: "unknown-peer", seed: func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") }, params: url.Values{"VpcId": {"vpc-mut-a"}, "PeerVpcId": {"vpc-mut-nope"}}},
			{
				name: "with-tags",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedVPC(t, h, "vpc-mut-b")
				},
				params: withTagSpec(
					url.Values{"VpcId": {"vpc-mut-a"}, "PeerVpcId": {"vpc-mut-b"}},
					"vpc-peering-connection", "Name", "mut"),
				after: tagsAfter("vpcPeeringConnectionId"),
			},
			{
				name: "reserved-tag-key-refuses-the-create",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedVPC(t, h, "vpc-mut-b")
				},
				params: withTagSpec(
					url.Values{"VpcId": {"vpc-mut-a"}, "PeerVpcId": {"vpc-mut-b"}},
					"vpc-peering-connection", "aws:reserved", "x"),
			},
		},
		"AcceptVpcPeeringConnection": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedVpcPeering(t, h, "pcx-mut-accept", "vpc-mut-a", "vpc-mut-b", "pending-acceptance")
				},
				params: url.Values{"VpcPeeringConnectionId": {"pcx-mut-accept"}},
			},
			{
				name: "wrong-state",
				seed: func(t *testing.T, h *Handler) {
					seedVpcPeering(t, h, "pcx-mut-active", "vpc-mut-a", "vpc-mut-b", "active")
				},
				params: url.Values{"VpcPeeringConnectionId": {"pcx-mut-active"}},
			},
		},
		"DeleteVpcPeeringConnection": {
			{
				name: "from-active",
				seed: func(t *testing.T, h *Handler) {
					seedVpcPeering(t, h, "pcx-mut-del", "vpc-mut-a", "vpc-mut-b", "active")
				},
				params: url.Values{"VpcPeeringConnectionId": {"pcx-mut-del"}},
			},
			{
				name: "already-deleted",
				seed: func(t *testing.T, h *Handler) {
					seedVpcPeering(t, h, "pcx-mut-deleted", "vpc-mut-a", "vpc-mut-b", "deleted")
				},
				params: url.Values{"VpcPeeringConnectionId": {"pcx-mut-deleted"}},
			},
		},
		"AllocateAddress": {
			{name: "default-domain", params: url.Values{}},
			{name: "explicit-domain", params: url.Values{"Domain": {"standard"}}},
			{
				name:   "with-tags",
				params: withTagSpec(url.Values{}, "elastic-ip", "Name", "mut"),
				after:  tagsAfter("allocationId"),
			},
			{
				name:   "reserved-tag-key-refuses-the-create",
				params: withTagSpec(url.Values{}, "elastic-ip", "aws:reserved", "x"),
			},
		},
		"ReleaseAddress": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedEIP(t, h, "eipalloc-mut-del", false) },
				params: url.Values{"AllocationId": {"eipalloc-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeAddresses", indexed("AllocationId", "eipalloc-mut-del"))
				},
			},
		},
		"AssociateAddress": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedEIP(t, h, "eipalloc-mut-assoc", false) },
				params: url.Values{"AllocationId": {"eipalloc-mut-assoc"}, "InstanceId": {"i-mut-target"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeAddresses", indexed("AllocationId", "eipalloc-mut-assoc"))
				},
			},
		},
		"DisassociateAddress": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedEIP(t, h, "eipalloc-mut-disassoc", true) },
				params: url.Values{"AssociationId": {"eipassoc-mut-fixed"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeAddresses", indexed("AllocationId", "eipalloc-mut-disassoc"))
				},
			},
		},
		"CreateNatGateway": {
			{
				name: "tags-applied",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedSubnet(t, h, "subnet-mut-a", "vpc-mut-a")
				},
				params: url.Values{
					"SubnetId":                        {"subnet-mut-a"},
					"TagSpecification.1.ResourceType": {"natgateway"}, "TagSpecification.1.Tag.1.Key": {"Name"}, "TagSpecification.1.Tag.1.Value": {"mut"},
				},
				after: tagsAfter("natGatewayId"),
			},
			{name: "unknown-subnet", params: url.Values{"SubnetId": {"subnet-mut-nope"}}},
			{
				name: "reserved-tag-key-refuses-the-create",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedSubnet(t, h, "subnet-mut-a", "vpc-mut-a")
				},
				params: withTagSpec(url.Values{"SubnetId": {"subnet-mut-a"}}, "natgateway", "aws:reserved", "x"),
			},
		},
		"DeleteNatGateway": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedSubnet(t, h, "subnet-mut-a", "vpc-mut-a")
					seedNatGateway(t, h, "nat-mut-del", "subnet-mut-a", "vpc-mut-a")
				},
				params: url.Values{"NatGatewayId": {"nat-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeNatGateways", indexed("NatGatewayId", "nat-mut-del"))
				},
			},
		},
		"CreateVpnGateway": {
			{
				name: "tags-applied",
				params: url.Values{
					"Type":                            {"ipsec.1"},
					"TagSpecification.1.ResourceType": {"vpn-gateway"}, "TagSpecification.1.Tag.1.Key": {"Name"}, "TagSpecification.1.Tag.1.Value": {"mut"},
				},
				after: tagsAfter("vpnGatewayId"),
			},
			{name: "wrong-type", params: url.Values{"Type": {"ipsec.2"}}},
			{
				name:   "reserved-tag-key-refuses-the-create",
				params: withTagSpec(url.Values{"Type": {"ipsec.1"}}, "vpn-gateway", "aws:reserved", "x"),
			},
		},
		"AttachVpnGateway": {
			{
				name: "success",
				seed: func(t *testing.T, h *Handler) {
					seedVPC(t, h, "vpc-mut-a")
					seedVGW(t, h, "vgw-mut-attach", "")
				},
				params: url.Values{"VpnGatewayId": {"vgw-mut-attach"}, "VpcId": {"vpc-mut-a"}},
			},
			{
				name:   "already-attached",
				seed:   func(t *testing.T, h *Handler) { seedVGW(t, h, "vgw-mut-already", "vpc-mut-a") },
				params: url.Values{"VpnGatewayId": {"vgw-mut-already"}, "VpcId": {"vpc-mut-a"}},
			},
		},
		"DetachVpnGateway": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVGW(t, h, "vgw-mut-detach", "vpc-mut-a") },
				params: url.Values{"VpnGatewayId": {"vgw-mut-detach"}, "VpcId": {"vpc-mut-a"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeVpnGateways", indexed("VpnGatewayId", "vgw-mut-detach"))
				},
			},
		},
		"DeleteVpnGateway": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVGW(t, h, "vgw-mut-del", "") },
				params: url.Values{"VpnGatewayId": {"vgw-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeVpnGateways", indexed("VpnGatewayId", "vgw-mut-del"))
				},
			},
			{
				name:   "dependency-violation-attached",
				seed:   func(t *testing.T, h *Handler) { seedVGW(t, h, "vgw-mut-att", "vpc-mut-a") },
				params: url.Values{"VpnGatewayId": {"vgw-mut-att"}},
			},
		},
		"CreateNetworkInterface": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedSubnet(t, h, "subnet-mut-a", "vpc-mut-a") },
				params: url.Values{"SubnetId": {"subnet-mut-a"}, "Description": {"mutation parity"}},
			},
			{
				name: "with-tags",
				seed: func(t *testing.T, h *Handler) { seedSubnet(t, h, "subnet-mut-a", "vpc-mut-a") },
				params: withTagSpec(
					url.Values{"SubnetId": {"subnet-mut-a"}, "Description": {"mutation parity"}},
					"network-interface", "Name", "mut"),
				after: tagsAfter("networkInterfaceId"),
			},
			{
				name: "reserved-tag-key-refuses-the-create",
				seed: func(t *testing.T, h *Handler) { seedSubnet(t, h, "subnet-mut-a", "vpc-mut-a") },
				params: withTagSpec(
					url.Values{"SubnetId": {"subnet-mut-a"}, "Description": {"mutation parity"}},
					"network-interface", "aws:reserved", "x"),
			},
		},
		"DeleteNetworkInterface": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedNetworkInterface(t, h, "eni-mut-del", "subnet-mut-a", "vpc-mut-a") },
				params: url.Values{"NetworkInterfaceId": {"eni-mut-del"}},
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeNetworkInterfaces", indexed("NetworkInterfaceId", "eni-mut-del"))
				},
			},
		},
		"CreateVpcEndpoint": {
			{
				name:   "type-defaults-to-gateway",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: url.Values{"VpcId": {"vpc-mut-a"}, "ServiceName": {"com.amazonaws.us-east-1.s3"}},
			},
			{
				name:   "interface-type-honoured",
				seed:   func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: url.Values{"VpcId": {"vpc-mut-a"}, "ServiceName": {"com.amazonaws.us-east-1.ec2"}, "VpcEndpointType": {"Interface"}},
			},
			{
				name: "with-tags",
				seed: func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(
					url.Values{"VpcId": {"vpc-mut-a"}, "ServiceName": {"com.amazonaws.us-east-1.s3"}},
					"vpc-endpoint", "Name", "mut"),
				after: tagsAfter("vpcEndpointId"),
			},
			{
				name: "reserved-tag-key-refuses-the-create",
				seed: func(t *testing.T, h *Handler) { seedVPC(t, h, "vpc-mut-a") },
				params: withTagSpec(
					url.Values{"VpcId": {"vpc-mut-a"}, "ServiceName": {"com.amazonaws.us-east-1.s3"}},
					"vpc-endpoint", "aws:reserved", "x"),
			},
		},
		"DeleteVpcEndpoints": {
			{
				name:   "success",
				seed:   func(t *testing.T, h *Handler) { seedVpcEndpoint(t, h, "vpce-mut-del", "vpc-mut-a") },
				params: indexed("VpcEndpointId", "vpce-mut-del"),
				after: func(t *testing.T, h *Handler, _ string) string {
					return dispatchLegacy(t, h, "DescribeVpcEndpoints", indexed("VpcEndpointId", "vpce-mut-del"))
				},
			},
			{name: "unknown-id-silently-skipped", params: indexed("VpcEndpointId", "vpce-mut-nope")},
		},
	}
}

// withTagSpec returns params plus one TagSpecification.N entry, so a create
// case can be checked with tag input without restating the whole request.
//
// Every create AWS models with a TagSpecifications member gets two of these:
// one carrying a tag that must survive to the response's tagSet and the tag
// store, and one carrying the reserved aws: prefix that must refuse the call
// before the resource is made. Neither existed for six of these operations,
// which is how the typed twins came to drop create-time tags on the floor
// with the differential table still green.
func withTagSpec(params url.Values, resourceType, key, value string) url.Values {
	out := url.Values{}
	for k, vs := range params {
		out[k] = append([]string(nil), vs...)
	}
	out.Set("TagSpecification.1.ResourceType", resourceType)
	out.Set("TagSpecification.1.Tag.1.Key", key)
	out.Set("TagSpecification.1.Tag.1.Value", value)
	return out
}

// tagsAfter drives a legacy DescribeTags for the resource a create minted, so
// a case's compared string carries what the mutation *stored* and not only
// what it echoed. CreateKeyPair is the reason both halves are checked: it
// echoed a tagSet it had not written.
func tagsAfter(idField string) func(t *testing.T, h *Handler, raw string) string {
	return func(t *testing.T, h *Handler, raw string) string {
		t.Helper()
		return dispatchLegacy(t, h, "DescribeTags", filterParams("resource-id", xmlField(raw, idField)))
	}
}

// ipPermParams builds IpPermissions.N.* params for one rule.
func ipPermParams(n int, protocol string, fromPort, toPort int, cidr string) url.Values {
	p := url.Values{}
	idx := strconv.Itoa(n)
	p.Set("IpPermissions."+idx+".IpProtocol", protocol)
	p.Set("IpPermissions."+idx+".FromPort", strconv.Itoa(fromPort))
	p.Set("IpPermissions."+idx+".ToPort", strconv.Itoa(toPort))
	p.Set("IpPermissions."+idx+".IpRanges.1.CidrIp", cidr)
	return p
}

// TestTypedParityMutations is the mutation-side gate every operation moved
// off ec2TypedDispatchRemainder in wave 2 passed to get there: seeded
// identically, one side legacy and one side typed, the same request answers
// the same bytes and — where the bare response cannot show it — leaves the
// store in the same state.
func TestTypedParityMutations(t *testing.T) {
	cases := mutationCases()
	actions := make([]string, 0, len(cases))
	for action := range cases {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			for _, c := range cases[action] {
				t.Run(c.name, func(t *testing.T) {
					legacy, typed := answerBothMutation(t, action, c)
					if legacy != typed {
						t.Errorf("the two dispatch paths disagree.\nlegacy:\n%s\n\ntyped:\n%s", legacy, typed)
					}
				})
			}
		})
	}
}

// Every create AWS models with a TagSpecifications member must be checked in
// this table with tag input, both ways round: a valid tag whose fate is
// followed into the store by an `after` probe, and a reserved key that must
// refuse the call.
//
// This is the row that did not exist. The differential table was the
// guarantee behind routing 69 operations to the typed registry, and it had no
// case anywhere that sent a create-time tag to CreateVpc, CreateSubnet,
// CreateSecurityGroup, CreateInternetGateway, CreateRouteTable or
// CreateNetworkInterface — so six typed twins that never decoded
// TagSpecification.N compared byte-identical to six legacy handlers that did,
// on requests that carried none.
//
// The `after` probe is required rather than optional because a response that
// echoes a tagSet proves only that the handler read the request: CreateKeyPair
// echoed tags it had not written.
func TestTypedParityMutations_everyTagSpecCreateIsCheckedWithTags(t *testing.T) {
	mutations := mutationCases()
	for action, resourceType := range tagSpecCreates {
		if !ec2TypedOps[action] {
			continue
		}
		t.Run(action, func(t *testing.T) {
			valid, reserved := false, false
			for _, row := range mutations[action] {
				if row.params.Get("TagSpecification.1.ResourceType") != resourceType {
					continue
				}
				if strings.HasPrefix(row.params.Get("TagSpecification.1.Tag.1.Key"), "aws:") {
					reserved = true
					continue
				}
				if row.after != nil {
					valid = true
				}
			}
			if !valid {
				t.Errorf("%s has no mutationCases row sending TagSpecification.1.ResourceType=%s with an "+
					"`after` probe — nothing here would notice its typed body dropping create-time tags",
					action, resourceType)
			}
			if !reserved {
				t.Errorf("%s has no mutationCases row sending a reserved aws: tag key — nothing here would "+
					"notice its typed body creating the resource instead of refusing the call", action)
			}
		})
	}
}

// Every operation wave 2 routed that is not a Describe* (parityCases' job)
// must have a row here — the same discipline TestTypedParity_coversEveryRoutedOp
// holds parityCases to, applied to the half of the ledger that needed a
// second table because a mutation cannot be checked answerBoth's way.
func TestTypedParityMutations_coversEveryRoutedMutation(t *testing.T) {
	describes := parityCases()
	mutations := mutationCases()
	for action := range ec2TypedOps {
		if _, isDescribe := describes[action]; isDescribe {
			continue
		}
		rows, ok := mutations[action]
		if !ok || len(rows) == 0 {
			t.Errorf("%s is routed to the typed registry with no row in parityCases or mutationCases", action)
		}
	}
	for action := range mutations {
		if !ec2TypedOps[action] {
			t.Errorf("mutationCases has a row for %s, which is not routed — drop the row or route the operation", action)
		}
		if _, isDescribe := describes[action]; isDescribe {
			t.Errorf("%s has rows in both parityCases and mutationCases", action)
		}
	}
}
