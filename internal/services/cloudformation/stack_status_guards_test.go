package cloudformation

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
)

// Stack-status guards: which stack states each stack operation may start from.
//
// Every guard is exercised over both dispatch paths — the Query handlers on
// Handler and the typed functions behind the JSON/typed operation table —
// because the two have drifted before (#806) and a guard on one entrance is no
// guard at all. The two end-to-end tests (the CDK redeploy flow, and the web
// console's copy of the stable set) are the exceptions, and say why in place.

const statusGuardTemplate = `{"Resources":{}}`

// statusGuardResult is the part of a response these tests assert on, whichever
// path produced it: whether the call was accepted, and the AWS error otherwise.
type statusGuardResult struct {
	accepted bool
	code     string
	message  string
}

// statusGuardPath names one of the two dispatch paths and the call under test.
type statusGuardPath struct {
	name string
	call func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult
}

// statusGuardArgs carries every parameter the four guarded entry points need,
// so one table shape drives all of them.
type statusGuardArgs struct {
	stackName     string
	changeSetName string
	changeSetType string
	// disableRollback is UpdateStack's "preserve successfully provisioned
	// resources", which decides whether a FAILED stack may be updated.
	disableRollback bool
}

func statusGuardFromQuery(rec *httptest.ResponseRecorder) statusGuardResult {
	if rec.Code >= 200 && rec.Code < 300 {
		return statusGuardResult{accepted: true}
	}
	var resp queryErrorResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return statusGuardResult{code: "<unparseable>", message: rec.Body.String()}
	}
	return statusGuardResult{code: resp.Code, message: resp.Message}
}

func statusGuardFromTyped(aerr *protocol.AWSError) statusGuardResult {
	if aerr == nil {
		return statusGuardResult{accepted: true}
	}
	return statusGuardResult{code: aerr.Code, message: aerr.Message}
}

// ── The four guarded entry points, one adapter per dispatch path ────────────

var statusGuardCreateStackPaths = []statusGuardPath{
	{name: "query", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost("CreateStack", map[string]string{
			"StackName":    args.stackName,
			"TemplateBody": statusGuardTemplate,
		}))
		return statusGuardFromQuery(rec)
	}},
	{name: "typed", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		_, aerr := h.createStackTyped(context.Background(), &createStackReq{
			StackName:    args.stackName,
			TemplateBody: statusGuardTemplate,
		})
		return statusGuardFromTyped(aerr)
	}},
}

var statusGuardCreateChangeSetPaths = []statusGuardPath{
	{name: "query", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost("CreateChangeSet", map[string]string{
			"StackName":     args.stackName,
			"ChangeSetName": args.changeSetName,
			"ChangeSetType": args.changeSetType,
			"TemplateBody":  statusGuardTemplate,
		}))
		return statusGuardFromQuery(rec)
	}},
	{name: "typed", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		_, aerr := h.createChangeSetTyped(context.Background(), &createChangeSetReq{
			StackName:     args.stackName,
			ChangeSetName: args.changeSetName,
			ChangeSetType: args.changeSetType,
			TemplateBody:  statusGuardTemplate,
		})
		return statusGuardFromTyped(aerr)
	}},
}

var statusGuardExecuteChangeSetPaths = []statusGuardPath{
	{name: "query", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost("ExecuteChangeSet", map[string]string{
			"StackName":     args.stackName,
			"ChangeSetName": args.changeSetName,
		}))
		return statusGuardFromQuery(rec)
	}},
	{name: "typed", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		_, aerr := h.executeChangeSetTyped(context.Background(), &executeChangeSetReq{
			StackName:     args.stackName,
			ChangeSetName: args.changeSetName,
		})
		return statusGuardFromTyped(aerr)
	}},
}

var statusGuardUpdateStackPaths = []statusGuardPath{
	{name: "query", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost("UpdateStack", map[string]string{
			"StackName":       args.stackName,
			"TemplateBody":    statusGuardTemplate,
			"DisableRollback": strconv.FormatBool(args.disableRollback),
		}))
		return statusGuardFromQuery(rec)
	}},
	{name: "typed", call: func(t *testing.T, h *Handler, args statusGuardArgs) statusGuardResult {
		t.Helper()
		_, aerr := h.updateStackTyped(context.Background(), &updateStackReq{
			StackName:       args.stackName,
			TemplateBody:    statusGuardTemplate,
			DisableRollback: strconv.FormatBool(args.disableRollback),
		})
		return statusGuardFromTyped(aerr)
	}},
}

// statusGuardSeedChangeSet stores an executable change set of the given type
// against a stack, as CreateChangeSet would have left it.
func statusGuardSeedChangeSet(t *testing.T, st *cfnStore, stack *Stack, csName, csType string) {
	t.Helper()
	cs := &ChangeSet{
		ChangeSetName:   csName,
		ChangeSetID:     "arn:aws:cloudformation:us-east-1:000000000000:changeSet/" + csName + "/66666666-7777-8888-9999-000000000000",
		StackID:         stack.StackID,
		StackName:       stack.StackName,
		TemplateBody:    statusGuardTemplate,
		Status:          ChangeSetStatusCreateComplete,
		ChangeSetType:   csType,
		ExecutionStatus: ExecStatusAvailable,
		CreatedAt:       stack.CreatedAt,
	}
	if err := st.putChangeSet(context.Background(), cs); err != nil {
		t.Fatalf("putChangeSet: %v", err)
	}
}

// statusGuardStackStatus reads back the stored status of a stack.
func statusGuardStackStatus(t *testing.T, st *cfnStore, name string) string {
	t.Helper()
	got, err := st.getStack(context.Background(), name)
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if got == nil {
		t.Fatalf("stack %q disappeared", name)
	}
	return got.Status
}

// ── #821: a CREATE change set against a stack that already exists ──────────

// statusGuardOccupiedStatuses are the states in which a stack record still owns
// its name, so a CREATE change set naming it is AlreadyExistsException.
var statusGuardOccupiedStatuses = []string{
	StatusCreateComplete,
	StatusCreateFailed,
	StatusRollbackComplete,
	StatusRollbackFailed,
	StatusUpdateComplete,
	StatusUpdateFailed,
	StatusUpdateRollbackComplete,
	StatusUpdateRollbackFailed,
	StatusCreateInProgress,
	StatusUpdateInProgress,
}

func TestCreateChangeSet_createTypeAgainstExistingStack_alreadyExists(t *testing.T) {
	for _, status := range statusGuardOccupiedStatuses {
		for _, path := range statusGuardCreateChangeSetPaths {
			t.Run(status+"/"+path.name, func(t *testing.T) {
				// Given: a stack that already exists in this status
				h, st := newRollbackTestHandler(t)
				seedStack(t, st, "taken-name", status)

				// When: a CREATE change set names the same stack
				got := path.call(t, h, statusGuardArgs{
					stackName:     "taken-name",
					changeSetName: "cs1",
					changeSetType: "CREATE",
				})

				// Then: AWS's AlreadyExistsException comes back
				if got.accepted {
					t.Fatalf("CreateChangeSet(CREATE) was accepted against a %s stack", status)
				}
				if got.code != "AlreadyExistsException" {
					t.Errorf("error code = %q, want AlreadyExistsException (message: %s)", got.code, got.message)
				}
				if got.message != "Stack [taken-name] already exists" {
					t.Errorf("message = %q, want %q", got.message, "Stack [taken-name] already exists")
				}

				// Then: the existing stack is untouched
				if now := statusGuardStackStatus(t, st, "taken-name"); now != status {
					t.Errorf("stack status = %q, want %q — the rejected call moved the stack", now, status)
				}
			})
		}
	}
}

func TestCreateChangeSet_createTypeAgainstReviewInProgress_accepted(t *testing.T) {
	for _, path := range statusGuardCreateChangeSetPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: a stack that exists only as a change set's own placeholder
			h, st := newRollbackTestHandler(t)
			seedStack(t, st, "review-stack", StatusReviewInProgress)

			// When: another CREATE change set is made against it
			got := path.call(t, h, statusGuardArgs{
				stackName:     "review-stack",
				changeSetName: "cs2",
				changeSetType: "CREATE",
			})

			// Then: it is accepted — nothing has been provisioned yet
			if !got.accepted {
				t.Fatalf("CreateChangeSet(CREATE) rejected against REVIEW_IN_PROGRESS: %s: %s", got.code, got.message)
			}
		})
	}
}

func TestCreateChangeSet_createTypeAfterDeleteComplete_accepted(t *testing.T) {
	for _, path := range statusGuardCreateChangeSetPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: a deleted stack's tombstone record — the state the CDK CLI
			// leaves behind when it deletes a failed stack before re-deploying
			h, st := newRollbackTestHandler(t)
			seedStack(t, st, "cdk-toolkit", StatusDeleteComplete)

			// When: a CREATE change set re-uses the name
			got := path.call(t, h, statusGuardArgs{
				stackName:     "cdk-toolkit",
				changeSetName: "cdk-deploy-change-set",
				changeSetType: "CREATE",
			})

			// Then: it is accepted
			if !got.accepted {
				t.Fatalf("CreateChangeSet(CREATE) rejected after DELETE_COMPLETE: %s: %s", got.code, got.message)
			}
		})
	}
}

func TestExecuteChangeSet_createTypeAgainstExistingStack_alreadyExists(t *testing.T) {
	for _, path := range statusGuardExecuteChangeSetPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: a CREATE change set that outlived the placeholder it was
			// made against — the stack has since been created for real
			h, st := newRollbackTestHandler(t)
			stack := seedStack(t, st, "live-stack", StatusCreateComplete)
			statusGuardSeedChangeSet(t, st, stack, "stale-create", "CREATE")

			// When: the change set is executed
			got := path.call(t, h, statusGuardArgs{
				stackName:     "live-stack",
				changeSetName: "stale-create",
			})

			// Then: AWS's AlreadyExistsException comes back
			if got.accepted {
				t.Fatalf("ExecuteChangeSet(CREATE) was accepted against a CREATE_COMPLETE stack")
			}
			if got.code != "AlreadyExistsException" {
				t.Errorf("error code = %q, want AlreadyExistsException (message: %s)", got.code, got.message)
			}
			if got.message != "Stack [live-stack] already exists" {
				t.Errorf("message = %q, want %q", got.message, "Stack [live-stack] already exists")
			}

			// Then: the live stack is not re-created underneath the caller
			if now := statusGuardStackStatus(t, st, "live-stack"); now != StatusCreateComplete {
				t.Errorf("stack status = %q, want CREATE_COMPLETE", now)
			}
		})
	}
}

func TestExecuteChangeSet_createTypeAgainstPlaceholder_accepted(t *testing.T) {
	for _, path := range statusGuardExecuteChangeSetPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: a CREATE change set against its own REVIEW_IN_PROGRESS
			// placeholder — the ordinary first-deploy flow
			h, st := newRollbackTestHandler(t)
			stack := seedStack(t, st, "new-stack", StatusReviewInProgress)
			statusGuardSeedChangeSet(t, st, stack, "first-deploy", "CREATE")

			// When: the change set is executed
			got := path.call(t, h, statusGuardArgs{
				stackName:     "new-stack",
				changeSetName: "first-deploy",
			})

			// Then: it runs, and the stack is created
			if !got.accepted {
				t.Fatalf("ExecuteChangeSet(CREATE) rejected against its own placeholder: %s: %s", got.code, got.message)
			}
			if now := statusGuardStackStatus(t, st, "new-stack"); now != StatusCreateComplete {
				t.Errorf("stack status = %q, want CREATE_COMPLETE", now)
			}
		})
	}
}

// The guard's acceptance test: the CDK CLI deletes a stack it cannot deploy
// over and creates it again, so the whole delete-then-redeploy sequence has to
// stay green end to end. A guard that breaks this is the wrong guard.
func TestExecuteChangeSet_cdkRedeployAfterFailure_deleteThenCreate(t *testing.T) {
	// Given: a stack left in ROLLBACK_COMPLETE by a create that failed
	h, st := newRollbackTestHandler(t)
	seedStack(t, st, "cdk-app", StatusRollbackComplete, StackResource{
		LogicalID:    "Widget",
		Type:         "AWS::CloudFormation::WaitConditionHandle",
		Status:       ResourceCreateFailed,
		StatusReason: "cannot provision widget",
	})

	// When: the CDK deletes it and deploys again through a CREATE change set
	for _, call := range []struct {
		action string
		params map[string]string
	}{
		{"DeleteStack", map[string]string{"StackName": "cdk-app"}},
		{"CreateChangeSet", map[string]string{
			"StackName": "cdk-app", "ChangeSetName": "cdk-deploy-change-set",
			"ChangeSetType": "CREATE", "TemplateBody": statusGuardTemplate,
		}},
		{"ExecuteChangeSet", map[string]string{
			"StackName": "cdk-app", "ChangeSetName": "cdk-deploy-change-set",
		}},
	} {
		rec := httptest.NewRecorder()
		h.dispatch(rec, cfnPost(call.action, call.params))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body: %s", call.action, rec.Code, rec.Body.String())
		}
	}

	// Then: the stack is created again, carrying nothing from the failed run
	if now := statusGuardStackStatus(t, st, "cdk-app"); now != StatusCreateComplete {
		t.Fatalf("stack status = %q, want CREATE_COMPLETE", now)
	}
	stack, err := st.getStack(context.Background(), "cdk-app")
	if err != nil {
		t.Fatalf("getStack: %v", err)
	}
	if len(stack.Resources) != 0 {
		t.Errorf("resources = %v, want none — the failed run's records outlived it", stack.Resources)
	}
}

// CreateStack is stricter than ExecuteChangeSet(CREATE) and must stay so: real
// CloudFormation refuses CreateStack against a stack that exists in any form,
// including the REVIEW_IN_PROGRESS placeholder a change set left behind.
func TestCreateStack_againstReviewInProgress_alreadyExists(t *testing.T) {
	for _, path := range statusGuardCreateStackPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: a change set's placeholder stack
			h, st := newRollbackTestHandler(t)
			seedStack(t, st, "review-stack", StatusReviewInProgress)

			// When: CreateStack names it
			got := path.call(t, h, statusGuardArgs{stackName: "review-stack"})

			// Then: AlreadyExistsException, not a create
			if got.accepted {
				t.Fatalf("CreateStack was accepted against a REVIEW_IN_PROGRESS stack")
			}
			if got.code != "AlreadyExistsException" {
				t.Errorf("error code = %q, want AlreadyExistsException (message: %s)", got.code, got.message)
			}
		})
	}
}

// ── #822: updating a stack with no last known stable state ─────────────────

// statusGuardUnstableStatuses are states an update cannot start from: there is
// either no completed generation to update onto, or an operation already
// running.
var statusGuardUnstableStatuses = []string{
	StatusRollbackComplete,
	StatusRollbackFailed,
	StatusCreateFailed,
	StatusUpdateFailed,
	StatusUpdateRollbackFailed,
	StatusReviewInProgress,
	StatusCreateInProgress,
	StatusUpdateInProgress,
	StatusRollbackInProgress,
	StatusUpdateRollbackInProgress,
	StatusDeleteInProgress,
	StatusDeleteFailed,
}

func TestUpdateStack_fromUnstableStatus_validationError(t *testing.T) {
	for _, status := range statusGuardUnstableStatuses {
		for _, path := range statusGuardUpdateStackPaths {
			t.Run(status+"/"+path.name, func(t *testing.T) {
				// Given: a stack with no last known stable state to update onto
				h, st := newRollbackTestHandler(t)
				stack := seedStack(t, st, "unstable", status)

				// When: UpdateStack is called
				got := path.call(t, h, statusGuardArgs{stackName: "unstable"})

				// Then: AWS's ValidationError, naming the stack ARN and status
				if got.accepted {
					t.Fatalf("UpdateStack was accepted from %s", status)
				}
				if got.code != "ValidationError" {
					t.Errorf("error code = %q, want ValidationError (message: %s)", got.code, got.message)
				}
				want := "Stack:" + stack.StackID + " is in " + status + " state and can not be updated."
				if got.message != want {
					t.Errorf("message = %q, want %q", got.message, want)
				}

				// Then: the stack is left exactly as it was
				if now := statusGuardStackStatus(t, st, "unstable"); now != status {
					t.Errorf("stack status = %q, want %q — the rejected update moved the stack", now, status)
				}
			})
		}
	}
}

func TestUpdateStack_fromStableStatus_accepted(t *testing.T) {
	for _, status := range stackStableStatusList() {
		for _, path := range statusGuardUpdateStackPaths {
			t.Run(status+"/"+path.name, func(t *testing.T) {
				// Given: a stack in one of AWS's last known stable states
				h, st := newRollbackTestHandler(t)
				seedStack(t, st, "stable", status)

				// When: UpdateStack is called
				got := path.call(t, h, statusGuardArgs{stackName: "stable"})

				// Then: the update runs to completion
				if !got.accepted {
					t.Fatalf("UpdateStack rejected from %s: %s: %s", status, got.code, got.message)
				}
				if now := statusGuardStackStatus(t, st, "stable"); now != StatusUpdateComplete {
					t.Errorf("stack status = %q, want UPDATE_COMPLETE", now)
				}
			})
		}
	}
}

// AWS's remediation path for a failed operation that preserved its resources:
// fix the template, re-run the update with "preserve successfully provisioned
// resources", and the failed resources are retried instead of the whole stack
// being unwound. The option is required, so the same update without it is
// still refused.
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/stack-failure-options.html
func TestUpdateStack_fromFailedStatus_needsDisableRollback(t *testing.T) {
	for _, status := range []string{StatusCreateFailed, StatusUpdateFailed} {
		for _, path := range statusGuardUpdateStackPaths {
			t.Run(status+"/withDisableRollback/"+path.name, func(t *testing.T) {
				// Given: a stack whose operation failed with its resources preserved
				h, st := newRollbackTestHandler(t)
				seedStack(t, st, "resumable", status)

				// When: the update sets DisableRollback
				got := path.call(t, h, statusGuardArgs{stackName: "resumable", disableRollback: true})

				// Then: it is accepted and the retry runs
				if !got.accepted {
					t.Fatalf("UpdateStack from %s with DisableRollback rejected: %s: %s", status, got.code, got.message)
				}
				if now := statusGuardStackStatus(t, st, "resumable"); now != StatusUpdateComplete {
					t.Errorf("stack status = %q, want UPDATE_COMPLETE", now)
				}
			})

			t.Run(status+"/withoutDisableRollback/"+path.name, func(t *testing.T) {
				// Given: the same stack
				h, st := newRollbackTestHandler(t)
				stack := seedStack(t, st, "resumable", status)

				// When: the update omits the option AWS requires
				got := path.call(t, h, statusGuardArgs{stackName: "resumable"})

				// Then: it is refused, and the stack is left as it was
				if got.accepted {
					t.Fatalf("UpdateStack from %s was accepted without DisableRollback", status)
				}
				want := "Stack:" + stack.StackID + " is in " + status + " state and can not be updated."
				if got.code != "ValidationError" || got.message != want {
					t.Errorf("error = %s: %q, want ValidationError: %q", got.code, got.message, want)
				}
				if now := statusGuardStackStatus(t, st, "resumable"); now != status {
					t.Errorf("stack status = %q, want %q", now, status)
				}
			})
		}
	}
}

// The carve-out is for the two FAILED statuses only. ROLLBACK_COMPLETE is what
// #822 exists for: the create never completed, so there is nothing to preserve
// and nothing to resume, whatever the request asks for.
func TestUpdateStack_fromRollbackComplete_refusedWithOrWithoutDisableRollback(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		for _, path := range statusGuardUpdateStackPaths {
			t.Run(fmt.Sprintf("disableRollback=%t/%s", preserve, path.name), func(t *testing.T) {
				// Given: a stack whose create rolled back
				h, st := newRollbackTestHandler(t)
				stack := seedStack(t, st, "rolled-back", StatusRollbackComplete)

				// When: it is updated, with and without the preserve option
				got := path.call(t, h, statusGuardArgs{stackName: "rolled-back", disableRollback: preserve})

				// Then: refused either way
				if got.accepted {
					t.Fatalf("UpdateStack from ROLLBACK_COMPLETE was accepted with DisableRollback=%t", preserve)
				}
				want := "Stack:" + stack.StackID + " is in ROLLBACK_COMPLETE state and can not be updated."
				if got.code != "ValidationError" || got.message != want {
					t.Errorf("error = %s: %q, want ValidationError: %q", got.code, got.message, want)
				}
			})
		}
	}
}

// A change set may be created against a failed stack without any flag: the
// preserve option belongs to ExecuteChangeSet, not to CreateChangeSet.
func TestCreateChangeSet_updateTypeFromFailedStatus_accepted(t *testing.T) {
	for _, status := range []string{StatusCreateFailed, StatusUpdateFailed} {
		for _, path := range statusGuardCreateChangeSetPaths {
			t.Run(status+"/"+path.name, func(t *testing.T) {
				// Given: a stack left failed with its resources preserved
				h, st := newRollbackTestHandler(t)
				seedStack(t, st, "resumable", status)

				// When: an UPDATE change set is created against it
				got := path.call(t, h, statusGuardArgs{
					stackName:     "resumable",
					changeSetName: "retry",
					changeSetType: "UPDATE",
				})

				// Then: it is accepted
				if !got.accepted {
					t.Fatalf("CreateChangeSet(UPDATE) rejected against a %s stack: %s: %s", status, got.code, got.message)
				}
			})
		}
	}
}

func TestCreateChangeSet_updateTypeFromUnstableStatus_validationError(t *testing.T) {
	for _, path := range statusGuardCreateChangeSetPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: a stack whose create rolled back
			h, st := newRollbackTestHandler(t)
			stack := seedStack(t, st, "rolled-back", StatusRollbackComplete)

			// When: an UPDATE change set is made against it
			got := path.call(t, h, statusGuardArgs{
				stackName:     "rolled-back",
				changeSetName: "cs3",
				changeSetType: "UPDATE",
			})

			// Then: the same ValidationError UpdateStack would have returned
			if got.accepted {
				t.Fatalf("CreateChangeSet(UPDATE) was accepted against a ROLLBACK_COMPLETE stack")
			}
			if got.code != "ValidationError" {
				t.Errorf("error code = %q, want ValidationError (message: %s)", got.code, got.message)
			}
			want := "Stack:" + stack.StackID + " is in ROLLBACK_COMPLETE state and can not be updated."
			if got.message != want {
				t.Errorf("message = %q, want %q", got.message, want)
			}
		})
	}
}

func TestExecuteChangeSet_updateTypeFromUnstableStatus_validationError(t *testing.T) {
	for _, path := range statusGuardExecuteChangeSetPaths {
		t.Run(path.name, func(t *testing.T) {
			// Given: an UPDATE change set that outlived the stable stack it was
			// created against — an update has since failed and rolled back
			h, st := newRollbackTestHandler(t)
			stack := seedStack(t, st, "rolled-back", StatusRollbackComplete)
			statusGuardSeedChangeSet(t, st, stack, "stale-update", "UPDATE")

			// When: the change set is executed
			got := path.call(t, h, statusGuardArgs{
				stackName:     "rolled-back",
				changeSetName: "stale-update",
			})

			// Then: it is refused with the same ValidationError
			if got.accepted {
				t.Fatalf("ExecuteChangeSet(UPDATE) was accepted against a ROLLBACK_COMPLETE stack")
			}
			if got.code != "ValidationError" {
				t.Errorf("error code = %q, want ValidationError (message: %s)", got.code, got.message)
			}
			want := "Stack:" + stack.StackID + " is in ROLLBACK_COMPLETE state and can not be updated."
			if got.message != want {
				t.Errorf("message = %q, want %q", got.message, want)
			}
			if now := statusGuardStackStatus(t, st, "rolled-back"); now != StatusRollbackComplete {
				t.Errorf("stack status = %q, want ROLLBACK_COMPLETE", now)
			}
		})
	}
}

// ── The console must not offer an update the server refuses ────────────────

// statusGuardWebUtils is the web console's copy of the same rule. The two are
// separate languages with no shared source of truth, so the only thing that
// can keep them together is a test that reads one and compares it to the other.
const statusGuardWebUtils = "../../../web/src/features/cloudformation/utils.ts"

var statusGuardStableSetRE = regexp.MustCompile(`(?s)const STABLE_STATUSES = new Set\(\[(.*?)\]\)`)

var statusGuardQuotedRE = regexp.MustCompile(`"([A-Z_]+)"`)

// The relationship asserted is containment, not equality: every status whose
// Update button the console offers must be one the server accepts
// unconditionally.
//
// Equality would be the wrong test in one direction. The server also accepts
// CREATE_FAILED and UPDATE_FAILED when the request preserves successfully
// provisioned resources, and the console's update form has no such control —
// so the console being the smaller set is correct, and widening it to match
// would offer a button whose request the server would refuse. What must never
// happen is the other direction: a status the console offers that the server
// rejects out of hand.
func TestStackStableStatuses_webConsoleOffersNoUpdateTheServerRefuses(t *testing.T) {
	// Given: the web console's STABLE_STATUSES set
	src, err := os.ReadFile(statusGuardWebUtils)
	if err != nil {
		t.Fatalf("read %s: %v — if the file moved, move this guard with it", statusGuardWebUtils, err)
	}
	block := statusGuardStableSetRE.FindSubmatch(src)
	if block == nil {
		t.Fatalf("no `const STABLE_STATUSES = new Set([...])` in %s — if it was renamed, rename it here too", statusGuardWebUtils)
	}
	var web []string
	for _, m := range statusGuardQuotedRE.FindAllSubmatch(block[1], -1) {
		web = append(web, string(m[1]))
	}
	if len(web) == 0 {
		t.Fatalf("parsed no statuses out of STABLE_STATUSES in %s", statusGuardWebUtils)
	}
	sort.Strings(web)

	// When/Then: every one of them is a status the server updates from without
	// the caller having to ask for anything else
	for _, status := range web {
		if !canUpdateStackFrom(status, false) {
			t.Errorf("the console offers Update for %q, which the server refuses; server accepts %v",
				status, stackStableStatusList())
		}
	}
}
