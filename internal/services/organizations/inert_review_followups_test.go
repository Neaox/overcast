package organizations

// Coverage for the post-merge review follow-ups on PRs #1376 (policies) and
// #1821 (OU/root): MaxResults' modeled range instead of silent clamping,
// InvalidInputException.Reason, rune-counted (not byte-counted) name length,
// tags applied only after a successful record write, and a no-op update that
// does not rewrite the record.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// errorReason decodes the modeled "Reason" member from a JSON error body —
// the sibling of errorCode (inert_policy_test.go), which decodes "__type".
func errorReason(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Reason string `json:"Reason"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decoding the error envelope %s: %v", body, err)
	}
	return env.Reason
}

// ---- item 1: MaxResults' modeled @range (min 1, max 20) -------------------
//
// Before this fix, out-of-range values were silently clamped by
// serviceutil.Paginate's MaxLimit rather than rejected — AWS answers
// InvalidInputException instead.

func TestListPolicies_MaxResultsOutOfRange(t *testing.T) {
	s := newTestService(t)
	cases := []struct {
		name       string
		maxResults int32
		wantReason string
	}{
		{"above the modeled maximum of 20", 21, "MAX_VALUE_EXCEEDED"},
		{"zero is below the modeled minimum of 1", 0, "MIN_VALUE_EXCEEDED"},
		{"negative", -5, "MIN_VALUE_EXCEEDED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := dispatch(t, s, "ListPolicies", map[string]any{
				"Filter": "SERVICE_CONTROL_POLICY", "MaxResults": tc.maxResults,
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("ListPolicies MaxResults=%d returned %d, want 400", tc.maxResults, rec.Code)
			}
			if code := errorCode(t, rec.Body.Bytes()); code != "InvalidInputException" {
				t.Fatalf("ListPolicies MaxResults=%d returned %q, want InvalidInputException", tc.maxResults, code)
			}
			if reason := errorReason(t, rec.Body.Bytes()); reason != tc.wantReason {
				t.Fatalf("ListPolicies MaxResults=%d Reason = %q, want %q", tc.maxResults, reason, tc.wantReason)
			}
		})
	}
}

func TestListRoots_MaxResultsOutOfRange(t *testing.T) {
	s := newTestService(t)
	for _, tc := range []struct {
		maxResults int32
		wantReason string
	}{
		{21, "MAX_VALUE_EXCEEDED"},
		{0, "MIN_VALUE_EXCEEDED"},
	} {
		rec := dispatch(t, s, "ListRoots", map[string]any{"MaxResults": tc.maxResults})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("ListRoots MaxResults=%d returned %d, want 400", tc.maxResults, rec.Code)
		}
		if reason := errorReason(t, rec.Body.Bytes()); reason != tc.wantReason {
			t.Fatalf("ListRoots MaxResults=%d Reason = %q, want %q", tc.maxResults, reason, tc.wantReason)
		}
	}
}

func TestListOrganizationalUnitsForParent_MaxResultsOutOfRange(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)
	for _, tc := range []struct {
		maxResults int32
		wantReason string
	}{
		{21, "MAX_VALUE_EXCEEDED"},
		{0, "MIN_VALUE_EXCEEDED"},
	} {
		rec := dispatch(t, s, "ListOrganizationalUnitsForParent", map[string]any{
			"ParentId": rootID, "MaxResults": tc.maxResults,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("ListOrganizationalUnitsForParent MaxResults=%d returned %d, want 400", tc.maxResults, rec.Code)
		}
		if reason := errorReason(t, rec.Body.Bytes()); reason != tc.wantReason {
			t.Fatalf("ListOrganizationalUnitsForParent MaxResults=%d Reason = %q, want %q", tc.maxResults, reason, tc.wantReason)
		}
	}
}

// TestListPolicies_MaxResultsBoundariesAreAccepted pins the boundary values
// themselves (1 and 20) so the range check cannot be off by one in either
// direction.
func TestListPolicies_MaxResultsBoundariesAreAccepted(t *testing.T) {
	s := newTestService(t)
	createTestPolicy(t, s, "boundary-policy")
	for _, maxResults := range []int32{1, 20} {
		rec := dispatch(t, s, "ListPolicies", map[string]any{
			"Filter": "SERVICE_CONTROL_POLICY", "MaxResults": maxResults,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("ListPolicies MaxResults=%d returned %d, want 200: %s", maxResults, rec.Code, rec.Body.String())
		}
	}
}

// ---- item 2: InvalidInputException.Reason ----------------------------------

func TestCreatePolicy_ReasonsForMissingRequiredMembers(t *testing.T) {
	s := newTestService(t)
	base := map[string]any{
		"Name": "reasons", "Description": "d",
		"Content": `{"Version":"2012-10-17","Statement":[]}`, "Type": "SERVICE_CONTROL_POLICY",
	}
	cases := []struct {
		missing string
	}{{"Name"}, {"Content"}, {"Description"}}
	for _, tc := range cases {
		fields := map[string]any{}
		for k, v := range base {
			if k != tc.missing {
				fields[k] = v
			}
		}
		rec := dispatch(t, s, "CreatePolicy", fields)
		if reason := errorReason(t, rec.Body.Bytes()); reason != "INPUT_REQUIRED" {
			t.Fatalf("CreatePolicy missing %s: Reason = %q, want INPUT_REQUIRED", tc.missing, reason)
		}
	}
}

func TestCreatePolicy_InvalidTypeReasonIsEnumSpecific(t *testing.T) {
	s := newTestService(t)
	rec := dispatch(t, s, "CreatePolicy", map[string]any{
		"Name": "bad-type", "Description": "d",
		"Content": `{"Version":"2012-10-17","Statement":[]}`, "Type": "NOT_A_REAL_POLICY_TYPE",
	})
	if reason := errorReason(t, rec.Body.Bytes()); reason != "INVALID_ENUM_POLICY_TYPE" {
		t.Fatalf("CreatePolicy invalid Type: Reason = %q, want INVALID_ENUM_POLICY_TYPE", reason)
	}
}

func TestListPolicies_FilterReasonsDistinguishMissingFromInvalid(t *testing.T) {
	s := newTestService(t)

	missing := dispatch(t, s, "ListPolicies", map[string]any{})
	if reason := errorReason(t, missing.Body.Bytes()); reason != "INPUT_REQUIRED" {
		t.Fatalf("ListPolicies with no Filter: Reason = %q, want INPUT_REQUIRED", reason)
	}

	invalid := dispatch(t, s, "ListPolicies", map[string]any{"Filter": "NOT_A_REAL_POLICY_TYPE"})
	if reason := errorReason(t, invalid.Body.Bytes()); reason != "INVALID_ENUM_POLICY_TYPE" {
		t.Fatalf("ListPolicies with an invalid Filter: Reason = %q, want INVALID_ENUM_POLICY_TYPE", reason)
	}
}

// TestGarbageNextToken_ReasonIsInvalidNextToken pins the wire value of the
// InvalidInputExceptionReason enum's INVALID_PAGINATION_TOKEN member — its
// Go-side name and its @enumValue differ, and INVALID_NEXT_TOKEN is what
// actually goes on the wire — across every paginated operation.
func TestGarbageNextToken_ReasonIsInvalidNextToken(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)

	cases := []struct {
		op     string
		fields map[string]any
	}{
		{"ListPolicies", map[string]any{"Filter": "SERVICE_CONTROL_POLICY", "NextToken": "not-a-real-token"}},
		{"ListRoots", map[string]any{"NextToken": "not-a-real-token"}},
		{"ListOrganizationalUnitsForParent", map[string]any{"ParentId": rootID, "NextToken": "not-a-real-token"}},
		{"ListTagsForResource", map[string]any{"ResourceId": rootID, "NextToken": "not-a-real-token"}},
	}
	for _, tc := range cases {
		rec := dispatch(t, s, tc.op, tc.fields)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s with a garbage NextToken returned %d, want 400", tc.op, rec.Code)
		}
		if reason := errorReason(t, rec.Body.Bytes()); reason != "INVALID_NEXT_TOKEN" {
			t.Fatalf("%s with a garbage NextToken: Reason = %q, want INVALID_NEXT_TOKEN", tc.op, reason)
		}
	}
}

func TestUpdatePolicy_EmptyOptionalMembersReportMinLengthExceeded(t *testing.T) {
	s := newTestService(t)
	summary := createTestPolicy(t, s, "shrinkable")
	id, _ := summary["Id"].(string)

	for _, field := range []string{"Name", "Content"} {
		rec := dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, field: ""})
		if reason := errorReason(t, rec.Body.Bytes()); reason != "MIN_LENGTH_EXCEEDED" {
			t.Fatalf("UpdatePolicy empty %s: Reason = %q, want MIN_LENGTH_EXCEEDED", field, reason)
		}
	}
}

func TestUpdateOrganizationalUnit_EmptyNameReportsMinLengthExceeded(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)
	ou := createTestOU(t, s, rootID, "shrinkable-ou")
	id, _ := ou["Id"].(string)

	rec := dispatch(t, s, "UpdateOrganizationalUnit", map[string]any{"OrganizationalUnitId": id, "Name": ""})
	if reason := errorReason(t, rec.Body.Bytes()); reason != "MIN_LENGTH_EXCEEDED" {
		t.Fatalf("UpdateOrganizationalUnit empty Name: Reason = %q, want MIN_LENGTH_EXCEEDED", reason)
	}
}

// ---- item 3: @length counts Unicode code points, not bytes -----------------

// TestCreateOrganizationalUnit_NameLengthCountsRunesNotBytes is the case a
// byte-counted len(name) gets wrong: 100 multi-byte characters is well under
// the modeled 128-code-point maximum, but "é" is two bytes in UTF-8, so the
// byte length (200) is not.
func TestCreateOrganizationalUnit_NameLengthCountsRunesNotBytes(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)
	name := strings.Repeat("é", 100)
	if len(name) <= maxOrganizationalUnitName {
		t.Fatalf("test setup: byte length %d must exceed %d for this case to be meaningful", len(name), maxOrganizationalUnitName)
	}

	rec := dispatch(t, s, "CreateOrganizationalUnit", map[string]any{"Name": name, "ParentId": rootID})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateOrganizationalUnit with a %d-rune (%d-byte) name returned %d, want 200: %s",
			100, len(name), rec.Code, rec.Body.String())
	}
}

// TestCreateOrganizationalUnit_NameLengthBoundaryIsRuneExact pins the exact
// modeled boundary (128 code points) in both directions.
func TestCreateOrganizationalUnit_NameLengthBoundaryIsRuneExact(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)

	atLimit := strings.Repeat("é", maxOrganizationalUnitName)
	rec := dispatch(t, s, "CreateOrganizationalUnit", map[string]any{"Name": atLimit, "ParentId": rootID})
	if rec.Code != http.StatusOK {
		t.Fatalf("a %d-rune name returned %d, want 200: %s", maxOrganizationalUnitName, rec.Code, rec.Body.String())
	}

	overLimit := strings.Repeat("é", maxOrganizationalUnitName+1)
	rec = dispatch(t, s, "CreateOrganizationalUnit", map[string]any{"Name": overLimit, "ParentId": rootID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a %d-rune name returned %d, want 400", maxOrganizationalUnitName+1, rec.Code)
	}
	if reason := errorReason(t, rec.Body.Bytes()); reason != "MAX_LENGTH_EXCEEDED" {
		t.Fatalf("over-limit name: Reason = %q, want MAX_LENGTH_EXCEEDED", reason)
	}
}

// ---- item 4: tags are applied only after a successful record write --------

// failSetStore wraps a state.Store and fails every Set call for one
// namespace, simulating a Put failure at the exact point CreatePolicy and
// CreateOrganizationalUnit persist their record.
type failSetStore struct {
	state.Store
	failNamespace string
}

func (f *failSetStore) Set(ctx context.Context, namespace, key, value string) error {
	if namespace == f.failNamespace {
		return errors.New("simulated store failure")
	}
	return f.Store.Set(ctx, namespace, key, value)
}

// TestCreatePolicy_FailedPutLeavesNoOrphanTags is #1376's review follow-up:
// tags used to be written before the record Put, so a failed Put left tags
// behind for a policy that was never created. Applying tags only after a
// successful Put (as the handler now does) means a failed create leaves the
// tag store untouched.
func TestCreatePolicy_FailedPutLeavesNoOrphanTags(t *testing.T) {
	st := &failSetStore{Store: state.NewMemoryStore(), failNamespace: nsPolicies}
	t.Cleanup(func() { _ = st.Store.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clock.NewMock())

	name := "orphan-check"
	rec := dispatch(t, s, "CreatePolicy", map[string]any{
		"Name": name, "Description": "d",
		"Content": `{"Version":"2012-10-17","Statement":[]}`, "Type": "SERVICE_CONTROL_POLICY",
		"Tags": []map[string]string{{"Key": "env", "Value": "dev"}},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("CreatePolicy against a failing store returned %d, want 500: %s", rec.Code, rec.Body.String())
	}

	id := policyID(name)
	arn := s.policyARN(&policyRecord{Id: id, Type: "SERVICE_CONTROL_POLICY"})
	tags, aerr := s.tags.Load(context.Background(), arn)
	if aerr != nil {
		t.Fatalf("loading tags for the never-created policy: %v", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("orphan tags %v exist for a policy whose Put failed", tags)
	}
}

// TestCreateOrganizationalUnit_FailedPutLeavesNoOrphanTags is the OU sibling
// of TestCreatePolicy_FailedPutLeavesNoOrphanTags, for #1821.
func TestCreateOrganizationalUnit_FailedPutLeavesNoOrphanTags(t *testing.T) {
	st := &failSetStore{Store: state.NewMemoryStore(), failNamespace: nsOrganizationalUnits}
	t.Cleanup(func() { _ = st.Store.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clock.NewMock())
	rootID := testRootID(t, s)

	name := "orphan-check-ou"
	rec := dispatch(t, s, "CreateOrganizationalUnit", map[string]any{
		"Name": name, "ParentId": rootID,
		"Tags": []map[string]string{{"Key": "env", "Value": "dev"}},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("CreateOrganizationalUnit against a failing store returned %d, want 500: %s", rec.Code, rec.Body.String())
	}

	id := organizationalUnitID(s.rootID(), rootID, name)
	arn := s.organizationalUnitARN(id)
	tags, aerr := s.tags.Load(context.Background(), arn)
	if aerr != nil {
		t.Fatalf("loading tags for the never-created OU: %v", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("orphan tags %v exist for an OU whose Put failed", tags)
	}
}

// ---- item 5: a no-op update does not rewrite the record --------------------

func TestUpdatePolicy_NoOpDoesNotBumpUpdatedAtOrWrite(t *testing.T) {
	clk := clock.NewMock()
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	clk.Set(fixed)
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clk)

	summary := createTestPolicy(t, s, "no-op-policy")
	id, _ := summary["Id"].(string)
	clk.Add(24 * time.Hour)

	// An UpdatePolicy call that sends no optional member at all.
	if rec := dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id}); rec.Code != http.StatusOK {
		t.Fatalf("no-op UpdatePolicy returned %d: %s", rec.Code, rec.Body.String())
	}
	rec, found, err := s.policies.Get(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("reading back the record: (%v, %v, %v)", rec, found, err)
	}
	if !rec.UpdatedAt.Equal(fixed) {
		t.Fatalf("UpdatedAt = %v after a no-op update, want unchanged %v", rec.UpdatedAt, fixed)
	}

	// Resending the exact current values is also a no-op.
	clk.Add(24 * time.Hour)
	if rec := dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, "Description": "a description"}); rec.Code != http.StatusOK {
		t.Fatalf("resend-current-value UpdatePolicy returned %d: %s", rec.Code, rec.Body.String())
	}
	rec2, _, _ := s.policies.Get(context.Background(), id)
	if !rec2.UpdatedAt.Equal(fixed) {
		t.Fatalf("UpdatedAt = %v after resending the current Description, want unchanged %v", rec2.UpdatedAt, fixed)
	}
}

// ---- item 6: PolicyName/PolicyContent/PolicyDescription's modeled @length
// maxes were never enforced -----------------------------------------------
//
// The audit note createPolicy carried after #1376's review named exactly
// this gap: PolicyName's @length max (128) and PolicyDescription's (512)
// were never checked at all, on either CreatePolicy or UpdatePolicy.
// PolicyContent has no modeled max (@length min: 1 only), so it gets no
// max-length test here — only the boundary tests below for Name and
// Description, following the same rune-counted, boundary-exact pattern as
// TestCreateOrganizationalUnit_NameLengthBoundaryIsRuneExact.

func TestCreatePolicy_NameLengthBoundaryIsRuneExact(t *testing.T) {
	s := newTestService(t)
	base := map[string]any{
		"Description": "d", "Content": `{"Version":"2012-10-17","Statement":[]}`,
		"Type": "SERVICE_CONTROL_POLICY",
	}

	atLimit := strings.Repeat("é", maxPolicyName)
	fields := map[string]any{"Name": atLimit}
	for k, v := range base {
		fields[k] = v
	}
	rec := dispatch(t, s, "CreatePolicy", fields)
	if rec.Code != http.StatusOK {
		t.Fatalf("CreatePolicy with a %d-rune Name returned %d, want 200: %s", maxPolicyName, rec.Code, rec.Body.String())
	}

	overLimit := strings.Repeat("é", maxPolicyName+1)
	fields = map[string]any{"Name": overLimit}
	for k, v := range base {
		fields[k] = v
	}
	rec = dispatch(t, s, "CreatePolicy", fields)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CreatePolicy with a %d-rune Name returned %d, want 400", maxPolicyName+1, rec.Code)
	}
	if reason := errorReason(t, rec.Body.Bytes()); reason != "MAX_LENGTH_EXCEEDED" {
		t.Fatalf("over-limit Name: Reason = %q, want MAX_LENGTH_EXCEEDED", reason)
	}
}

// TestCreatePolicy_NameLengthCountsRunesNotBytes is the policy sibling of
// TestCreateOrganizationalUnit_NameLengthCountsRunesNotBytes: 100 "é"
// characters is 100 code points (under PolicyName's 128 max) but 200 bytes
// (over it), so a byte-counted check would wrongly reject it.
func TestCreatePolicy_NameLengthCountsRunesNotBytes(t *testing.T) {
	s := newTestService(t)
	name := strings.Repeat("é", 100)
	if len(name) <= maxPolicyName {
		t.Fatalf("test setup: byte length %d must exceed %d for this case to be meaningful", len(name), maxPolicyName)
	}

	rec := dispatch(t, s, "CreatePolicy", map[string]any{
		"Name": name, "Description": "d",
		"Content": `{"Version":"2012-10-17","Statement":[]}`, "Type": "SERVICE_CONTROL_POLICY",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreatePolicy with a %d-rune (%d-byte) Name returned %d, want 200: %s",
			100, len(name), rec.Code, rec.Body.String())
	}
}

func TestCreatePolicy_DescriptionLengthBoundaryIsRuneExact(t *testing.T) {
	s := newTestService(t)
	base := map[string]any{
		"Content": `{"Version":"2012-10-17","Statement":[]}`, "Type": "SERVICE_CONTROL_POLICY",
	}

	atLimit := strings.Repeat("é", maxPolicyDescription)
	fields := map[string]any{"Name": "desc-at-limit", "Description": atLimit}
	for k, v := range base {
		fields[k] = v
	}
	rec := dispatch(t, s, "CreatePolicy", fields)
	if rec.Code != http.StatusOK {
		t.Fatalf("CreatePolicy with a %d-rune Description returned %d, want 200: %s", maxPolicyDescription, rec.Code, rec.Body.String())
	}

	overLimit := strings.Repeat("é", maxPolicyDescription+1)
	fields = map[string]any{"Name": "desc-over-limit", "Description": overLimit}
	for k, v := range base {
		fields[k] = v
	}
	rec = dispatch(t, s, "CreatePolicy", fields)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CreatePolicy with a %d-rune Description returned %d, want 400", maxPolicyDescription+1, rec.Code)
	}
	if reason := errorReason(t, rec.Body.Bytes()); reason != "MAX_LENGTH_EXCEEDED" {
		t.Fatalf("over-limit Description: Reason = %q, want MAX_LENGTH_EXCEEDED", reason)
	}
}

// TestCreatePolicy_ContentHasNoMaxLength pins PolicyContent's modeled
// @length as min-only (1, no max): a very long Content value — well past
// PolicyName's and PolicyDescription's maxes — must still be accepted.
func TestCreatePolicy_ContentHasNoMaxLength(t *testing.T) {
	s := newTestService(t)
	longContent := `{"Version":"2012-10-17","Statement":[]}` + strings.Repeat(" ", 4096)
	rec := dispatch(t, s, "CreatePolicy", map[string]any{
		"Name": "long-content", "Description": "d", "Content": longContent, "Type": "SERVICE_CONTROL_POLICY",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreatePolicy with a %d-rune Content returned %d, want 200: %s", utf8.RuneCountInString(longContent), rec.Code, rec.Body.String())
	}
}

func TestUpdatePolicy_NameLengthBoundaryIsRuneExact(t *testing.T) {
	s := newTestService(t)
	summary := createTestPolicy(t, s, "rename-target")
	id, _ := summary["Id"].(string)

	atLimit := strings.Repeat("é", maxPolicyName)
	rec := dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, "Name": atLimit})
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdatePolicy with a %d-rune Name returned %d, want 200: %s", maxPolicyName, rec.Code, rec.Body.String())
	}

	overLimit := strings.Repeat("é", maxPolicyName+1)
	rec = dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, "Name": overLimit})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("UpdatePolicy with a %d-rune Name returned %d, want 400", maxPolicyName+1, rec.Code)
	}
	if reason := errorReason(t, rec.Body.Bytes()); reason != "MAX_LENGTH_EXCEEDED" {
		t.Fatalf("over-limit Name: Reason = %q, want MAX_LENGTH_EXCEEDED", reason)
	}
}

func TestUpdatePolicy_DescriptionLengthBoundaryIsRuneExact(t *testing.T) {
	s := newTestService(t)
	summary := createTestPolicy(t, s, "redescribe-target")
	id, _ := summary["Id"].(string)

	atLimit := strings.Repeat("é", maxPolicyDescription)
	rec := dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, "Description": atLimit})
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdatePolicy with a %d-rune Description returned %d, want 200: %s", maxPolicyDescription, rec.Code, rec.Body.String())
	}

	overLimit := strings.Repeat("é", maxPolicyDescription+1)
	rec = dispatch(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, "Description": overLimit})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("UpdatePolicy with a %d-rune Description returned %d, want 400", maxPolicyDescription+1, rec.Code)
	}
	if reason := errorReason(t, rec.Body.Bytes()); reason != "MAX_LENGTH_EXCEEDED" {
		t.Fatalf("over-limit Description: Reason = %q, want MAX_LENGTH_EXCEEDED", reason)
	}
}

func TestUpdateOrganizationalUnit_NoOpDoesNotBumpUpdatedAtOrWrite(t *testing.T) {
	clk := clock.NewMock()
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	clk.Set(fixed)
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clk)

	rootID := testRootID(t, s)
	ou := createTestOU(t, s, rootID, "no-op-ou")
	id, _ := ou["Id"].(string)
	clk.Add(24 * time.Hour)

	// An UpdateOrganizationalUnit call that sends no Name at all.
	if rec := dispatch(t, s, "UpdateOrganizationalUnit", map[string]any{"OrganizationalUnitId": id}); rec.Code != http.StatusOK {
		t.Fatalf("no-op UpdateOrganizationalUnit returned %d: %s", rec.Code, rec.Body.String())
	}
	rec, found, err := s.ous.Get(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("reading back the record: (%v, %v, %v)", rec, found, err)
	}
	if !rec.UpdatedAt.Equal(fixed) {
		t.Fatalf("UpdatedAt = %v after a no-op update, want unchanged %v", rec.UpdatedAt, fixed)
	}

	// Resending the exact current name is also a no-op.
	clk.Add(24 * time.Hour)
	if rec := dispatch(t, s, "UpdateOrganizationalUnit", map[string]any{"OrganizationalUnitId": id, "Name": "no-op-ou"}); rec.Code != http.StatusOK {
		t.Fatalf("resend-current-name UpdateOrganizationalUnit returned %d: %s", rec.Code, rec.Body.String())
	}
	rec2, _, _ := s.ous.Get(context.Background(), id)
	if !rec2.UpdatedAt.Equal(fixed) {
		t.Fatalf("UpdatedAt = %v after resending the current Name, want unchanged %v", rec2.UpdatedAt, fixed)
	}
}
