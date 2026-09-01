package cloudformation_test

// continue_update_rollback_test.go — the whole recovery, end to end.
//
// UPDATE_ROLLBACK_FAILED is the one stack state with no way out except
// ContinueUpdateRollback: it has no last known stable state, so every update
// and change set is refused, and RollbackStack has already been tried — that
// is how the stack got here. The test drives a real stack into it through the
// services rather than by seeding a record, because the state is only
// interesting when something outside CloudFormation is genuinely blocking the
// rollback, and clears the blocker the way an operator would.

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// The update adds a second bucket whose VersioningConfiguration S3 rejects.
// The bucket itself is created before the configuration is applied, so the
// resource fails with a physical resource behind it — which is what gives the
// rollback something to delete, and the test something to block.
const continueRollbackUpdateTemplate = `{"Resources":{
  "Keep":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-cur-keep"}},
  "Doomed":{"Type":"AWS::S3::Bucket","DependsOn":"Keep","Properties":{
    "BucketName":"cfn-cur-doomed",
    "VersioningConfiguration":{"Status":"Invalid"}
  }}
}}`

const continueRollbackBaseTemplate = `{"Resources":{
  "Keep":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-cur-keep"}}
}}`

func TestContinueUpdateRollback_recoversAStackWedgedByABlockedRollback(t *testing.T) {
	// Given: a deployed stack
	srv := helpers.NewTestServer(t)
	stackName := "cfn-continue-update-rollback"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {continueRollbackBaseTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Given: an update that fails after creating a bucket, with rollback
	// disabled so the operator can inspect what the failure left standing —
	// which is also the window this test needs to block the rollback.
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":       {stackName},
		"TemplateBody":    {continueRollbackUpdateTemplate},
		"DisableRollback": {"true"},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_FAILED")

	// Given: something outside the stack now holds the failed resource — here
	// an object in the bucket, which S3 refuses to delete around
	s3PutObject(t, srv, "cfn-cur-doomed", "held.txt", "not empty")

	// When: the operator rolls the stack back
	rollback := cfnQuery(t, srv, "RollbackStack", url.Values{"StackName": {stackName}})
	defer rollback.Body.Close()
	helpers.AssertStatus(t, rollback, http.StatusOK)

	// Then: the rollback cannot finish, and the stack is wedged — no update,
	// no change set and no second RollbackStack gets it out of here
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_FAILED")
	assertStackNotUpdatable(t, srv, stackName)

	// When: the blocker is cleared and the rollback is continued
	s3DeleteObject(t, srv, "cfn-cur-doomed", "held.txt")
	continued := cfnQuery(t, srv, "ContinueUpdateRollback", url.Values{"StackName": {stackName}})
	defer continued.Body.Close()
	helpers.AssertStatus(t, continued, http.StatusOK)
	assertContinueUpdateRollbackEnvelope(t, continued)

	// Then: the rollback finishes, the resource it was stuck on is gone, and
	// the stack is deployable again
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")
	assertS3BucketAbsent(t, srv, "cfn-cur-doomed")

	redeploy := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {continueRollbackBaseTemplate},
	})
	defer redeploy.Body.Close()
	helpers.AssertStatus(t, redeploy, http.StatusOK)
}

func TestContinueUpdateRollback_fromAStableStackIsRefusedNotUnimplemented(t *testing.T) {
	// Given: a healthy stack — the operation has no rollback to continue
	srv := helpers.NewTestServer(t)
	stackName := "cfn-cur-stable"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{"Resources":{"Queue":{"Type":"AWS::SQS::Queue",` +
			`"Properties":{"QueueName":"cfn-cur-stable-q"}}}}`},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: ContinueUpdateRollback is called anyway
	resp := cfnQuery(t, srv, "ContinueUpdateRollback", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()

	// Then: AWS's own refusal reaches the client, not a 501 — the whole point
	// being that the operation exists and has an opinion about the state
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	body, _ := io.ReadAll(resp.Body)
	var errResp cfnErrorResponse
	if err := xml.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v; body: %s", err, body)
	}
	if errResp.Error.Code != "ValidationError" {
		t.Errorf("error code = %q, want ValidationError; body: %s", errResp.Error.Code, body)
	}
	if !strings.Contains(errResp.Error.Message, "CREATE_COMPLETE state and can not be updated") {
		t.Errorf("error message = %q, want it to name the state", errResp.Error.Message)
	}
}

// assertStackNotUpdatable pins what makes UPDATE_ROLLBACK_FAILED a dead end:
// the ordinary ways forward are all refused, so the only thing that moves the
// stack is finishing the rollback.
func assertStackNotUpdatable(t *testing.T, srv *helpers.TestServer, stackName string) {
	t.Helper()
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {continueRollbackBaseTemplate},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("UpdateStack on a wedged stack: status = %d, want 400; body: %s", resp.StatusCode, body)
	}
}

// assertContinueUpdateRollbackEnvelope checks the wire shape an SDK decodes:
// AWS's response carries no members, but the result element has to be there
// and named, or the operation does not deserialise.
func assertContinueUpdateRollbackEnvelope(t *testing.T, resp *http.Response) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	for _, want := range []string{"ContinueUpdateRollbackResponse", "ContinueUpdateRollbackResult", "RequestId"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("response does not contain %q; body: %s", want, body)
		}
	}
}

func s3DeleteObject(t *testing.T, srv *helpers.TestServer, bucket, key string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/"+bucket+"/"+key, nil)
	if err != nil {
		t.Fatalf("delete object request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete object %s/%s: %v", bucket, key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete object %s/%s: status %d; body: %s", bucket, key, resp.StatusCode, body)
	}
}

func assertS3BucketAbsent(t *testing.T, srv *helpers.TestServer, bucket string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodHead, srv.URL+"/"+bucket, nil)
	if err != nil {
		t.Fatalf("head bucket request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("head bucket %s: %v", bucket, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD /%s after the rollback finished: status = %d, want 404 — the bucket should be gone",
			bucket, resp.StatusCode)
	}
}
