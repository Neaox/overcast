// ARN-based stack addressing. AWS accepts "the name or the unique stack ID"
// for every stack-scoped operation's StackName member — and after a stack is
// deleted, the ARN is the only handle that still resolves, which is what CDK's
// deploy monitor relies on when it polls DescribeStackEvents during a delete.
package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// createStackReturningARN creates a stack and returns the StackId minted for it.
func createStackReturningARN(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{name},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	arn := extractBetween(string(readBody(t, resp)), "<StackId>", "</StackId>")
	if !strings.HasPrefix(arn, "arn:aws:cloudformation:") {
		t.Fatalf("CreateStack returned no stack ARN, got %q", arn)
	}
	return arn
}

func TestStackReadOps_byARN_resolveTheStack(t *testing.T) {
	// Given: a stack that finished creating, addressed by the ARN CreateStack minted
	srv := helpers.NewTestServer(t)
	arn := createStackReturningARN(t, srv, "arn-read-stack")
	waitForStackStatus(t, srv, "arn-read-stack", "CREATE_COMPLETE")

	// When/Then: each read operation accepts the ARN as StackName
	for _, tc := range []struct {
		action string
		expect string
	}{
		{"DescribeStacks", "<StackName>arn-read-stack</StackName>"},
		{"DescribeStackEvents", "CREATE_COMPLETE"},
		{"ListStackResources", "ListStackResourcesResult"},
		{"DescribeStackResources", "DescribeStackResourcesResult"},
		{"GetTemplate", "Resources"},
		{"GetTemplateSummary", "GetTemplateSummaryResult"},
	} {
		resp := cfnQuery(t, srv, tc.action, url.Values{"StackName": []string{arn}})
		b := readBody(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s by ARN: status = %d, want 200; body: %s", tc.action, resp.StatusCode, b)
			continue
		}
		if !strings.Contains(string(b), tc.expect) {
			t.Errorf("%s by ARN: body missing %q: %s", tc.action, tc.expect, b)
		}
	}
}

func TestUpdateStack_byARN_updatesTheStack(t *testing.T) {
	// Given: a stack that finished creating
	srv := helpers.NewTestServer(t)
	arn := createStackReturningARN(t, srv, "arn-update-stack")
	waitForStackStatus(t, srv, "arn-update-stack", "CREATE_COMPLETE")

	// When: UpdateStack addresses it by ARN
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{arn},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()

	// Then: the stack is found and updates to completion
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "arn-update-stack", "UPDATE_COMPLETE")
}

func TestDeletedStack_byARN_remainsDescribable(t *testing.T) {
	// Given: a stack deleted by its ARN — the handle CDK polls with during destroy
	srv := helpers.NewTestServer(t)
	arn := createStackReturningARN(t, srv, "arn-del-stack")
	waitForStackStatus(t, srv, "arn-del-stack", "CREATE_COMPLETE")
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{arn}})
	del.Body.Close()
	waitForStackStatus(t, srv, "arn-del-stack", "DELETE_COMPLETE")

	// When/Then: the ARN still resolves the deleted stack's final state and events
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{arn}})
	b := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "DELETE_COMPLETE") {
		t.Errorf("DescribeStacks by ARN after delete: status %d, body: %s", resp.StatusCode, b)
	}
	ev := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{arn}})
	eb := readBody(t, ev)
	ev.Body.Close()
	if ev.StatusCode != http.StatusOK || !strings.Contains(string(eb), "DELETE_COMPLETE") {
		t.Errorf("DescribeStackEvents by ARN after delete: status %d, body: %s", ev.StatusCode, eb)
	}

	// And: deleting again is a no-op — no second wave of delete events
	before := strings.Count(string(eb), "<EventId>")
	again := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"arn-del-stack"}})
	helpers.AssertStatus(t, again, http.StatusOK)
	again.Body.Close()
	ev2 := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{arn}})
	eb2 := readBody(t, ev2)
	ev2.Body.Close()
	if after := strings.Count(string(eb2), "<EventId>"); after != before {
		t.Errorf("re-deleting a DELETE_COMPLETE stack appended events: %d -> %d", before, after)
	}

	// And: mutations treat the deleted stack as nonexistent
	upd := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"arn-del-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	ub := readBody(t, upd)
	upd.Body.Close()
	if upd.StatusCode != http.StatusBadRequest || !strings.Contains(string(ub), "does not exist") {
		t.Errorf("UpdateStack on deleted stack: status %d, body: %s", upd.StatusCode, ub)
	}
}

func TestStaleStackARN_afterRecreate_doesNotResolve(t *testing.T) {
	// Given: a name deleted and recreated — two incarnations, two ARNs
	srv := helpers.NewTestServer(t)
	oldARN := createStackReturningARN(t, srv, "reborn-stack")
	waitForStackStatus(t, srv, "reborn-stack", "CREATE_COMPLETE")
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"reborn-stack"}})
	del.Body.Close()
	waitForStackStatus(t, srv, "reborn-stack", "DELETE_COMPLETE")
	newARN := createStackReturningARN(t, srv, "reborn-stack")
	if newARN == oldARN {
		t.Fatalf("recreate reused the stack ARN %s", oldARN)
	}

	// When/Then: the previous incarnation's ARN no longer resolves
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{oldARN}})
	b := readBody(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "does not exist") {
		t.Errorf("DescribeStacks by stale ARN: status %d, body: %s", resp.StatusCode, b)
	}

	// And: the new incarnation's ARN does
	resp2 := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{newARN}})
	b2 := readBody(t, resp2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || !strings.Contains(string(b2), "reborn-stack") {
		t.Errorf("DescribeStacks by current ARN: status %d, body: %s", resp2.StatusCode, b2)
	}
}

func TestChangeSetOps_byStackARN_resolveTheStack(t *testing.T) {
	// Given: a stack that finished creating
	srv := helpers.NewTestServer(t)
	arn := createStackReturningARN(t, srv, "arn-cs-stack")
	waitForStackStatus(t, srv, "arn-cs-stack", "CREATE_COMPLETE")

	// When: a change set is cut against the stack ARN
	create := cfnQuery(t, srv, "CreateChangeSet", url.Values{
		"StackName":     []string{arn},
		"ChangeSetName": []string{"cs-by-arn"},
		"TemplateBody":  []string{minimalTemplate},
	})
	cb := readBody(t, create)
	create.Body.Close()
	if create.StatusCode != http.StatusOK {
		t.Fatalf("CreateChangeSet by ARN: status %d, body: %s", create.StatusCode, cb)
	}

	// Then: the change set is recorded under the stack's name, not the ARN
	desc := cfnQuery(t, srv, "DescribeChangeSet", url.Values{
		"StackName":     []string{arn},
		"ChangeSetName": []string{"cs-by-arn"},
	})
	db := readBody(t, desc)
	desc.Body.Close()
	if desc.StatusCode != http.StatusOK || !strings.Contains(string(db), "<StackName>arn-cs-stack</StackName>") {
		t.Errorf("DescribeChangeSet by ARN: status %d, body: %s", desc.StatusCode, db)
	}

	// And: DeleteChangeSet by stack ARN actually removes it
	rm := cfnQuery(t, srv, "DeleteChangeSet", url.Values{
		"StackName":     []string{arn},
		"ChangeSetName": []string{"cs-by-arn"},
	})
	helpers.AssertStatus(t, rm, http.StatusOK)
	rm.Body.Close()
	desc2 := cfnQuery(t, srv, "DescribeChangeSet", url.Values{
		"StackName":     []string{arn},
		"ChangeSetName": []string{"cs-by-arn"},
	})
	d2b := readBody(t, desc2)
	desc2.Body.Close()
	if desc2.StatusCode != http.StatusBadRequest || !strings.Contains(string(d2b), "ChangeSetNotFound") {
		t.Errorf("DescribeChangeSet after delete by ARN: status %d, body: %s", desc2.StatusCode, d2b)
	}
}

func TestCreateChangeSet_missingStackByARN_doesNotMintPlaceholder(t *testing.T) {
	// Given: an ARN that resolves to no stack
	srv := helpers.NewTestServer(t)
	ghost := "arn:aws:cloudformation:us-east-1:000000000000:stack/never-created/11111111-2222-3333-4444-555555555555"

	// When: a CREATE-type change set names it
	resp := cfnQuery(t, srv, "CreateChangeSet", url.Values{
		"StackName":     []string{ghost},
		"ChangeSetName": []string{"cs-ghost"},
		"ChangeSetType": []string{"CREATE"},
		"TemplateBody":  []string{minimalTemplate},
	})
	b := readBody(t, resp)
	resp.Body.Close()

	// Then: an ARN cannot name a new stack — no placeholder is created
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "does not exist") {
		t.Errorf("CreateChangeSet CREATE by ghost ARN: status %d, body: %s", resp.StatusCode, b)
	}
	ls := cfnQuery(t, srv, "ListStacks", nil)
	lb := readBody(t, ls)
	ls.Body.Close()
	if strings.Contains(string(lb), "never-created") {
		t.Errorf("a placeholder stack was minted for the ghost ARN: %s", lb)
	}
}
