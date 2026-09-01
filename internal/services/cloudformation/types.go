package cloudformation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── Stack ──────────────────────────────────────────────────────────────────

// Stack represents a CloudFormation stack.
// Events are stored separately (see cfnStore.appendStackEvent / getStackEvents)
// so that stack metadata reads never load the full event history.
type Stack struct {
	StackName       string            `json:"StackName"`
	StackID         string            `json:"StackId"`
	Region          string            `json:"Region,omitempty"`
	ParentStackID   string            `json:"ParentId,omitempty"`
	RootID          string            `json:"RootId,omitempty"`
	TemplateBody    string            `json:"TemplateBody"`
	Parameters      []Parameter       `json:"Parameters,omitempty"`
	Tags            []Tag             `json:"Tags,omitempty"`
	Outputs         []Output          `json:"Outputs,omitempty"`
	Resources       []StackResource   `json:"Resources,omitempty"`
	Status          string            `json:"StackStatus"`
	StatusReason    string            `json:"StackStatusReason,omitempty"`
	Capabilities    []string          `json:"Capabilities,omitempty"`
	RoleARN         string            `json:"RoleARN,omitempty"`
	DisableRollback bool              `json:"DisableRollback,omitempty"`
	CreatedAt       time.Time         `json:"CreationTime"`
	UpdatedAt       *time.Time        `json:"LastUpdatedTimestamp,omitempty"`
	DeletedAt       *time.Time        `json:"DeletionTime,omitempty"`
	Metadata        map[string]string `json:"Metadata,omitempty"`

	// RetainExceptOnCreate opts the stack operation currently in flight into
	// deleting the resources it created rather than leaving a rollback to
	// orphan them — see parseRetainExceptOnCreate for what AWS defines it to
	// mean. Like DisableRollback it belongs to the operation, so each
	// Create/Update overwrites it as it starts.
	RetainExceptOnCreate bool `json:"RetainExceptOnCreate,omitempty"`

	// ClientRequestToken identifies the stack operation currently in flight,
	// and is stamped onto every event that operation records. It belongs to
	// the operation rather than to the stack, so each Create/Update/Delete
	// overwrites it as it starts — see stackOperationToken for what it holds
	// when the caller supplied none.
	ClientRequestToken string `json:"ClientRequestToken,omitempty"`
}

// Parameter is a key-value pair for a stack parameter.
type Parameter struct {
	Key   string `json:"ParameterKey"`
	Value string `json:"ParameterValue"`
}

// Tag is a key-value pair for tagging.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// Output is a stack output value.
type Output struct {
	Key         string `json:"OutputKey"`
	Value       string `json:"OutputValue"`
	Description string `json:"Description,omitempty"`
	ExportName  string `json:"ExportName,omitempty"`
}

// stackGeneration is the stack metadata one update supersedes: the template,
// parameters and tags DescribeStacks and GetTemplate reported before it began.
//
// Every update overwrites all three on the stack record before provisioning
// starts, so from that moment the record describes the attempt rather than what
// is actually deployed. If the attempt then fails and rolls back, this is what
// the stack has to be handed back, alongside its resources.
type stackGeneration struct {
	TemplateBody string
	Parameters   []Parameter
	Tags         []Tag
}

// captureStackGeneration snapshots a stack's metadata. Call it before assigning
// an update's attempted template, parameters or tags — afterwards there is
// nothing left to capture.
func captureStackGeneration(stack *Stack) stackGeneration {
	return stackGeneration{
		TemplateBody: stack.TemplateBody,
		Parameters:   append([]Parameter(nil), stack.Parameters...),
		Tags:         append([]Tag(nil), stack.Tags...),
	}
}

// StackResource tracks a single provisioned resource within a stack.
type StackResource struct {
	LogicalID    string            `json:"LogicalResourceId"`
	PhysicalID   string            `json:"PhysicalResourceId,omitempty"`
	Type         string            `json:"ResourceType"`
	Status       string            `json:"ResourceStatus"`
	StatusReason string            `json:"ResourceStatusReason,omitempty"`
	Timestamp    time.Time         `json:"Timestamp"`
	Attributes   map[string]string `json:"Attributes,omitempty"`
	// PropertiesHash is a sha256 of the resolved Properties at provisioning
	// time. UpdateStack uses it to detect property drift and re-provision
	// only resources whose properties actually changed (e.g. Lambda code).
	PropertiesHash string `json:"PropertiesHash,omitempty"`
	// Properties are the resolved properties this resource was provisioned
	// with. They must persist: an update reads them back as the "old" side of
	// the comparison every Update handler makes to decide whether a changed
	// property can be applied in place or forces replacement, and to diff which
	// fields to patch. Marked `json:"-"` they came back nil on every update, so
	// each of those comparisons quietly concluded "nothing changed" — an
	// AWS::RDS::DBInstance kept its old master username, an API Gateway stage
	// patched nothing, and a custom resource's Lambda was handed a null
	// OldResourceProperties, which the custom-resource contract does not allow.
	//
	// Records written before this was persisted decode to nil, which every
	// caller already treats as "no prior state known" and skips.
	Properties map[string]any `json:"Properties,omitempty"`
	// DeletionPolicy / UpdateReplacePolicy are copied from the template at
	// provisioning time so DeleteStack and UpdateStack can honour Retain /
	// Snapshot semantics without re-parsing the template.
	DeletionPolicy      string `json:"DeletionPolicy,omitempty"`
	UpdateReplacePolicy string `json:"UpdateReplacePolicy,omitempty"`
}

// shouldRetainOnDelete reports whether an ordinary stack delete or resource
// removal should leave this resource in place. Snapshot is treated as Retain
// because Overcast does not snapshot.
func (r *StackResource) shouldRetainOnDelete() bool {
	switch r.DeletionPolicy {
	case "Retain", "RetainExceptOnCreate", "Snapshot":
		return true
	}
	return false
}

// shouldRetainOnCreateRollback applies the initial-create exception that
// ordinary deletion does not: template RetainExceptOnCreate is deleted, and
// RollbackStack's operation flag can opt a Retain resource into deletion too.
//
// Snapshot ignores the flag on purpose, and the asymmetry with Retain is the
// part worth explaining. AWS scopes the operation flag narrowly: it deletes
// newly created resources when a stack operation rolls back, "including newly
// created resources marked with a deletion policy of Retain". Retain is the
// only policy it names, and the flag exists because a resource kept back by
// Retain is kept with nothing to show for it — a rollback leaves an orphan the
// operator has to find and delete by hand. Snapshot does not have that problem
// on AWS, because AWS takes the snapshot on the way out.
//
// Overcast does not take the snapshot. So deleting a Snapshot resource here
// would be data loss with no snapshot behind it, which is a worse outcome than
// the orphan the flag was introduced to avoid, and worse than what the same
// template does against real AWS. Retaining is the conservative reading, and it
// keeps this in step with shouldRetainOnDelete, which treats Snapshot as Retain
// for exactly the same reason.
func (r *StackResource) shouldRetainOnCreateRollback(retainExceptOnCreate bool) bool {
	switch r.DeletionPolicy {
	case "Retain":
		return !retainExceptOnCreate
	case "Snapshot":
		return true
	}
	return false
}

// existsServiceSide reports whether a real resource stands behind this record:
// the stack created and named something, and has not since torn it down. It is
// what a rollback asks before deleting, and what an update asks before treating
// the record as a resource it can diff against and change in place.
//
// The physical ID answers the first half, and CREATE_FAILED counts when it is
// set. A create that failed outright never named anything and leaves the ID
// empty; one that failed to stabilize — an RDS instance whose database never
// came up, an ECS service whose tasks never started — was created and named
// first, and only then failed to become usable. Skipping those left a real
// database behind under a name that nothing in the stack still recorded.
//
// DELETE_COMPLETE answers the second half. A create rollback records what it
// deleted rather than dropping it from the list, so the record outlives the
// resource and keeps its physical ID.
func (r *StackResource) existsServiceSide() bool {
	return r.PhysicalID != "" && r.Status != ResourceDeleteComplete
}

// shouldRetainOnReplace reports whether an UpdateStack replacement should
// orphan the old resource (skip its delete) instead of deleting it.
func (r *StackResource) shouldRetainOnReplace() bool {
	switch r.UpdateReplacePolicy {
	case "Retain", "Snapshot":
		return true
	}
	return false
}

// StackEvent is an immutable record of a lifecycle state transition.
// Events are appended as provisioning progresses and are never mutated.
// The order in which events are appended matches the order AWS emits them;
// DescribeStackEvents returns them newest-first.
type StackEvent struct {
	EventID              string    `json:"EventId"`
	StackID              string    `json:"StackId"`
	StackName            string    `json:"StackName"`
	LogicalResourceID    string    `json:"LogicalResourceId"`
	PhysicalResourceID   string    `json:"PhysicalResourceId,omitempty"`
	ResourceType         string    `json:"ResourceType"`
	ResourceStatus       string    `json:"ResourceStatus"`
	ResourceStatusReason string    `json:"ResourceStatusReason,omitempty"`
	Timestamp            time.Time `json:"Timestamp"`
	// ClientRequestToken is the token of the stack operation that produced
	// this event, as on AWS. Overcast fills it with the request ID of the API
	// call that started the operation when the caller supplied no token of
	// their own, which is what makes an event traceable back to the request —
	// see stackOperationToken.
	ClientRequestToken string `json:"ClientRequestToken,omitempty"`
}

// ── Change Set ─────────────────────────────────────────────────────────────

// ChangeSet represents a CloudFormation change set.
type ChangeSet struct {
	ChangeSetName   string      `json:"ChangeSetName"`
	ChangeSetID     string      `json:"ChangeSetId"`
	StackID         string      `json:"StackId"`
	StackName       string      `json:"StackName"`
	TemplateBody    string      `json:"TemplateBody"`
	Parameters      []Parameter `json:"Parameters,omitempty"`
	Tags            []Tag       `json:"Tags,omitempty"`
	TagsSet         bool        `json:"TagsSet,omitempty"`
	Capabilities    []string    `json:"Capabilities,omitempty"`
	Status          string      `json:"Status"`
	StatusReason    string      `json:"StatusReason,omitempty"`
	ChangeSetType   string      `json:"ChangeSetType"` // CREATE or UPDATE
	Changes         []Change    `json:"Changes,omitempty"`
	CreatedAt       time.Time   `json:"CreationTime"`
	ExecutionStatus string      `json:"ExecutionStatus"`
}

// Change describes a single resource change in a change set.
type Change struct {
	Type           string         `json:"Type"` // always "Resource"
	ResourceChange ResourceChange `json:"ResourceChange"`
}

// ResourceChange describes how a resource will be modified.
type ResourceChange struct {
	Action             string `json:"Action"` // Add, Modify, Remove
	LogicalResourceID  string `json:"LogicalResourceId"`
	PhysicalResourceID string `json:"PhysicalResourceId,omitempty"`
	ResourceType       string `json:"ResourceType"`
	Replacement        string `json:"Replacement,omitempty"` // True, False, Conditional
}

// ── Stack statuses ─────────────────────────────────────────────────────────

const (
	StatusCreateInProgress   = "CREATE_IN_PROGRESS"
	StatusCreateComplete     = "CREATE_COMPLETE"
	StatusCreateFailed       = "CREATE_FAILED"
	StatusUpdateInProgress   = "UPDATE_IN_PROGRESS"
	StatusUpdateComplete     = "UPDATE_COMPLETE"
	StatusUpdateFailed       = "UPDATE_FAILED"
	StatusDeleteInProgress   = "DELETE_IN_PROGRESS"
	StatusDeleteComplete     = "DELETE_COMPLETE"
	StatusDeleteFailed       = "DELETE_FAILED"
	StatusRollbackInProgress = "ROLLBACK_IN_PROGRESS"
	StatusRollbackComplete   = "ROLLBACK_COMPLETE"
	StatusRollbackFailed     = "ROLLBACK_FAILED"

	StatusUpdateRollbackInProgress = "UPDATE_ROLLBACK_IN_PROGRESS"
	StatusUpdateRollbackComplete   = "UPDATE_ROLLBACK_COMPLETE"
	StatusUpdateRollbackFailed     = "UPDATE_ROLLBACK_FAILED"

	// A stack that exists only because a change set was created against a name
	// that had none. It has an ID and nothing else — no template, no resources
	// — until the change set is executed, which is why it is the one state a
	// CREATE change set may legitimately find its target stack in.
	StatusReviewInProgress = "REVIEW_IN_PROGRESS"

	// Import terminal states. Overcast never produces them — resource import
	// is not emulated — but they are two of the five states AWS calls a last
	// known stable state, so the update guard has to name them or it would
	// refuse an imported stack that real CloudFormation updates happily.
	StatusImportComplete         = "IMPORT_COMPLETE"
	StatusImportRollbackComplete = "IMPORT_ROLLBACK_COMPLETE"

	// Import in-progress states. Like the two above, Overcast never produces
	// them, but ListStacks' StackStatusFilter validation (see
	// stackStatusEnumValues) has to know they are real so a caller filtering
	// on one is not refused for a value AWS itself accepts.
	StatusImportInProgress         = "IMPORT_IN_PROGRESS"
	StatusImportRollbackInProgress = "IMPORT_ROLLBACK_IN_PROGRESS"
	StatusImportRollbackFailed     = "IMPORT_ROLLBACK_FAILED"

	// Cleanup states. An update does not finish the moment every resource has
	// been updated: CloudFormation then removes what the update superseded —
	// resources dropped from the template, and the originals that replacements
	// replaced — and reports that phase separately. Both are transient states
	// on the way to their COMPLETE counterpart, and both are observable, which
	// is why a stack can sit visibly in one when a leftover resource will not
	// delete.
	StatusUpdateCompleteCleanupInProgress         = "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS"
	StatusUpdateRollbackCompleteCleanupInProgress = "UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS"

	ChangeSetStatusCreateComplete = "CREATE_COMPLETE"
	ChangeSetStatusFailed         = "FAILED"

	ExecStatusAvailable         = "AVAILABLE"
	ExecStatusUnavailable       = "UNAVAILABLE"
	ExecStatusExecuteComplete   = "EXECUTE_COMPLETE"
	ExecStatusExecuteFailed     = "EXECUTE_FAILED"
	ExecStatusExecuteInProgress = "EXECUTE_IN_PROGRESS"

	ResourceCreateInProgress = "CREATE_IN_PROGRESS"
	ResourceCreateComplete   = "CREATE_COMPLETE"
	ResourceCreateFailed     = "CREATE_FAILED"
	ResourceUpdateInProgress = "UPDATE_IN_PROGRESS"
	ResourceUpdateComplete   = "UPDATE_COMPLETE"
	ResourceUpdateFailed     = "UPDATE_FAILED"
	ResourceDeleteInProgress = "DELETE_IN_PROGRESS"
	ResourceDeleteComplete   = "DELETE_COMPLETE"
	ResourceDeleteFailed     = "DELETE_FAILED"
	ResourceDeleteSkipped    = "DELETE_SKIPPED"
)

// ── Stack status guards ────────────────────────────────────────────────────
//
// Which states an operation may start from is a property of the status
// vocabulary, not of either dispatch path, so the predicates live here and
// both handler.go (Query) and typed_logic.go (typed/JSON) call them. Inline
// status comparisons at each entry point are what let CreateStack enforce a
// rule CreateChangeSet did not — see #806 for the same shape in CloudWatch.

// stackStableStatuses are the states AWS calls a stack's "last known stable
// state": every generation that completed, and so something an update rollback
// can return to. An update may always start from one of them.
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html
//
// ROLLBACK_COMPLETE is pointedly absent: a create that rolled back never
// reached CREATE_COMPLETE, so there is nothing to update onto and nothing for
// a failed update to unwind to — such a stack can only be deleted and created
// again. The in-progress states are absent because an operation is already
// running, and UPDATE_ROLLBACK_FAILED because AWS names it as the one failed
// status a change set may not even be created against.
//
// This is not the whole answer for an update, though — see canUpdateStackFrom
// for the CREATE_FAILED / UPDATE_FAILED carve-out, which this list has no way
// to express because it depends on the request rather than the status.
//
// The web console keeps a copy of this set in
// web/src/features/cloudformation/utils.ts, and
// TestStackStableStatuses_matchWebConsole holds the two together.
var stackStableStatuses = map[string]bool{
	StatusCreateComplete:         true,
	StatusUpdateComplete:         true,
	StatusUpdateRollbackComplete: true,
	StatusImportComplete:         true,
	StatusImportRollbackComplete: true,
}

// canUpdateStackFrom reports whether an update may start from the given status,
// given whether the update preserves successfully provisioned resources —
// UpdateStack's DisableRollback, the API spelling of the console's "Preserve
// successfully provisioned resources" and the CLI's --disable-rollback.
//
// A stable status is always updatable. CREATE_FAILED and UPDATE_FAILED are
// updatable *only* with that option, which is AWS's documented remediation
// path: a failed operation that preserved its resources is resumed by fixing
// the template and re-running the update with the option set, and the failed
// resources are retried rather than the whole stack being unwound.
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/stack-failure-options.html
//
//	"When you update a stack that's in a FAILED state, you must select
//	Preserve successfully provisioned resources for the Stack failure
//	options to continue updating your stack."
//
// So an update from a FAILED status without the option is still refused —
// AWS requires the option, and a stack left FAILED without it has resources
// in a state the plain update path cannot reason about. ROLLBACK_COMPLETE is
// refused either way: it is not one of the two statuses the carve-out names,
// and it is the case #822 exists for.
func canUpdateStackFrom(status string, preserveOnFailure bool) bool {
	if stackStableStatuses[status] {
		return true
	}
	return preserveOnFailure && (status == StatusCreateFailed || status == StatusUpdateFailed)
}

// stackStableStatusList returns the stable statuses in sorted order, for tests
// and error messages that need to enumerate rather than test them.
func stackStableStatusList() []string {
	out := make([]string, 0, len(stackStableStatuses))
	for status := range stackStableStatuses {
		out = append(out, status)
	}
	sort.Strings(out)
	return out
}

// stackNameTaken reports whether a stack record in the given status still owns
// its name, so that CreateStack naming it must fail with
// AlreadyExistsException.
//
// Only DELETE_COMPLETE releases the name. Overcast keeps a deleted stack's
// record as a tombstone — DescribeStacks by name and the event history still
// answer for it, as they do in AWS — but the name is free again, which is what
// makes the CDK CLI's delete-then-redeploy of a failed stack work.
func stackNameTaken(status string) bool {
	return status != StatusDeleteComplete
}

// changeSetCreateTargetTaken reports whether a stack record in the given status
// blocks a change set with ChangeSetType=CREATE.
//
// It is deliberately one status weaker than stackNameTaken. A CREATE change set
// creates a REVIEW_IN_PROGRESS stack that holds nothing but an ID, and both a
// second CREATE change set and the execution of the first legitimately find
// their target in that state — whereas CreateStack against the same placeholder
// is AlreadyExistsException in real CloudFormation, because a stack of that
// name does exist. The two rules are near neighbours and not the same rule, so
// they are two predicates rather than one with a flag.
func changeSetCreateTargetTaken(status string) bool {
	return stackNameTaken(status) && status != StatusReviewInProgress
}

// changeSetTargetError reports why a change set of the given type cannot run
// against a stack in its current state, or nil when it can.
//
// One function serves both change-set entrances: CreateChangeSet, which is
// where real CloudFormation checks, and ExecuteChangeSet, which has to check
// again because a change set outlives the state it was created against — the
// stack it targets can be created, updated, rolled back or deleted between the
// two calls.
//
// IMPORT rides with UPDATE, as it does everywhere else in these handlers
// (computeChanges included): it edits an existing stack, so it needs the same
// last known stable state an update does.
//
// The update arm passes preserveOnFailure unconditionally, where UpdateStack
// passes its own DisableRollback. That is AWS's rule for change sets rather
// than a shortcut: the preserve option is chosen when the change set is
// *executed* (`execute-change-set --disable-rollback`), not when it is created,
// so creating one against a failed stack is allowed on its own —
//
//	"You can initiate a change set for a stack with a status of CREATE_FAILED
//	or UPDATE_FAILED, but not for a status of UPDATE_ROLLBACK_FAILED."
//	https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/stack-failure-options.html
//
// — and UPDATE_ROLLBACK_FAILED, the status that note excludes, is refused by
// the stable set anyway. Overcast does not model ExecuteChangeSet's own
// DisableRollback parameter; the stack keeps the setting from the operation
// that failed, which is what its provisioning then honours.
func changeSetTargetError(stack *Stack, changeSetType string) *protocol.AWSError {
	if changeSetType == "CREATE" {
		if changeSetCreateTargetTaken(stack.Status) {
			return stackAlreadyExistsErr(stack.StackName)
		}
		return nil
	}
	if !canUpdateStackFrom(stack.Status, true) {
		return stackNotUpdatableErr(stack)
	}
	return nil
}

// stackAlreadyExistsErr is AWS's answer to a create naming a stack that exists.
// The error code is modelled (AlreadyExistsException, HTTP 400); the message
// wording follows CreateStack's, which is the shape AWS uses.
func stackAlreadyExistsErr(stackName string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "AlreadyExistsException",
		Message:    fmt.Sprintf("Stack [%s] already exists", stackName),
		HTTPStatus: http.StatusBadRequest,
	}
}

// stackNotUpdatableErr is AWS's answer to an update of a stack with no last
// known stable state:
//
//	Stack:arn:aws:cloudformation:… is in ROLLBACK_COMPLETE state and can not
//	be updated.
//
// The colon with no space, and "can not", are AWS's own.
func stackNotUpdatableErr(stack *Stack) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationError",
		Message:    fmt.Sprintf("Stack:%s is in %s state and can not be updated.", stack.StackID, stack.Status),
		HTTPStatus: http.StatusBadRequest,
	}
}

// ── Template model ─────────────────────────────────────────────────────────

// Template is a parsed CloudFormation template.
type Template struct {
	AWSTemplateFormatVersion string                       `json:"AWSTemplateFormatVersion"`
	Description              string                       `json:"Description"`
	Parameters               map[string]TemplateParameter `json:"Parameters"`
	Resources                map[string]TemplateResource  `json:"Resources"`
	Outputs                  map[string]TemplateOutput    `json:"Outputs"`
	Conditions               map[string]any               `json:"Conditions"`
	Mappings                 map[string]any               `json:"Mappings"`
}

// TemplateParameter describes a declared parameter.
type TemplateParameter struct {
	Type          string                   `json:"Type"`
	Default       templateParameterValue   `json:"Default"`
	Description   string                   `json:"Description"`
	AllowedValues []templateParameterValue `json:"AllowedValues"`
}

// templateParameterValue preserves CloudFormation's string parameter model
// while accepting JSON/YAML numeric and boolean scalar defaults, which AWS
// accepts for Number and String parameters and resolves to strings for Ref.
type templateParameterValue string

func (v *templateParameterValue) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*v = ""
		return nil
	}
	var text string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*v = templateParameterValue(text)
		return nil
	}
	var scalar any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&scalar); err != nil {
		return err
	}
	switch value := scalar.(type) {
	case json.Number:
		*v = templateParameterValue(value.String())
	case bool:
		*v = templateParameterValue(fmt.Sprintf("%t", value))
	default:
		return fmt.Errorf("parameter default must be a scalar")
	}
	return nil
}

// TemplateResource describes a declared resource.
type TemplateResource struct {
	Type       string         `json:"Type"`
	Properties map[string]any `json:"Properties"`
	DependsOn  any            `json:"DependsOn"` // string or []string
	Condition  string         `json:"Condition"`
	// DeletionPolicy controls what happens on stack delete: "Delete" (default),
	// "Retain" (leave the resource in place), or "Snapshot" (treated as Retain
	// in Overcast — we don't snapshot).
	DeletionPolicy string `json:"DeletionPolicy,omitempty"`
	// UpdateReplacePolicy controls what happens when an UpdateStack requires
	// the resource to be replaced (delete + create). "Delete" (default),
	// "Retain" (orphan the old resource), or "Snapshot" (treated as Retain).
	UpdateReplacePolicy string `json:"UpdateReplacePolicy,omitempty"`
}

// TemplateOutput describes a declared output.
type TemplateOutput struct {
	Value       any    `json:"Value"`
	Description string `json:"Description"`
	Export      *struct {
		Name any `json:"Name"`
	} `json:"Export"`
	Condition string `json:"Condition"`
}
