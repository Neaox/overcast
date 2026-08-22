// golden_test.go contains wire-byte golden tests for EC2 — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// EC2 is Query/XML only (internal/services/ec2/typed_ops.go's
// SupportedProtocols returns only codec.QueryXML), and per #1204's own
// framing, the legacy and typed Describe paths converge on one body post-#1217
// — there is only one wire shape to golden per operation, no dual-path split.
//
// Coverage — the eight highest-value management/instance operations:
//
//	CreateVpc, DescribeVpcs, CreateSecurityGroup, DescribeSecurityGroups,
//	DescribeSubnets, RunInstances, DescribeInstances, TerminateInstances
//
// Plus one error-shape golden: CreateSubnet against a nonexistent VpcId,
// pinning the InvalidVpcID.NotFound fault. CreateSubnet is now answered by
// the typed registry too (#754 wave 2 emptied ec2TypedDispatchRemainder,
// typed_dispatch.go) — this golden is unchanged from when it pinned the
// legacy path, which is exactly the property the differential parity test
// (typed_parity_mutations_dev_test.go) exists to guarantee: whichever
// decoder read the request, the error is byte-identical.
//
// Determinism — EC2 is the trickiest of the five priority services because
// EVERY resource ID (instance, VPC, subnet, security group, ENI,
// reservation, CIDR association) is minted by shortID() (handler.go:
// strings.ReplaceAll(uuid.NewString(), "-", "")[:8] — 8 random hex chars from
// a v4 UUID), on every single call, not just between record and assert. Worse,
// a default VPC/subnet/security-group triple is auto-created the first time
// ANY VPC-scoped read touches a region (default_vpc.go's
// ensureDefaultVPCQuietly), so even a bare DescribeVpcs on a freshly-created
// server returns a random default VPC ID. normalizeEC2IDs regex-redacts every
// <prefix>-<8 hex chars> shape uniformly. Separately, DescribeVpcs/
// DescribeSecurityGroups/DescribeSubnets read from an ordered (Ascend'd)
// B-tree keyed by resource ID (see ec2Store.listVPCs etc. in store.go) — so
// when more than one resource of the same kind exists (the default plus one
// this test creates), their RELATIVE ORDER depends on the random-hex ID
// comparison and is itself non-deterministic. Every Describe* golden here
// therefore filters to the single resource this test created (VpcId.1,
// GroupId.1, SubnetId.1, InstanceId.1), sidestepping cross-resource ordering
// entirely rather than trying to normalize it away.
//
// helpers.WithMockClock() makes LaunchTime/CreateDate-shaped fields
// deterministic. RunInstances'/DescribeInstances' PrivateIPAddress is not
// random, but it is not per-test-server either: sharedVPCStrategy.
// AllocatePrivateIP (vpc_strategy.go) draws from syntheticIPCounter, a
// package-level atomic.Uint32 in handler.go shared by every EC2 operation
// that allocates a private IP anywhere in this test binary process, never
// reset between helpers.NewTestServer calls (only between separate `go test`
// processes) — so its concrete value depends on how many other tests in this
// package allocated one first. Rather than betting on cross-file test-order
// stability (fragile under an unfiltered `go test ./tests/integration/ec2/...`,
// which is how CI actually exercises this package), privateIPRE redacts it.
//
// Record: go test ./tests/integration/ec2/ -run TestGolden -record
// Assert: go test ./tests/integration/ec2/ -run TestGolden
package ec2_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const ec2GoldenDir = "goldens"

// goldenRequestID pins the Query/XML protocol's embedded RequestID element
// (every EC2 response, success or error, carries one in the body — see
// ec2XMLNS-tagged response structs in typed_logic.go and
// protocol.WriteEC2QueryXMLError) so it is reproducible across record and
// repeated assert runs, the same technique used in the STS/IAM golden tests.
const goldenRequestID = "00000000-0000-0000-0000-000000000000"

// ec2IDRE matches every shortID()-minted resource identifier this emulator
// hands back: an 8-hex-char suffix after one of the known resource-type
// prefixes. vpc-cidr-assoc- is listed before the plainer vpc- alternative so
// the longer, more specific prefix always wins the match — not that it
// matters in practice, since "cidr-ass" right after "vpc-" contains letters
// outside [0-9a-f] and so can never satisfy the plain-vpc branch's 8-hex-char
// requirement anyway; the ordering just keeps that safety property explicit.
//
// dopt- (CreateVpc's DhcpOptionsID) is included here too: an earlier version
// of this regex omitted it, which recorded a real random dopt-XXXXXXXX value
// straight into CreateVpc.json — passing at record time but guaranteed to
// fail the very next assert run once a fresh process minted a different one.
var ec2IDRE = regexp.MustCompile(`\b(vpc-cidr-assoc|dopt|i|vpc|subnet|sg|eni|r|key)-[0-9a-f]{8}\b`)

// privateIPRE matches the <privateIpAddress> element RunInstances/
// DescribeInstances embed. sharedVPCStrategy.AllocatePrivateIP
// (vpc_strategy.go) derives it from vpc.CidrBlock plus
// syntheticIPCounter.Add(1) — a package-level atomic.Uint32 in handler.go
// shared by every EC2 operation in this test binary that ever allocates a
// private IP, and never reset between helpers.NewTestServer calls (only
// between separate `go test` processes). That makes the concrete value
// dependent on how many OTHER tests in this package allocated one first —
// stable under `-run TestGolden` alone (this file is the only caller, always
// in the same order), but not under an unfiltered `go test
// ./tests/integration/ec2/...` (e.g. the plain `go test ./...` sweep CI
// actually runs), where every earlier test in ec2_test.go/
// ec2_eventbridge_test.go has already advanced the same counter. Redacting
// it — rather than betting on cross-file test-order stability — is what
// makes these two goldens robust under both invocations.
var privateIPRE = regexp.MustCompile(`<privateIpAddress>[\d.]+</privateIpAddress>`)

// normalizeEC2IDs redacts every random resource ID to a fixed placeholder
// that keeps the prefix (so a reviewer can still tell what kind of resource
// each field names) while treating the random suffix as opaque, plus the
// counter-derived private IP address (see privateIPRE).
func normalizeEC2IDs(body []byte) []byte {
	body = ec2IDRE.ReplaceAll(body, []byte("$1-00000000"))
	body = privateIPRE.ReplaceAll(body, []byte("<privateIpAddress>REDACTED</privateIpAddress>"))
	return body
}

// ec2GoldenCall sends a Query-protocol request with a pinned x-amzn-requestid
// header, so the RequestID embedded in the response body (see the package
// comment) is reproducible without its own regex. It duplicates ec2Query's
// (ec2_test.go) request construction rather than extending it, since that
// helper has no way to set request headers and golden determinism needs one.
func ec2GoldenCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2016-11-15")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("ec2GoldenCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("x-amzn-requestid", goldenRequestID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ec2GoldenCall %s: %v", action, err)
	}
	return resp
}

// xmlField extracts the first "<tag>value</tag>" occurrence from resp's body
// and closes it. It's a deliberately minimal scrape (not a full XML decode)
// used only to pull one ID out of a setup call's response so the next call in
// the chain can reference it — the setup call's own wire bytes are never
// asserted against a golden.
func xmlField(t *testing.T, resp *http.Response, tag string) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("xmlField %s: read body: %v", tag, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xmlField %s: setup call failed, status %d: %s", tag, resp.StatusCode, b)
	}
	re := regexp.MustCompile(`<` + tag + `>([^<]*)</` + tag + `>`)
	m := re.FindSubmatch(b)
	if m == nil {
		t.Fatalf("xmlField %s: not found in body: %s", tag, b)
	}
	return string(m[1])
}

func TestGolden_CreateVpc(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := ec2GoldenCall(t, srv, "CreateVpc", url.Values{
		"CidrBlock": {"10.100.0.0/16"},
	})
	helpers.GoldenTest(t, ec2GoldenDir, "CreateVpc", resp, normalizeEC2IDs)
}

func TestGolden_DescribeVpcs(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")

	resp := ec2GoldenCall(t, srv, "DescribeVpcs", url.Values{"VpcId.1": {vpcID}})
	helpers.GoldenTest(t, ec2GoldenDir, "DescribeVpcs", resp, normalizeEC2IDs)
}

func TestGolden_CreateSecurityGroup(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")

	resp := ec2GoldenCall(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName":        {"golden-sg"},
		"GroupDescription": {"golden security group"},
		"VpcId":            {vpcID},
	})
	helpers.GoldenTest(t, ec2GoldenDir, "CreateSecurityGroup", resp, normalizeEC2IDs)
}

func TestGolden_DescribeSecurityGroups(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")
	sgResp := ec2GoldenCall(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName": {"golden-sg"}, "GroupDescription": {"golden security group"}, "VpcId": {vpcID},
	})
	groupID := xmlField(t, sgResp, "groupId")

	resp := ec2GoldenCall(t, srv, "DescribeSecurityGroups", url.Values{"GroupId.1": {groupID}})
	helpers.GoldenTest(t, ec2GoldenDir, "DescribeSecurityGroups", resp, normalizeEC2IDs)
}

func TestGolden_DescribeSubnets(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")
	subResp := ec2GoldenCall(t, srv, "CreateSubnet", url.Values{
		"VpcId": {vpcID}, "CidrBlock": {"10.100.1.0/24"}, "AvailabilityZone": {"us-east-1a"},
	})
	subnetID := xmlField(t, subResp, "subnetId")

	resp := ec2GoldenCall(t, srv, "DescribeSubnets", url.Values{"SubnetId.1": {subnetID}})
	helpers.GoldenTest(t, ec2GoldenDir, "DescribeSubnets", resp, normalizeEC2IDs)
}

func TestGolden_RunInstances(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")
	subResp := ec2GoldenCall(t, srv, "CreateSubnet", url.Values{
		"VpcId": {vpcID}, "CidrBlock": {"10.100.1.0/24"}, "AvailabilityZone": {"us-east-1a"},
	})
	subnetID := xmlField(t, subResp, "subnetId")

	resp := ec2GoldenCall(t, srv, "RunInstances", url.Values{
		"ImageId":      {"ami-golden12345"},
		"InstanceType": {"t3.micro"},
		"MinCount":     {"1"},
		"MaxCount":     {"1"},
		"SubnetId":     {subnetID},
	})
	helpers.GoldenTest(t, ec2GoldenDir, "RunInstances", resp, normalizeEC2IDs)
}

func TestGolden_DescribeInstances(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")
	subResp := ec2GoldenCall(t, srv, "CreateSubnet", url.Values{
		"VpcId": {vpcID}, "CidrBlock": {"10.100.1.0/24"}, "AvailabilityZone": {"us-east-1a"},
	})
	subnetID := xmlField(t, subResp, "subnetId")
	runResp := ec2GoldenCall(t, srv, "RunInstances", url.Values{
		"ImageId": {"ami-golden12345"}, "InstanceType": {"t3.micro"},
		"MinCount": {"1"}, "MaxCount": {"1"}, "SubnetId": {subnetID},
	})
	instanceID := xmlField(t, runResp, "instanceId")

	resp := ec2GoldenCall(t, srv, "DescribeInstances", url.Values{"InstanceId.1": {instanceID}})
	helpers.GoldenTest(t, ec2GoldenDir, "DescribeInstances", resp, normalizeEC2IDs)
}

func TestGolden_TerminateInstances(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	created := ec2GoldenCall(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.100.0.0/16"}})
	vpcID := xmlField(t, created, "vpcId")
	subResp := ec2GoldenCall(t, srv, "CreateSubnet", url.Values{
		"VpcId": {vpcID}, "CidrBlock": {"10.100.1.0/24"}, "AvailabilityZone": {"us-east-1a"},
	})
	subnetID := xmlField(t, subResp, "subnetId")
	runResp := ec2GoldenCall(t, srv, "RunInstances", url.Values{
		"ImageId": {"ami-golden12345"}, "InstanceType": {"t3.micro"},
		"MinCount": {"1"}, "MaxCount": {"1"}, "SubnetId": {subnetID},
	})
	instanceID := xmlField(t, runResp, "instanceId")

	resp := ec2GoldenCall(t, srv, "TerminateInstances", url.Values{"InstanceId.1": {instanceID}})
	helpers.GoldenTest(t, ec2GoldenDir, "TerminateInstances", resp, normalizeEC2IDs)
}

// TestGolden_CreateSubnet_invalidVpc pins the InvalidVpcID.NotFound fault —
// the error-shape golden for this service. CreateSubnet is answered by the
// typed registry as of #754 wave 2 (typed_dispatch.go); this test still
// drives it through the same ec2GoldenCall as every other golden here
// (real HTTP, real dispatch), so it exercises whichever path DispatchQuery
// currently routes to rather than pinning one deliberately.
func TestGolden_CreateSubnet_invalidVpc(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := ec2GoldenCall(t, srv, "CreateSubnet", url.Values{
		"VpcId": {"vpc-ghost0000"}, "CidrBlock": {"10.100.1.0/24"},
	})
	helpers.GoldenTest(t, ec2GoldenDir, "CreateSubnet_invalidVpc", resp, normalizeEC2IDs)
}
