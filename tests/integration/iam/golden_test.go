// golden_test.go contains wire-byte golden tests for IAM — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// IAM is Query/XML only (see typed_ops.go's SupportedProtocols — no JSON1.x
// or CBOR variant exists for this service), so there is no cross-protocol
// delegation risk to guard here the way there is for Kinesis or SSM. The
// value of these goldens is different: IAM's handler.go mints a fresh random
// resource ID (RoleId/UserId/PolicyId, via iamID — crypto/rand per
// character) on every CreateRole/CreateUser/CreatePolicy call, including
// every single -record and assert run. A normalize step is therefore load
// bearing here in a way it usually isn't for other services' goldens: get it
// wrong and either recording is flaky (fails on the very next assert run) or
// a real regression in the surrounding XML shape hides behind "well, IDs are
// random anyway." See normalizeIAM below.
//
// A second, more universal source of non-determinism applies to every single
// operation here, not just the ID-minting ones: unlike the JSON1.x protocols
// (where the request ID only ever appears in the x-amzn-requestid response
// header, which GoldenHeaders already excludes from comparison), AWS's
// Query/XML wire format embeds the same per-request UUID inside the body
// itself — see protocol.QueryResponseMetadata's <RequestId> element on
// success responses, and protocol.WriteXMLError's/WriteQueryXMLError's
// <RequestId> on error responses. Every golden fixture in this package,
// success or error, therefore needs that UUID redacted too, or it would fail
// on literally every assert run regardless of whether the operation mints a
// resource ID. normalizeIAM handles both concerns in one pass.
//
// Coverage — the nine highest-value IAM management-plane operations:
//
//	CreateRole, GetRole, ListRoles, CreatePolicy, GetPolicy, AttachRolePolicy,
//	ListAttachedRolePolicies, CreateUser, GetUser
//
// Plus one error-shape golden: GetRole for a role name that doesn't exist,
// pinning the NoSuchEntity fault envelope (store.go's errNoSuchEntity, 404).
//
// Excluded (low golden value or covered elsewhere):
//
//   - Everything access-key related (CreateAccessKey, ListAccessKeys,
//     DeleteAccessKey): CreateAccessKey mints both a random AccessKeyId
//     (iamID("AKIA", 16)) and a random SecretAccessKey (randBase64(30), true
//     crypto/rand bytes, not a redactable fixed-length character-class
//     pattern in the same way) — pinning the wire shape around a secret
//     value that must never be reproducible adds normalization complexity
//     for no realistic regression-catching benefit. Existing behavioral
//     tests (iam_test.go TestCreateAccessKey_success etc.) already assert
//     the fields present.
//   - Groups, instance profiles, inline policies, tagging, policy
//     simulation, service-linked roles: same rationale as kinesis/ssm's
//     "pick the highest-value ops" framing in
//     docs/plans/wire-byte-goldens.md §5.2 — the nine ops above already
//     exercise every response shape family (single-resource create/get,
//     list, attach/no-op-body) that recurs across the rest of the API
//     surface; broader coverage is deferred to a future pass if needed.
//
// Record: go test ./tests/integration/iam/ -run TestGolden -record
// Assert: go test ./tests/integration/iam/ -run TestGolden
package iam_test

import (
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const iamGoldenDir = "goldens"

// IAM mints a fresh crypto/rand ID on every CreateRole/CreateUser/
// CreatePolicy call — see handler.go's iamID(prefix, n), which draws n
// characters from [A-Z0-9] per call. That happens on every -record run and
// on every assert run (assert mode still calls CreateRole etc. to produce
// the response being compared), so the exact 17-character random suffix can
// never match between recordings. Every operation, in addition, embeds a
// random per-request UUID in its own <RequestId> element (see the package
// comment above). normalizeIAM redacts each ID family, plus the request ID,
// to a fixed placeholder before the fixture is written or compared, so the
// golden pins everything else about the shape (tag names, field order,
// surrounding XML) while treating each's value as opaque.
var (
	roleIDRE    = regexp.MustCompile(`AROA[A-Z0-9]{17}`)
	userIDRE    = regexp.MustCompile(`AIDA[A-Z0-9]{17}`)
	policyIDRE  = regexp.MustCompile(`ANPA[A-Z0-9]{17}`)
	requestIDRE = regexp.MustCompile(`<RequestId>[0-9a-f-]{36}</RequestId>`)
)

func normalizeIAM(body []byte) []byte {
	body = roleIDRE.ReplaceAll(body, []byte("AROAREDACTEDREDACTED"))
	body = userIDRE.ReplaceAll(body, []byte("AIDAREDACTEDREDACTED"))
	body = policyIDRE.ReplaceAll(body, []byte("ANPAREDACTEDREDACTED"))
	body = requestIDRE.ReplaceAll(body, []byte("<RequestId>REDACTED-REQUEST-ID</RequestId>"))
	return body
}

func TestGolden_CreateRole(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"golden-role"},
		"AssumeRolePolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
	})
	helpers.GoldenTest(t, iamGoldenDir, "CreateRole", resp, normalizeIAM)
}

func TestGolden_GetRole(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createRole(t, srv, "golden-role")

	resp := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"golden-role"}})
	helpers.GoldenTest(t, iamGoldenDir, "GetRole", resp, normalizeIAM)
}

func TestGolden_GetRole_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"ghost-role"}})
	helpers.GoldenTest(t, iamGoldenDir, "GetRole_notFound", resp, normalizeIAM)
}

func TestGolden_ListRoles(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createRole(t, srv, "golden-role")

	resp := iamCall(t, srv, "ListRoles", nil)
	helpers.GoldenTest(t, iamGoldenDir, "ListRoles", resp, normalizeIAM)
}

func TestGolden_CreatePolicy(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"golden-policy"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	})
	helpers.GoldenTest(t, iamGoldenDir, "CreatePolicy", resp, normalizeIAM)
}

func TestGolden_GetPolicy(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	policyArn := createPolicy(t, srv, "golden-policy")

	resp := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {policyArn}})
	helpers.GoldenTest(t, iamGoldenDir, "GetPolicy", resp, normalizeIAM)
}

// AttachRolePolicy attaches by ARN and never mints a resource ID — the
// policy ARN here is AWS's own fixed, well-known managed-policy string (no
// account ID, no random component: arn:aws:iam::aws:policy/ReadOnlyAccess).
// See attachRolePolicyTyped in typed_logic.go: it only appends req.PolicyArn
// (an ensureAttachedPolicy dedup) and writes an empty-result response — no
// AROA/AIDA/ANPA-style ID field anywhere in the request or response path.
// normalizeIAM is still applied, same as every op in this file, purely to
// redact the per-request <RequestId> UUID (see the package comment).
func TestGolden_AttachRolePolicy(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createRole(t, srv, "golden-role")

	resp := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"golden-role"},
		"PolicyArn": {"arn:aws:iam::aws:policy/ReadOnlyAccess"},
	})
	helpers.GoldenTest(t, iamGoldenDir, "AttachRolePolicy", resp, normalizeIAM)
}

// ListAttachedRolePolicies echoes back the same fixed AWS-managed-policy ARN
// and its PolicyName (both caller-supplied at attach time, not minted by the
// store) — see listAttachedRolePoliciesTyped, which just maps
// role.AttachedPolicies straight to attachedPolicyXML with no resource-ID
// generation. Same caveat as AttachRolePolicy above: normalizeIAM is still
// needed for the <RequestId> UUID, just not for any AROA/AIDA/ANPA ID.
func TestGolden_ListAttachedRolePolicies(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createRole(t, srv, "golden-role")
	ar := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"golden-role"},
		"PolicyArn": {"arn:aws:iam::aws:policy/ReadOnlyAccess"},
	})
	ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	resp := iamCall(t, srv, "ListAttachedRolePolicies", url.Values{"RoleName": {"golden-role"}})
	helpers.GoldenTest(t, iamGoldenDir, "ListAttachedRolePolicies", resp, normalizeIAM)
}

func TestGolden_CreateUser(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := iamCall(t, srv, "CreateUser", url.Values{"UserName": {"golden-user"}})
	helpers.GoldenTest(t, iamGoldenDir, "CreateUser", resp, normalizeIAM)
}

func TestGolden_GetUser(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createUser(t, srv, "golden-user")

	resp := iamCall(t, srv, "GetUser", url.Values{"UserName": {"golden-user"}})
	helpers.GoldenTest(t, iamGoldenDir, "GetUser", resp, normalizeIAM)
}
