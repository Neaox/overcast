package organizations

// aws_id_pattern_test.go pins every identifier and ARN this service mints
// against the AWS model's own patterns — the fix for #1736
// (organization ID o-overcast violates the AWS ID pattern, blocking the #1113
// G2 pilot's ARN-shape assertions).
//
// The regexes below are copied verbatim from models/aws/shapes/organizations.json
// (not read from the file at test time — see inert_policy.go's header comment
// on why a runtime reference to the model snapshot is refused) so a change to
// either drifts visibly instead of silently.

import (
	"regexp"
	"testing"
)

// OrganizationId, models/aws/shapes/organizations.json.
var organizationIDPattern = regexp.MustCompile(`^o-[a-z0-9]{10,32}$`)

// OrganizationArn, models/aws/shapes/organizations.json.
var organizationArnPattern = regexp.MustCompile(`^arn:aws:organizations::\d{12}:organization\/o-[a-z0-9]{10,32}$`)

// AccountArn, models/aws/shapes/organizations.json.
var accountArnPattern = regexp.MustCompile(`^arn:aws:organizations::\d{12}:account\/o-[a-z0-9]{10,32}\/\d{12}$`)

// PolicyId, models/aws/shapes/organizations.json.
var policyIDPattern = regexp.MustCompile(`^p-[0-9a-zA-Z_]{8,128}$`)

// PolicyArn, models/aws/shapes/organizations.json — the org-scoped
// alternative (the second alternative is for AWS-managed policies, which this
// service never mints since AwsManaged is always false here).
var policyArnPattern = regexp.MustCompile(`^(arn:aws:organizations::\d{12}:policy\/o-[a-z0-9]{10,32}\/[0-9a-z_]+\/p-[0-9a-z]{10,32})|(arn:aws:organizations::aws:policy\/[0-9a-z_]+\/p-[0-9a-zA-Z_]{10,128})$`)

// RootId, models/aws/shapes/organizations.json.
var rootIDPattern = regexp.MustCompile(`^r-[0-9a-z]{4,32}$`)

// RootArn, models/aws/shapes/organizations.json.
var rootArnPattern = regexp.MustCompile(`^arn:aws:organizations::\d{12}:root\/o-[a-z0-9]{10,32}\/r-[0-9a-z]{4,32}$`)

// OrganizationalUnitId, models/aws/shapes/organizations.json.
var organizationalUnitIDPattern = regexp.MustCompile(`^ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}$`)

// OrganizationalUnitArn, models/aws/shapes/organizations.json.
var organizationalUnitArnPattern = regexp.MustCompile(`^arn:aws:organizations::\d{12}:ou\/o-[a-z0-9]{10,32}\/ou-[0-9a-z]{4,32}-[0-9a-z]{8,32}$`)

// Path, models/aws/shapes/organizations.json — the OrganizationalUnit member
// naming a unit's position in the tree: the organization, the root, one
// segment per ancestor unit, and a trailing slash.
var pathPattern = regexp.MustCompile(`^(o-[a-z0-9]{10,32}\/r-[0-9a-z]{4,32}(\/ou\-[0-9a-z]{4,32}-[a-z0-9]{8,32})*(\/\d{12})*)\/$`)

// TestOrganizationID_MatchesTheModeledPattern is the direct regression test
// for #1736: the eight-character "o-overcast" constant violated
// OrganizationId's `{10,32}` lower bound. AWS issues ten characters after
// `o-`, so the derivation must produce exactly that shape for any account ID.
func TestOrganizationID_MatchesTheModeledPattern(t *testing.T) {
	for _, accountID := range []string{"000000000000", "111111111111", "123456789012"} {
		id := organizationID(accountID)
		if !organizationIDPattern.MatchString(id) {
			t.Fatalf("organizationID(%q) = %q, want a match for %s", accountID, id, organizationIDPattern.String())
		}
	}
}

// TestOrganizationID_IsStablePerAccount holds the issue's other requirement:
// the organization ID is the root of every Organizations ARN this service
// mints, so it must not be regenerated per process. Deriving it
// deterministically from the account ID (rather than randomly, or from a
// counter) is what makes two independently constructed services — or the
// same service restarted — agree on it without persisting a second piece of
// organization metadata.
func TestOrganizationID_IsStablePerAccount(t *testing.T) {
	first := organizationID("123456789012")
	second := organizationID("123456789012")
	if first != second {
		t.Fatalf("organizationID is not deterministic: %q then %q for the same account", first, second)
	}

	other := organizationID("210987654321")
	if first == other {
		t.Fatalf("organizationID(%q) and organizationID(%q) collided: %q", "123456789012", "210987654321", first)
	}
}

// TestDescribeOrganization_MintsAWSShapedIdentifiers exercises the real
// dispatch path (JSON 1.1) and asserts the Id and Arn it returns against the
// model, rather than only the derivation helper in isolation.
func TestDescribeOrganization_MintsAWSShapedIdentifiers(t *testing.T) {
	s := newTestService(t)
	body := dispatchJSON(t, s, "DescribeOrganization", map[string]any{})
	org, ok := body["Organization"].(map[string]any)
	if !ok {
		t.Fatalf("DescribeOrganization returned %v, want an Organization", body)
	}

	id, _ := org["Id"].(string)
	if !organizationIDPattern.MatchString(id) {
		t.Fatalf("Organization.Id = %q, want a match for %s", id, organizationIDPattern.String())
	}
	arn, _ := org["Arn"].(string)
	if !organizationArnPattern.MatchString(arn) {
		t.Fatalf("Organization.Arn = %q, want a match for %s", arn, organizationArnPattern.String())
	}
	masterArn, _ := org["MasterAccountArn"].(string)
	if !accountArnPattern.MatchString(masterArn) {
		t.Fatalf("Organization.MasterAccountArn = %q, want a match for %s", masterArn, accountArnPattern.String())
	}
	// The org ID embedded in the account ARN must be the same one reported
	// as Organization.Id — one organization, one identifier, everywhere it
	// appears.
	if want := "/" + id + "/"; !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(masterArn) {
		t.Fatalf("MasterAccountArn %q does not embed Organization.Id %q", masterArn, id)
	}
}

// TestPolicyARN_MatchesTheModeledPattern re-asserts CreatePolicy's minted ARN
// against the model regex directly (inert_policy_test.go pins the literal
// value; this pins the shape so a future change to organizationID or
// policyID that keeps the literal test passing by accident cannot silently
// drift out of the modeled pattern).
func TestPolicyARN_MatchesTheModeledPattern(t *testing.T) {
	s := newTestService(t)
	summary := createTestPolicy(t, s, "pattern-check")

	id, _ := summary["Id"].(string)
	if !policyIDPattern.MatchString(id) {
		t.Fatalf("Policy.Id = %q, want a match for %s", id, policyIDPattern.String())
	}
	arn, _ := summary["Arn"].(string)
	if !policyArnPattern.MatchString(arn) {
		t.Fatalf("Policy.Arn = %q, want a match for %s", arn, policyArnPattern.String())
	}
}

// TestRootIDPattern_MatchesAWSExample and
// TestOrganizationalUnitIDPattern_MatchesAWSExample check the two patterns
// against AWS's own documented example IDs. Both identifiers are minted by
// this service now (#1813), and inert_ou_test.go asserts what it mints
// against these regexes — which is only worth anything if the regexes
// themselves accept a real AWS identifier, so the two documented examples
// stay pinned here.
func TestRootIDPattern_MatchesAWSExample(t *testing.T) {
	if !rootIDPattern.MatchString("r-a1b2") {
		t.Fatalf("rootIDPattern does not match AWS's own example root ID %q", "r-a1b2")
	}
}

func TestOrganizationalUnitIDPattern_MatchesAWSExample(t *testing.T) {
	if !organizationalUnitIDPattern.MatchString("ou-a1b2-f6g7h222") {
		t.Fatalf("organizationalUnitIDPattern does not match AWS's own example OU ID %q", "ou-a1b2-f6g7h222")
	}
}

// TestRootAndOrganizationalUnitIDs_AreDerivedNotMinted holds the property
// every ARN this service builds depends on: both identifiers come from a
// deterministic derivation, so a restart, a second service over the same
// account, or a state export/import all agree on them.
func TestRootAndOrganizationalUnitIDs_AreDerivedNotMinted(t *testing.T) {
	for _, accountID := range []string{"000000000000", "111111111111", "123456789012"} {
		org := organizationID(accountID)
		root := rootID(org)
		if !rootIDPattern.MatchString(root) {
			t.Fatalf("rootID(%q) = %q, want a match for %s", org, root, rootIDPattern.String())
		}
		if root != rootID(org) {
			t.Fatalf("rootID is not deterministic for %q", org)
		}
		ou := organizationalUnitID(root, root, "engineering")
		if !organizationalUnitIDPattern.MatchString(ou) {
			t.Fatalf("organizationalUnitID(...) = %q, want a match for %s", ou, organizationalUnitIDPattern.String())
		}
		if ou != organizationalUnitID(root, root, "engineering") {
			t.Fatalf("organizationalUnitID is not deterministic for %q", org)
		}
		// The same name under two parents is two different units, which is
		// what makes DuplicateOrganizationalUnitException a per-parent
		// constraint rather than a per-organization one.
		if sibling := organizationalUnitID(root, "ou-abcd-12345678", "engineering"); sibling == ou {
			t.Fatalf("the same OU name under two parents derived the same id %q", ou)
		}
	}

	// Two accounts do not share a root.
	if rootID(organizationID("123456789012")) == rootID(organizationID("210987654321")) {
		t.Fatal("two organizations derived the same root id")
	}
}
