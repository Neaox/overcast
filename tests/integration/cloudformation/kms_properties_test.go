package cloudformation_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// #525 — properties CDK sends on AWS::KMS::Key that CloudFormation parsed
// into nothing: PendingWindowInDays (dropped on Delete, hardcoded to 7
// instead), Tags (never forwarded to CreateKey at all), and Origin /
// MultiRegion / EnableKeyRotation (silently accepted and ignored instead of
// failing loudly for values the emulator cannot honour).

func kmsPropsKeyTemplate(extraProps string) string {
	props := `"KeySpec":"SYMMETRIC_DEFAULT"`
	if extraProps != "" {
		props += "," + extraProps
	}
	return fmt.Sprintf(`{"Resources":{"Key":{"Type":"AWS::KMS::Key","Properties":{%s}}}}`, props)
}

func kmsDescribeKey(t *testing.T, srv *helpers.TestServer, keyID string) (enabled bool, deletionDate float64, keyState string) {
	t.Helper()
	resp := kmsJSONCall(t, srv, "DescribeKey", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		KeyMetadata struct {
			Enabled      bool    `json:"Enabled"`
			DeletionDate float64 `json:"DeletionDate"`
			KeyState     string  `json:"KeyState"`
		} `json:"KeyMetadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode DescribeKey: %v", err)
	}
	return out.KeyMetadata.Enabled, out.KeyMetadata.DeletionDate, out.KeyMetadata.KeyState
}

func kmsListResourceTags(t *testing.T, srv *helpers.TestServer, keyID string) map[string]string {
	t.Helper()
	resp := kmsJSONCall(t, srv, "ListResourceTags", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags []struct {
			TagKey   string `json:"TagKey"`
			TagValue string `json:"TagValue"`
		} `json:"Tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode ListResourceTags: %v", err)
	}
	out2 := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		out2[tag.TagKey] = tag.TagValue
	}
	return out2
}

// A CDK key with `pendingWindow: Duration.days(14)` must actually get a
// 14-day window on stack deletion. The pre-fix handler hardcoded
// PendingWindowInDays to 7 regardless of what the template asked for.
// 14 is deliberately neither that 7 nor the 30-day default, and unlike the 5
// this test first used it is inside the range KMS accepts (7-30, per
// https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html)
// — real CloudFormation could never have deleted a stack asking for 5.
func TestDeleteStack_KMSKeyHonoursPendingWindowInDays(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	stackName := "kms-pending-window"
	template := kmsPropsKeyTemplate(`"PendingWindowInDays":14`)

	create := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	keyID := kmsKeyPhysicalID(t, srv, stackName)

	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")

	_, deletionDate, keyState := kmsDescribeKey(t, srv, keyID)
	if keyState != "PendingDeletion" {
		t.Fatalf("KeyState = %q, want PendingDeletion", keyState)
	}
	want := float64(srv.Clock.Now().Add(14*24*time.Hour).UnixMilli()) / 1000.0
	if deletionDate != want {
		t.Fatalf("DeletionDate = %v, want %v (14-day PendingWindowInDays honoured, not the hardcoded 7)", deletionDate, want)
	}
}

// A CDK key with no explicit pendingWindow must fall back to the documented
// KMS default of 30 days on deletion, matching real AWS — not the emulator's
// own internal 7-day compensating-cleanup value.
func TestDeleteStack_KMSKeyDefaultsPendingWindowTo30Days(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	stackName := "kms-pending-window-default"
	template := kmsPropsKeyTemplate("")

	create := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	keyID := kmsKeyPhysicalID(t, srv, stackName)

	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")

	_, deletionDate, _ := kmsDescribeKey(t, srv, keyID)
	want := float64(srv.Clock.Now().Add(30*24*time.Hour).UnixMilli()) / 1000.0
	if deletionDate != want {
		t.Fatalf("DeletionDate = %v, want %v (AWS's documented 30-day default)", deletionDate, want)
	}
}

// A CDK key built with `Tags.of(key).add(...)` (or a template Tags property)
// must round-trip through ListResourceTags — CreateKey silently dropped Tags
// entirely before this fix.
func TestCreateStack_KMSKeyTagsRoundTripThroughListResourceTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "kms-tags-create"
	template := kmsPropsKeyTemplate(`"Tags":[{"Key":"team","Value":"platform"}]`)

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {template},
		"Tags.member.1.Key": {"env"}, "Tags.member.1.Value": {"dev"},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	keyID := kmsKeyPhysicalID(t, srv, stackName)

	tags := kmsListResourceTags(t, srv, keyID)
	if tags["team"] != "platform" || tags["env"] != "dev" {
		t.Fatalf("ListResourceTags = %v, want team=platform and env=dev (stack tag merged in)", tags)
	}
}

// An update that changes the Tags property must reconcile via
// TagResource/UntagResource, the same as every other tag-aware handler.
func TestUpdateStack_KMSKeyTagsReconciled(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "kms-tags-update"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {kmsPropsKeyTemplate(`"Tags":[{"Key":"team","Value":"platform"}]`)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	keyID := kmsKeyPhysicalID(t, srv, stackName)

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {kmsPropsKeyTemplate(`"Tags":[{"Key":"owner","Value":"platform-eng"}]`)},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	tags := kmsListResourceTags(t, srv, keyID)
	if _, stillPresent := tags["team"]; stillPresent {
		t.Errorf("ListResourceTags = %v, want `team` removed", tags)
	}
	if tags["owner"] != "platform-eng" {
		t.Fatalf("ListResourceTags = %v, want owner=platform-eng", tags)
	}
}

// Origin values the emulator cannot back (no PENDING_IMPORT/ImportKeyMaterial
// flow, no CloudHSM cluster) must fail the resource loudly instead of the key
// silently reporting AWS_KMS origin regardless of what the template asked for.
func TestCreateStack_KMSKeyRejectsUnsupportedOrigin(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "kms-origin-external"
	template := kmsPropsKeyTemplate(`"Origin":"EXTERNAL"`)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")
}

// MultiRegion:true implies a replica-key relationship (ReplicateKey) the
// emulator does not model, so it must fail loudly rather than mint an
// ordinary single-Region key and call it multi-Region.
func TestCreateStack_KMSKeyRejectsMultiRegion(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "kms-multi-region"
	template := kmsPropsKeyTemplate(`"MultiRegion":true`)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")
}

// EnableKeyRotation:true (CDK's `enableKeyRotation: true`, a very common
// setting) used to be silently dropped: the stack came up CREATE_COMPLETE
// with a key that would never rotate and no record that rotation was ever
// requested. The emulator does not model rotation state at all, so this must
// now fail the resource loudly rather than keep lying about it.
func TestCreateStack_KMSKeyRejectsEnableKeyRotation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "kms-enable-rotation"
	template := kmsPropsKeyTemplate(`"EnableKeyRotation":true`)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")
}
