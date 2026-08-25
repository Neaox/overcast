package serviceutil_test

import (
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/serviceutil"
)

// TestAccountRegionalBucketSuffix pins the suffix grammar AWS documents for
// account regional namespace buckets: "-<accountId>-<region>-an". CFN's
// BucketNamePrefix appends exactly this, and CreateBucket's full-name
// validation checks for exactly this — both go through this one function so
// the format cannot drift between the two callers.
// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html#account-regional-naming-rules
func TestAccountRegionalBucketSuffix(t *testing.T) {
	got := serviceutil.AccountRegionalBucketSuffix("111122223333", "us-west-2")
	want := "-111122223333-us-west-2-an"
	if got != want {
		t.Errorf("AccountRegionalBucketSuffix() = %q, want %q", got, want)
	}
}

func TestHasAccountRegionalBucketSuffix(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"amzn-s3-demo-bucket-111122223333-us-west-2-an", true},
		{"my-bucket", false},
		{"my-bucketan", false}, // ends in the letters "an", but not the "-an" separator the rule reserves
		{"bucket-suffixed-an", true},
		{"an", false}, // too short to carry the "-an" suffix at all
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceutil.HasAccountRegionalBucketSuffix(tc.name); got != tc.want {
				t.Errorf("HasAccountRegionalBucketSuffix(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestValidateAccountRegionalBucketName is the specification for the full
// account-regional CreateBucket name check: base bucket-naming rules plus the
// required "-<accountId>-<region>-an" suffix.
//
// The exact error CODE for the mismatch cases (wrong account, wrong region,
// no matching suffix at all) is unverified against real AWS — see the
// CreateBucket comment this helper backs. Modeled here as the same
// InvalidBucketName shape the base rules already use, since AWS does not
// appear to carve out a distinct code for this newly introduced rule.
func TestValidateAccountRegionalBucketName(t *testing.T) {
	const accountID = "000000000000"
	const region = "us-east-1"

	cases := []struct {
		name  string
		valid bool
		why   string
	}{
		{name: "amzn-app-000000000000-us-east-1-an", valid: true, why: "well-formed account-regional name"},
		{name: "a-000000000000-us-east-1-an", valid: true, why: "single-character prefix is enough"},

		{name: "amzn-app-111122223333-us-east-1-an", why: "suffix names a different account"},
		{name: "amzn-app-000000000000-eu-west-1-an", why: "suffix names a different region"},
		{name: "amzn-app", why: "no account-regional suffix at all"},
		{name: "amzn-app-000000000000-us-east-1", why: "missing the -an suffix"},
		{name: "-000000000000-us-east-1-an", why: "empty prefix — name would begin with a hyphen, which base naming rules already reject"},
		{name: "AB-000000000000-us-east-1-an", why: "uppercase — base naming rules already reject"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := serviceutil.ValidateAccountRegionalBucketName(tc.name, accountID, region)
			if tc.valid && err != nil {
				t.Errorf("ValidateAccountRegionalBucketName(%q) rejected a name it should accept (%s): %v", tc.name, tc.why, err.Message)
			}
			if !tc.valid && err == nil {
				t.Errorf("ValidateAccountRegionalBucketName(%q) accepted a name it should reject (%s)", tc.name, tc.why)
			}
			if !tc.valid && err != nil && err.Code != "InvalidBucketName" {
				t.Errorf("ValidateAccountRegionalBucketName(%q) error code = %q, want InvalidBucketName", tc.name, err.Code)
			}
		})
	}
}

// TestValidateAccountRegionalBucketName_lengthBoundary pins the 63-char
// arithmetic the issue calls out: a 26-char suffix
// ("-012345678910-us-east-1-an") leaves 37 characters for the prefix.
func TestValidateAccountRegionalBucketName_lengthBoundary(t *testing.T) {
	const accountID = "012345678910"
	const region = "us-east-1"
	suffix := serviceutil.AccountRegionalBucketSuffix(accountID, region)
	if len(suffix) != 26 {
		t.Fatalf("suffix %q length = %d, want 26 (per issue #1471)", suffix, len(suffix))
	}

	// 37-char prefix + 26-char suffix = 63, the maximum.
	maxPrefix := strings.Repeat("a", 37)
	if err := serviceutil.ValidateAccountRegionalBucketName(maxPrefix+suffix, accountID, region); err != nil {
		t.Errorf("63-char name at the boundary was rejected: %v", err.Message)
	}

	// 38-char prefix + 26-char suffix = 64, one over.
	tooLongPrefix := strings.Repeat("a", 38)
	if err := serviceutil.ValidateAccountRegionalBucketName(tooLongPrefix+suffix, accountID, region); err == nil {
		t.Error("64-char name was accepted, want rejected (over the 63-char maximum)")
	}
}
