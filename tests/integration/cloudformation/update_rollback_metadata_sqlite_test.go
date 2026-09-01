//go:build !nosqlite

// The generation a rollback restores is only restored if it is what the durable
// store holds — the in-memory half of this is covered, under every tag set, by
// TestUpdateStack_rollbackRestoresSupersededMetadata in
// update_rollback_metadata_test.go. Under -tags nosqlite, state.NewHybridStore
// is a stub that always errors (internal/state/sqlite_hybrid_nosqlite.go), so
// such a build has no durable backend to prove anything against — see
// tests/AGENTS.md § Build-tag-sensitive tests.

package cloudformation_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestUpdateStack_rollbackMetadataRestorationIsDurable(t *testing.T) {
	// Given: a provisioned stack with a template, parameters and tags, on a
	// durable store.
	dataDir := t.TempDir()
	first := openReadyHybridStore(t, dataDir)
	srv := helpers.NewTestServer(t, helpers.WithStore(first))
	stackName := "upd-rollback-metadata-durable"
	logGroup := "upd-rollback-metadata-durable-logs"
	baseTemplate := fmt.Sprintf(rollbackMetadataBaseTemplate, logGroup)

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":                          {stackName},
		"TemplateBody":                       {baseTemplate},
		"Parameters.member.1.ParameterKey":   {"Stage"},
		"Parameters.member.1.ParameterValue": {"one"},
		"Tags.member.1.Key":                  {"stage"},
		"Tags.member.1.Value":                {"one"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	before := describeStackMetadata(t, srv, stackName)

	// When: an update carrying a new template, parameters and tags fails, and
	// the store is then closed and reopened as a restart would.
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":                          {stackName},
		"TemplateBody":                       {fmt.Sprintf(rollbackMetadataFailingTemplate, logGroup)},
		"Parameters.member.1.ParameterKey":   {"Stage"},
		"Parameters.member.1.ParameterValue": {"two"},
		"Tags.member.1.Key":                  {"stage"},
		"Tags.member.1.Value":                {"two"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")
	assertRolledBackToBaseGeneration(t, srv, stackName, baseTemplate, before)

	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	second := openReadyHybridStore(t, dataDir)
	t.Cleanup(func() { _ = second.Close() })
	restarted := helpers.NewTestServer(t, helpers.WithStore(second))

	// Then: what survives is the restored generation, not the failed attempt.
	assertRolledBackToBaseGeneration(t, restarted, stackName, baseTemplate, before)
}
