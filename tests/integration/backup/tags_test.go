// tags_test.go — AWS Backup TagResource / ListTags / UntagResource (#1195).
//
// TagResource and ListTags share the /tags/{ResourceArn} path space with
// several other services (internal/router/router.go's "/tags service
// dispatch" block); UntagResource does not — it is Backup's own
// POST /untag/{ResourceArn} — see internal/services/backup/handler_tags.go.
package backup_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const (
	pathTags  = "/tags"
	pathUntag = "/untag"
)

// backupArnPath builds the path an AWS SDK actually sends for a
// /tags/{ResourceArn} or /untag/{ResourceArn} call: the ARN's colons
// percent-encoded, exactly as router.go's tagsDispatch documents SDKs doing.
func backupArnPath(prefix, arn string) string {
	return prefix + "/" + strings.ReplaceAll(arn, ":", "%3A")
}

func vaultArn(name string) string {
	return "arn:aws:backup:" + defaultRegion + ":000000000000:backup-vault:" + name
}

func planArn(id string) string {
	return "arn:aws:backup:" + defaultRegion + ":000000000000:backup-plan:" + id
}

func listBackupTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := backupDo(t, srv, http.MethodGet, backupArnPath(pathTags, arn), defaultRegion, nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.Tags
}

// ─── TagResource / ListTags — vaults ─────────────────────────────────────────

func TestBackupTagResource_vault_success(t *testing.T) {
	// Given: a vault with no tags
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	arn := vaultArn("vault-a")

	// When: TagResource is called at POST /tags/{ResourceArn}
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"env": "prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTags sees it
	if got := listBackupTags(t, srv, arn); got["env"] != "prod" {
		t.Fatalf("Tags = %#v, want env=prod", got)
	}
}

func TestBackupTagResource_vault_createTimeTagsStick(t *testing.T) {
	// Given: BackupVaultTags is set on CreateBackupVault, previously accepted
	// and dropped (#815/#1195)
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPut, pathVaults+"/vault-a", defaultRegion, map[string]any{
		"BackupVaultTags": map[string]any{"created": "inline"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the tags are readable via ListTags — they actually stuck
	if got := listBackupTags(t, srv, vaultArn("vault-a")); got["created"] != "inline" {
		t.Fatalf("Tags = %#v, want created=inline", got)
	}
}

func TestBackupTagResource_vault_notFound(t *testing.T) {
	// Given: no such vault
	srv := helpers.NewTestServer(t)

	// When: TagResource names it anyway
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, vaultArn("nope")), defaultRegion, map[string]any{
		"Tags": map[string]any{"k": "v"},
	})
	defer resp.Body.Close()

	// Then: ResourceNotFoundException, backup's 400-status client error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestBackupTagResource_invalidTagsRejected(t *testing.T) {
	// Given: a vault with an existing tag
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	arn := vaultArn("vault-a")
	seed := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"keep": "me"},
	})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	// When: TagResource is called with a reserved aws: key prefix
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"aws:reserved": "x"},
	})
	defer resp.Body.Close()

	// Then: rejected, and the existing tag is untouched
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
	if got := listBackupTags(t, srv, arn); len(got) != 1 || got["keep"] != "me" {
		t.Fatalf("Tags = %#v after a rejected TagResource, want unchanged {keep: me}", got)
	}
}

func TestBackupTagResource_deletedWithVault(t *testing.T) {
	// Given: a tagged vault
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	arn := vaultArn("vault-a")
	tagResp := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"env": "prod"},
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)

	// When: the vault is deleted
	del := backupDo(t, srv, http.MethodDelete, pathVaults+"/vault-a", defaultRegion, nil)
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: its tags die with it — ListTags on the same ARN answers not-found,
	// not an orphaned tag blob nothing else can reach (#1037 tags-die-with-
	// resource semantics).
	list := backupDo(t, srv, http.MethodGet, backupArnPath(pathTags, arn), defaultRegion, nil)
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusBadRequest)
	helpers.AssertJSONError(t, list, "ResourceNotFoundException")
}

// ─── TagResource / ListTags — plans ──────────────────────────────────────────

func TestBackupTagResource_plan_success(t *testing.T) {
	// Given: a plan with no tags
	srv := helpers.NewTestServer(t)
	planID, _ := createPlan(t, srv, "plan-a")["BackupPlanId"].(string)
	arn := planArn(planID)

	// When: TagResource is called
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"env": "staging"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTags sees it
	if got := listBackupTags(t, srv, arn); got["env"] != "staging" {
		t.Fatalf("Tags = %#v, want env=staging", got)
	}
}

func TestBackupTagResource_plan_createTimeTagsStick(t *testing.T) {
	// Given: BackupPlanTags is set on CreateBackupPlan
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPut, pathPlans, defaultRegion, map[string]any{
		"BackupPlan": map[string]any{
			"BackupPlanName": "plan-a",
			"Rules":          []map[string]any{{"RuleName": "daily", "TargetBackupVaultName": "default"}},
		},
		"BackupPlanTags": map[string]any{"created": "inline"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	planID, _ := decodeMap(t, resp)["BackupPlanId"].(string)

	// Then: the tags are readable via ListTags
	if got := listBackupTags(t, srv, planArn(planID)); got["created"] != "inline" {
		t.Fatalf("Tags = %#v, want created=inline", got)
	}
}

func TestBackupTagResource_plan_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, planArn("nope")), defaultRegion, map[string]any{
		"Tags": map[string]any{"k": "v"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ─── UntagResource ────────────────────────────────────────────────────────

func TestBackupUntagResource_success(t *testing.T) {
	// Given: a vault with two tags
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	arn := vaultArn("vault-a")
	seed := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"a": "1", "b": "2"},
	})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	// When: UntagResource removes one, at its own POST /untag/{ResourceArn}.
	// The member is TagKeyList — Backup's own spelling, confirmed against the
	// real AWS CLI (#1195) — not the TagKeys most other services use.
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathUntag, arn), defaultRegion, map[string]any{
		"TagKeyList": []string{"a"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: only the other remains
	if got := listBackupTags(t, srv, arn); len(got) != 1 || got["b"] != "2" {
		t.Fatalf("Tags = %#v, want only {b: 2}", got)
	}
}

func TestBackupUntagResource_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPost, backupArnPath(pathUntag, vaultArn("nope")), defaultRegion, map[string]any{
		"TagKeyList": []string{"a"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestBackupUntagResource_isNotServedUnderTags(t *testing.T) {
	// UntagResource is modeled at POST /untag/{ResourceArn}, not DELETE
	// /tags/{ResourceArn} nor POST /tags/{ResourceArn} — confirm the /tags
	// path only ever answers TagResource/ListTags for a POST/GET, never an
	// untag.
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	arn := vaultArn("vault-a")
	seed := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"a": "1"},
	})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	resp := backupDo(t, srv, http.MethodDelete, backupArnPath(pathTags, arn), defaultRegion, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("DELETE %s answered 200; UntagResource must not be reachable there", pathTags)
	}

	// The tag is still there — the DELETE above did not remove it.
	if got := listBackupTags(t, srv, arn); got["a"] != "1" {
		t.Fatalf("Tags = %#v after a bad DELETE /tags call, want unchanged {a: 1}", got)
	}
}

// ─── ListTags ─────────────────────────────────────────────────────────────

func TestBackupListTags_emptyForUntaggedVault(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	got := listBackupTags(t, srv, vaultArn("vault-a"))
	if len(got) != 0 {
		t.Fatalf("Tags = %#v, want empty", got)
	}
}

// ListTags is bound with a trailing slash by the real AWS CLI/botocore
// (`GET /tags/{ResourceArn}/`), confirmed empirically against this codebase
// with `aws backup list-tags --debug` — the pinned model's own URI field
// carries no slash, the same asymmetry service.go's RegisterRoutes doc
// comment already documents for ListBackupVaults/ListBackupPlans/
// CreateBackupPlan/GetBackupPlan. Missing this form 404'd every real-CLI
// ListTags call and was caught only by the compat suite's cli/backup-tags
// group, not by any Go-level test that built its own request instead of
// shelling out to the real CLI (#1195).
func TestBackupListTags_trailingSlash(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	arn := vaultArn("vault-a")
	tag := backupDo(t, srv, http.MethodPost, backupArnPath(pathTags, arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"env": "prod"},
	})
	defer tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	resp := backupDo(t, srv, http.MethodGet, backupArnPath(pathTags, arn)+"/", defaultRegion, nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.Tags["env"] != "prod" {
		t.Fatalf("Tags = %#v after GET %s/, want env=prod", out.Tags, backupArnPath(pathTags, arn))
	}
}

func TestBackupListTags_malformedArnNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	for _, arn := range []string{"not-an-arn", "arn:aws:backup:" + defaultRegion + ":000000000000:recovery-point:x"} {
		t.Run(arn, func(t *testing.T) {
			resp := backupDo(t, srv, http.MethodGet, backupArnPath(pathTags, arn), defaultRegion, nil)
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("resourceArn=%q: ListTags succeeded, want an error", arn)
			}
		})
	}
}
