package organizations

// Behavioural coverage for the Tier 1 policy surface, over the real
// Dispatch method and the real store.
//
// These are deliberately the *whole* verification for this phase: no compat
// suite groups are added here. §8.1's seven-suite compat gate applies to
// waves (I4 onward), and cmd/compatgen (#1113) is the prerequisite that makes
// per-operation compat coverage affordable — hand-writing seven suite groups
// for a pilot that exists to prove an API shape would cost more than the
// pilot. That is a decision on the record, not an oversight.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

func createTestPolicy(t *testing.T, s *Service, name string) map[string]any {
	t.Helper()
	body := dispatchJSON(t, s, "CreatePolicy", map[string]any{
		"Name":        name,
		"Description": "a description",
		"Content":     `{"Version":"2012-10-17","Statement":[]}`,
		"Type":        "SERVICE_CONTROL_POLICY",
	})
	policy, ok := body["Policy"].(map[string]any)
	if !ok {
		t.Fatalf("CreatePolicy returned %v, want a Policy", body)
	}
	summary, ok := policy["PolicySummary"].(map[string]any)
	if !ok {
		t.Fatalf("CreatePolicy returned %v, want a PolicySummary", policy)
	}
	return summary
}

// TestCreatePolicy_DerivesTheModeledIdentifierAndARN pins the two §3.5
// derivations against the shapes the model declares for them: PolicyId's
// `^p-[0-9a-zA-Z_]{8,128}$` and PolicyArn's
// `arn:aws:organizations::{account}:policy/{org}/{type}/{id}`.
func TestCreatePolicy_DerivesTheModeledIdentifierAndARN(t *testing.T) {
	s := newTestService(t)
	summary := createTestPolicy(t, s, "guardrails")

	id, _ := summary["Id"].(string)
	if !strings.HasPrefix(id, "p-") || len(id) < 10 {
		t.Fatalf("Id = %q, want a p- prefixed identifier matching the modeled pattern", id)
	}
	wantARN := "arn:aws:organizations::000000000000:policy/o-overcast/service_control_policy/" + id
	if got, _ := summary["Arn"].(string); got != wantARN {
		t.Fatalf("Arn = %q, want %q", got, wantARN)
	}
	// Organizations is a global service, so the ARN's region field is empty.
	if strings.Contains(strings.TrimPrefix(wantARN, "arn:aws:organizations::"), "us-east-1") {
		t.Fatalf("Arn %q carries a region — Organizations is global (§3.5)", wantARN)
	}
}

// TestPolicyTimestampsComeFromTheClock is §3.5's timestamp rule for this
// resource. The conformance suite's clause has no modeled member to look at
// (Organizations declares none), so the rule is held here instead, against
// the persisted record.
func TestPolicyTimestampsComeFromTheClock(t *testing.T) {
	// Given: a service whose clock is frozen at a known instant.
	clk := clock.NewMock()
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk.Set(fixed)
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clk)

	// When: a policy is created, then updated 72h later on that clock.
	summary := createTestPolicy(t, s, "clocked")
	id, _ := summary["Id"].(string)
	clk.Add(72 * time.Hour)
	if body := dispatchJSON(t, s, "UpdatePolicy", map[string]any{"PolicyId": id, "Description": "moved on"}); body["Policy"] == nil {
		t.Fatalf("UpdatePolicy returned %v", body)
	}

	// Then: both timestamps track the injected clock, not wall time.
	rec, found, err := s.policies.Get(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("reading back the record: (%v, %v, %v)", rec, found, err)
	}
	if !rec.CreatedAt.Equal(fixed) {
		t.Fatalf("CreatedAt = %v, want the injected %v — the handler is reading time.Now()", rec.CreatedAt, fixed)
	}
	if !rec.UpdatedAt.Equal(fixed.Add(72 * time.Hour)) {
		t.Fatalf("UpdatedAt = %v, want %v", rec.UpdatedAt, fixed.Add(72*time.Hour))
	}
}

// TestCreatePolicyTags_AreVisibleToListTagsForResource is §3.1's Tag class
// and §7.3's "two stores for one resource is the failure mode to design
// against": tags arriving on the create input and tags arriving through
// TagResource have to be the same tags.
func TestCreatePolicyTags_AreVisibleToListTagsForResource(t *testing.T) {
	// Given: a policy created with one tag.
	s := newTestService(t)
	body := dispatchJSON(t, s, "CreatePolicy", map[string]any{
		"Name":        "tagged",
		"Description": "a description",
		"Content":     `{"Version":"2012-10-17","Statement":[]}`,
		"Type":        "SERVICE_CONTROL_POLICY",
		"Tags":        []map[string]string{{"Key": "env", "Value": "dev"}},
	})
	id := body["Policy"].(map[string]any)["PolicySummary"].(map[string]any)["Id"].(string)

	// When: a second tag is added through TagResource and the set is listed.
	if rec := dispatch(t, s, "TagResource", map[string]any{
		"ResourceId": id,
		"Tags":       []map[string]string{{"Key": "team", "Value": "platform"}},
	}); rec.Code != http.StatusOK {
		t.Fatalf("TagResource returned %d: %s", rec.Code, rec.Body.String())
	}
	listed := dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": id})

	// Then: both tags come back, ordered by key.
	got := tagsFromResponse(t, listed)
	if len(got) != 2 || got[0].Key != "env" || got[1].Key != "team" {
		t.Fatalf("ListTagsForResource = %+v, want env then team", got)
	}

	// And: UntagResource takes only the key it names.
	if rec := dispatch(t, s, "UntagResource", map[string]any{"ResourceId": id, "TagKeys": []string{"env"}}); rec.Code != http.StatusOK {
		t.Fatalf("UntagResource returned %d: %s", rec.Code, rec.Body.String())
	}
	got = tagsFromResponse(t, dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": id}))
	if len(got) != 1 || got[0].Key != "team" {
		t.Fatalf("ListTagsForResource after UntagResource = %+v, want just team", got)
	}
}

// TestDeletePolicy_TakesItsTagsWithIt: namespaced tags have nothing tying
// them to the record's lifetime, so a delete path that forgets them keeps
// answering ListTagsForResource for a resource that is gone.
func TestDeletePolicy_TakesItsTagsWithIt(t *testing.T) {
	s := newTestService(t)
	body := dispatchJSON(t, s, "CreatePolicy", map[string]any{
		"Name":        "short-lived",
		"Description": "a description",
		"Content":     `{"Version":"2012-10-17","Statement":[]}`,
		"Type":        "SERVICE_CONTROL_POLICY",
		"Tags":        []map[string]string{{"Key": "env", "Value": "dev"}},
	})
	summary := body["Policy"].(map[string]any)["PolicySummary"].(map[string]any)
	id, arn := summary["Id"].(string), summary["Arn"].(string)

	if rec := dispatch(t, s, "DeletePolicy", map[string]any{"PolicyId": id}); rec.Code != http.StatusOK {
		t.Fatalf("DeletePolicy returned %d: %s", rec.Code, rec.Body.String())
	}

	tags, aerr := s.tags.Load(context.Background(), arn)
	if aerr != nil {
		t.Fatalf("loading tags: %v", aerr)
	}
	if len(tags) != 0 {
		t.Fatalf("tags survived the policy: %v", tags)
	}
}

// TestTagOperations_RejectAnUnknownTarget: only policies are Tier 1 here, so
// a root, OU or account ID is genuinely unknown to this emulator. Reporting
// success for tagging something that does not exist is the §3.6 failure mode
// in a different costume.
func TestTagOperations_RejectAnUnknownTarget(t *testing.T) {
	s := newTestService(t)
	for _, resourceID := range []string{"r-abcd", "ou-abcd-11111111", "123456789012", "p-deadbeefdead"} {
		rec := dispatch(t, s, "ListTagsForResource", map[string]any{"ResourceId": resourceID})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("ListTagsForResource(%q) returned %d, want 404", resourceID, rec.Code)
		}
		if code := errorCode(t, rec.Body.Bytes()); code != "TargetNotFoundException" {
			t.Fatalf("ListTagsForResource(%q) returned %q, want TargetNotFoundException", resourceID, code)
		}
	}
}

// TestListPolicies_RequiresFilter covers the @required member the
// conformance fixture supplies on the caller's behalf, so the check is not
// left unexercised.
func TestListPolicies_RequiresFilter(t *testing.T) {
	s := newTestService(t)
	rec := dispatch(t, s, "ListPolicies", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ListPolicies without Filter returned %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "InvalidInputException" {
		t.Fatalf("ListPolicies without Filter returned %q, want InvalidInputException", code)
	}
}

// TestListPolicies_FiltersByType: Filter is not decoration — a policy of
// another type must not appear.
func TestListPolicies_FiltersByType(t *testing.T) {
	s := newTestService(t)
	createTestPolicy(t, s, "scp-one")
	if rec := dispatch(t, s, "CreatePolicy", map[string]any{
		"Name":        "tag-one",
		"Description": "a description",
		"Content":     `{"tags":{}}`,
		"Type":        "TAG_POLICY",
	}); rec.Code != http.StatusOK {
		t.Fatalf("CreatePolicy(TAG_POLICY) returned %d: %s", rec.Code, rec.Body.String())
	}

	body := dispatchJSON(t, s, "ListPolicies", map[string]any{"Filter": "TAG_POLICY"})
	policies, _ := body["Policies"].([]any)
	if len(policies) != 1 {
		t.Fatalf("ListPolicies(TAG_POLICY) returned %d policies, want 1: %v", len(policies), body)
	}
	if name := policies[0].(map[string]any)["Name"]; name != "tag-one" {
		t.Fatalf("ListPolicies(TAG_POLICY) returned %v, want tag-one", name)
	}
}

// TestMalformedRecord_ReadsAsNotFoundNotAsAnError: a corrupt persisted blob
// is isolated, never escalated — AGENTS.md § "Malformed persisted state must
// be isolated". The whole point is that the *other* records keep working.
func TestMalformedRecord_ReadsAsNotFoundNotAsAnError(t *testing.T) {
	// Given: one healthy policy and one corrupt record in the same namespace.
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clock.NewMock())
	healthy := createTestPolicy(t, s, "healthy")
	if err := st.Set(context.Background(), nsPolicies, "p-corrupted00", "{not json"); err != nil {
		t.Fatalf("seeding a corrupt record: %v", err)
	}

	// When: the corrupt record is described.
	rec := dispatch(t, s, "DescribePolicy", map[string]any{"PolicyId": "p-corrupted00"})

	// Then: it is a modeled not-found, not a 500.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DescribePolicy of a corrupt record returned %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "PolicyNotFoundException" {
		t.Fatalf("DescribePolicy of a corrupt record returned %q, want PolicyNotFoundException", code)
	}

	// And: the healthy policy is still listed.
	body := dispatchJSON(t, s, "ListPolicies", map[string]any{"Filter": "SERVICE_CONTROL_POLICY"})
	policies, _ := body["Policies"].([]any)
	if len(policies) != 1 || policies[0].(map[string]any)["Id"] != healthy["Id"] {
		t.Fatalf("ListPolicies alongside a corrupt record = %v, want just the healthy policy", body)
	}
}

// TestUpdatePolicy_KeepsTheARNStableAcrossARename: the ARN is derived from
// the immutable type and identifier, matching AWS, where a policy's ARN is
// stable for its life.
func TestUpdatePolicy_KeepsTheARNStableAcrossARename(t *testing.T) {
	s := newTestService(t)
	before := createTestPolicy(t, s, "original-name")

	body := dispatchJSON(t, s, "UpdatePolicy", map[string]any{
		"PolicyId": before["Id"],
		"Name":     "renamed",
	})
	after := body["Policy"].(map[string]any)["PolicySummary"].(map[string]any)

	if after["Name"] != "renamed" {
		t.Fatalf("Name = %v, want renamed", after["Name"])
	}
	if after["Arn"] != before["Arn"] {
		t.Fatalf("Arn moved on rename: %v then %v", before["Arn"], after["Arn"])
	}
	if after["Id"] != before["Id"] {
		t.Fatalf("Id moved on rename: %v then %v", before["Id"], after["Id"])
	}
}

type wireTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsFromResponse(t *testing.T, body map[string]any) []wireTag {
	t.Helper()
	raw, err := json.Marshal(body["Tags"])
	if err != nil {
		t.Fatalf("re-marshalling Tags: %v", err)
	}
	var out []wireTag
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding Tags from %s: %v", raw, err)
	}
	return out
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Type string `json:"__type"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decoding the error envelope %s: %v", body, err)
	}
	return env.Type
}
