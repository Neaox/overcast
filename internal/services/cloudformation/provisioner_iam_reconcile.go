package cloudformation

// IAM principal-attachment reconciliation (issues #521 and #710).
//
// The IAM resources carry relationship properties — a role's ManagedPolicyArns
// and inline Policies, a user's Groups, a managed policy's Roles/Users/Groups —
// that translate to individual IAM calls rather than to parameters of the
// create call. Honouring them means every handler needs the same three moves:
//
//   - diff the desired list against the previous one and emit the adds and
//     removes (Create diffs against nothing, Delete against everything),
//   - apply those mutations in order, compensating the ones already applied
//     when a later one fails, so a rejected update leaves the principal as it
//     was (the engine's rollback then re-runs Update with the property sets
//     swapped, which reuses the same diff in reverse),
//   - on teardown, detach before delete — IAM answers DeleteConflict while
//     stack-created attachments remain (#706/#707), so a Create that attaches
//     without a Delete that detaches breaks ordinary stack teardown (#710).
//
// CloudFormation stays a thin translator: nothing here validates names, ARNs
// or documents beyond their shape — the IAM service answers for existence and
// content exactly as it would for a direct caller.
//
// Ported and adapted from the salvage branch codex/issue-521-iam-cfn.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"

	"github.com/overcast-sh/overcast/internal/config"
)

const iamQueryVersion = "2010-05-08"

// iamQuery dispatches one IAM Query action, discarding the response body.
func iamQuery(ctx context.Context, router http.Handler, region, action string, params map[string]string) error {
	params = maps.Clone(params)
	params["Action"] = action
	params["Version"] = iamQueryVersion
	if _, err := internalQuery(ctx, router, region, params); err != nil {
		return fmt.Errorf("iam %s: %w", action, err)
	}
	return nil
}

// isIAMNoSuchEntity reports whether err is IAM's NoSuchEntity refusal.
//
// It reads the dispatch failure's own fields rather than its rendered text.
// The text used to be the only thing available, so this grepped it for
// "HTTP 404" and "<Code>NoSuchEntity</Code>" — a match that would have gone
// quietly false the moment the reason changed shape, which is exactly what
// status_reason.go did to it.
func isIAMNoSuchEntity(err error) bool {
	var callErr *serviceCallError
	return errors.As(err, &callErr) &&
		callErr.StatusCode == http.StatusNotFound && callErr.Code == "NoSuchEntity"
}

// ─── Property shapes ────────────────────────────────────────────────────────

// iamStringSet reads a list-of-strings property into a set. A property that is
// absent reads as the empty set; one that is present but not a list of
// non-empty strings is an error, because dropping a malformed entry would
// silently provision less than the template asked for.
func iamStringSet(props map[string]any, property string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	value, ok := props[property]
	if !ok || value == nil {
		return out, nil
	}
	add := func(item any) error {
		text, ok := item.(string)
		if !ok || text == "" {
			return fmt.Errorf("%s entries must be non-empty strings", property)
		}
		out[text] = struct{}{}
		return nil
	}
	switch values := value.(type) {
	case []any:
		for _, item := range values {
			if err := add(item); err != nil {
				return nil, err
			}
		}
	case []string:
		for _, item := range values {
			if err := add(item); err != nil {
				return nil, err
			}
		}
	case string:
		// Ref: AWS::NoValue resolves to the empty string rather than removing
		// the key; treat it as the property being absent.
		if values == "" {
			return out, nil
		}
		return nil, fmt.Errorf("%s must be a list", property)
	default:
		return nil, fmt.Errorf("%s must be a list", property)
	}
	return out, nil
}

// iamInlinePolicyMap reads the Policies property ([{PolicyName, PolicyDocument}])
// into a name → serialized document map.
func iamInlinePolicyMap(props map[string]any) (map[string]string, error) {
	out := make(map[string]string)
	value, ok := props["Policies"]
	if !ok || value == nil {
		return out, nil
	}
	policies, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("Policies must be a list")
	}
	for _, item := range policies {
		policy, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Policies entries must be objects")
		}
		name, _ := policy["PolicyName"].(string)
		if name == "" || policy["PolicyDocument"] == nil {
			return nil, fmt.Errorf("Policies entries require PolicyName and PolicyDocument")
		}
		document, err := json.Marshal(policy["PolicyDocument"])
		if err != nil {
			return nil, fmt.Errorf("marshal inline policy %s: %w", name, err)
		}
		out[name] = string(document)
	}
	return out, nil
}

type iamTag struct{ key, value string }

// iamTags reads the Tags property ([{Key, Value}]) keyed by tag key. Duplicate
// keys are rejected rather than collapsed, since CloudFormation's Tags is a
// list and collapsing would silently pick a winner the template never asked
// for.
func iamTags(props map[string]any) (map[string]iamTag, error) {
	out := make(map[string]iamTag)
	value, ok := props["Tags"]
	if !ok || value == nil {
		return out, nil
	}
	tags, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("Tags must be a list")
	}
	for _, item := range tags {
		tag, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Tags entries must be objects")
		}
		key, keyOK := tag["Key"].(string)
		tagValue, valueOK := tag["Value"].(string)
		if !keyOK || key == "" || !valueOK {
			return nil, fmt.Errorf("Tags entries require string Key and Value")
		}
		if _, duplicate := out[key]; duplicate {
			return nil, fmt.Errorf("Tags contains duplicate key %q", key)
		}
		out[key] = iamTag{key: key, value: tagValue}
	}
	return out, nil
}

// iamValidatePrincipalProperties rejects malformed relationship properties
// before any mutation is dispatched, so a bad template fails whole rather than
// half-applied.
func iamValidatePrincipalProperties(props map[string]any, includeGroups bool) error {
	if _, err := iamStringSet(props, "ManagedPolicyArns"); err != nil {
		return err
	}
	if _, err := iamInlinePolicyMap(props); err != nil {
		return err
	}
	if _, err := iamTags(props); err != nil {
		return err
	}
	if includeGroups {
		if _, err := iamStringSet(props, "Groups"); err != nil {
			return err
		}
	}
	return nil
}

// iamJSONPropertyChanged reports whether a resolved template property differs
// between the new and old property sets.
func iamJSONPropertyChanged(props, oldProps map[string]any, property string) bool {
	return !reflect.DeepEqual(props[property], oldProps[property])
}

// ─── Mutations and transactions ─────────────────────────────────────────────

// iamMutation is one IAM call plus the call that compensates it. Mutations
// with no undo (none currently) leave undoAction empty.
type iamMutation struct {
	action     string
	params     map[string]string
	undoAction string
	undoParams map[string]string
	// ignoreNoSuchEntity makes a NoSuchEntity answer a success — used on
	// teardown, where an entity that is already gone took its relationships
	// with it.
	ignoreNoSuchEntity bool
}

func ignoreIAMNoSuchEntity(mutations []iamMutation) []iamMutation {
	for i := range mutations {
		mutations[i].ignoreNoSuchEntity = true
	}
	return mutations
}

// iamTransactionError reports a failed mutation together with the outcome of
// compensating the mutations applied before it.
type iamTransactionError struct {
	err         error
	rollbackErr error // non-nil when compensation itself failed
}

func (e *iamTransactionError) Error() string {
	if e.rollbackErr != nil {
		return fmt.Sprintf("%v; rollback: %v", e.err, e.rollbackErr)
	}
	return e.err.Error()
}

func (e *iamTransactionError) Unwrap() error { return e.err }

// classifyIAMTransactionFailure maps a failed transaction to the engine's
// update-failure vocabulary: compensated means the resource is as it was
// (plain terminal failure), uncompensated means the rollback paths must not
// claim the previous state was restored.
func classifyIAMTransactionFailure(err error) error {
	var txErr *iamTransactionError
	if errors.As(err, &txErr) && txErr.rollbackErr != nil {
		return failDirtyUpdate(err)
	}
	return failUpdate(err)
}

// iamTransaction applies mutations in order and can compensate the applied
// prefix in reverse when something later fails.
type iamTransaction struct {
	ctx     context.Context
	router  http.Handler
	region  string
	applied []iamMutation
}

func newIAMTransaction(ctx context.Context, router http.Handler, region string) *iamTransaction {
	return &iamTransaction{ctx: ctx, router: router, region: region}
}

// apply dispatches each mutation. On failure it rolls back the already-applied
// prefix and returns an *iamTransactionError carrying both outcomes.
func (tx *iamTransaction) apply(mutations []iamMutation) error {
	for _, mutation := range mutations {
		if err := iamQuery(tx.ctx, tx.router, tx.region, mutation.action, mutation.params); err != nil {
			if mutation.ignoreNoSuchEntity && isIAMNoSuchEntity(err) {
				continue
			}
			return &iamTransactionError{err: err, rollbackErr: tx.rollback()}
		}
		tx.applied = append(tx.applied, mutation)
	}
	return nil
}

// rollback compensates the applied mutations in reverse order. It keeps going
// past individual failures — every remaining compensation is still owed — and
// reports the first one.
func (tx *iamTransaction) rollback() error {
	var rollbackErr error
	for i := len(tx.applied) - 1; i >= 0; i-- {
		mutation := tx.applied[i]
		if mutation.undoAction == "" {
			continue
		}
		if err := iamQuery(tx.ctx, tx.router, tx.region, mutation.undoAction, mutation.undoParams); err != nil && rollbackErr == nil {
			rollbackErr = err
		}
	}
	tx.applied = nil
	return rollbackErr
}

// ─── Mutation builders ──────────────────────────────────────────────────────
//
// Each builder diffs the desired properties against the previous ones and
// returns the adds and removes. Create passes oldProps == nil (everything is
// an add); teardown passes props == nil (everything is a remove).

// iamManagedPolicyMutations reconciles ManagedPolicyArns via
// Attach<Principal>Policy / Detach<Principal>Policy.
func iamManagedPolicyMutations(principalType, principalName string, props, oldProps map[string]any) ([]iamMutation, error) {
	desired, err := iamStringSet(props, "ManagedPolicyArns")
	if err != nil {
		return nil, err
	}
	previous, err := iamStringSet(oldProps, "ManagedPolicyArns")
	if err != nil {
		return nil, err
	}
	nameKey := principalType + "Name"
	mutations := make([]iamMutation, 0, len(desired)+len(previous))
	for arn := range desired {
		if _, existed := previous[arn]; existed {
			continue
		}
		params := map[string]string{nameKey: principalName, "PolicyArn": arn}
		mutations = append(mutations, iamMutation{action: "Attach" + principalType + "Policy", params: params, undoAction: "Detach" + principalType + "Policy", undoParams: params})
	}
	for arn := range previous {
		if _, remains := desired[arn]; remains {
			continue
		}
		params := map[string]string{nameKey: principalName, "PolicyArn": arn}
		mutations = append(mutations, iamMutation{action: "Detach" + principalType + "Policy", params: params, undoAction: "Attach" + principalType + "Policy", undoParams: params})
	}
	return mutations, nil
}

// iamInlinePolicyMutations reconciles the inline Policies property via
// Put<Principal>Policy / Delete<Principal>Policy. A policy whose document is
// unchanged is left alone.
func iamInlinePolicyMutations(principalType, principalName string, props, oldProps map[string]any) ([]iamMutation, error) {
	desired, err := iamInlinePolicyMap(props)
	if err != nil {
		return nil, err
	}
	previous, err := iamInlinePolicyMap(oldProps)
	if err != nil {
		return nil, err
	}
	nameKey := principalType + "Name"
	mutations := make([]iamMutation, 0, len(desired)+len(previous))
	for name, document := range desired {
		if old, existed := previous[name]; existed && old == document {
			continue
		}
		params := map[string]string{nameKey: principalName, "PolicyName": name, "PolicyDocument": document}
		undoAction := "Delete" + principalType + "Policy"
		undoParams := map[string]string{nameKey: principalName, "PolicyName": name}
		if old, existed := previous[name]; existed {
			undoAction = "Put" + principalType + "Policy"
			undoParams = map[string]string{nameKey: principalName, "PolicyName": name, "PolicyDocument": old}
		}
		mutations = append(mutations, iamMutation{action: "Put" + principalType + "Policy", params: params, undoAction: undoAction, undoParams: undoParams})
	}
	for name, old := range previous {
		if _, remains := desired[name]; remains {
			continue
		}
		params := map[string]string{nameKey: principalName, "PolicyName": name}
		undo := map[string]string{nameKey: principalName, "PolicyName": name, "PolicyDocument": old}
		mutations = append(mutations, iamMutation{action: "Delete" + principalType + "Policy", params: params, undoAction: "Put" + principalType + "Policy", undoParams: undo})
	}
	return mutations, nil
}

// iamUserGroupMutations reconciles a user's Groups via AddUserToGroup /
// RemoveUserFromGroup.
func iamUserGroupMutations(userName string, props, oldProps map[string]any) ([]iamMutation, error) {
	desired, err := iamStringSet(props, "Groups")
	if err != nil {
		return nil, err
	}
	previous, err := iamStringSet(oldProps, "Groups")
	if err != nil {
		return nil, err
	}
	mutations := make([]iamMutation, 0, len(desired)+len(previous))
	for group := range desired {
		if _, existed := previous[group]; existed {
			continue
		}
		params := map[string]string{"UserName": userName, "GroupName": group}
		mutations = append(mutations, iamMutation{action: "AddUserToGroup", params: params, undoAction: "RemoveUserFromGroup", undoParams: params})
	}
	for group := range previous {
		if _, remains := desired[group]; remains {
			continue
		}
		params := map[string]string{"UserName": userName, "GroupName": group}
		mutations = append(mutations, iamMutation{action: "RemoveUserFromGroup", params: params, undoAction: "AddUserToGroup", undoParams: params})
	}
	return mutations, nil
}

// iamPolicyPrincipalMutations reconciles a managed policy's Roles/Users/Groups
// attachment lists via Attach<Principal>Policy / Detach<Principal>Policy.
func iamPolicyPrincipalMutations(policyArn string, props, oldProps map[string]any) ([]iamMutation, error) {
	var mutations []iamMutation
	for _, principalType := range []string{"Role", "User", "Group"} {
		property := principalType + "s"
		desired, err := iamStringSet(props, property)
		if err != nil {
			return nil, err
		}
		previous, err := iamStringSet(oldProps, property)
		if err != nil {
			return nil, err
		}
		nameKey := principalType + "Name"
		for name := range desired {
			if _, existed := previous[name]; existed {
				continue
			}
			params := map[string]string{nameKey: name, "PolicyArn": policyArn}
			mutations = append(mutations, iamMutation{action: "Attach" + principalType + "Policy", params: params, undoAction: "Detach" + principalType + "Policy", undoParams: params})
		}
		for name := range previous {
			if _, remains := desired[name]; remains {
				continue
			}
			params := map[string]string{nameKey: name, "PolicyArn": policyArn}
			mutations = append(mutations, iamMutation{action: "Detach" + principalType + "Policy", params: params, undoAction: "Attach" + principalType + "Policy", undoParams: params})
		}
	}
	return mutations, nil
}

// iamEffectiveTags merges a resource's own Tags property with the stack's
// propagated tags — resource tags winning on key collision, the same
// convention mergeResourceTags uses for every other propagating resource type
// (#1310) — while staying in iamTag's map shape so the existing Tag<Principal>
// param builders don't need to change.
func iamEffectiveTags(props map[string]any, stackTags []Tag) (map[string]iamTag, error) {
	resourceTags, err := iamTags(props)
	if err != nil {
		return nil, err
	}
	out := make(map[string]iamTag, len(resourceTags)+len(stackTags))
	for _, tag := range stackTags {
		key := strings.TrimSpace(tag.Key)
		if key == "" {
			continue
		}
		out[key] = iamTag{key: key, value: tag.Value}
	}
	for key, tag := range resourceTags {
		out[key] = tag
	}
	return out, nil
}

// iamTagMutations reconciles the Tags property via Tag<Principal> /
// Untag<Principal>, one tag per mutation so each has an exact undo. Both sides
// are the effective (stack-merged) tag set, so a stack-tag-only change
// reconciles here too — the same effective-stack-tag mechanism #1143 built for
// Lambda/LogGroup/SecretsManager/SQS/Kinesis/ECR.
func iamTagMutations(principalType, principalName string, props, oldProps map[string]any, stackTags, previousStackTags []Tag) ([]iamMutation, error) {
	return iamTagMutationsKeyed(principalType+"Name", principalName, principalType, props, oldProps, stackTags, previousStackTags)
}

// iamPolicyTagMutations is iamTagMutations for AWS::IAM::ManagedPolicy:
// TagPolicy/UntagPolicy key on PolicyArn rather than a "<Principal>Name"
// parameter, so it cannot share iamTagMutations's nameKey derivation, but the
// diff logic is identical.
func iamPolicyTagMutations(policyArn string, props, oldProps map[string]any, stackTags, previousStackTags []Tag) ([]iamMutation, error) {
	return iamTagMutationsKeyed("PolicyArn", policyArn, "Policy", props, oldProps, stackTags, previousStackTags)
}

// iamTagMutationsKeyed is the shared diff behind iamTagMutations and
// iamPolicyTagMutations: nameKey/nameValue address the principal (RoleName,
// UserName, InstanceProfileName, or PolicyArn), and actionPrincipal names the
// Tag<X>/Untag<X> operation pair.
func iamTagMutationsKeyed(nameKey, nameValue, actionPrincipal string, props, oldProps map[string]any, stackTags, previousStackTags []Tag) ([]iamMutation, error) {
	desired, err := iamEffectiveTags(props, stackTags)
	if err != nil {
		return nil, err
	}
	previous, err := iamEffectiveTags(oldProps, previousStackTags)
	if err != nil {
		return nil, err
	}
	tagAction, untagAction := "Tag"+actionPrincipal, "Untag"+actionPrincipal
	mutations := make([]iamMutation, 0, len(desired)+len(previous))
	for key, old := range previous {
		if _, remains := desired[key]; remains {
			continue
		}
		params := map[string]string{nameKey: nameValue, "TagKeys.member.1": old.key}
		undo := map[string]string{nameKey: nameValue, "Tags.member.1.Key": old.key, "Tags.member.1.Value": old.value}
		mutations = append(mutations, iamMutation{action: untagAction, params: params, undoAction: tagAction, undoParams: undo})
	}
	for key, tag := range desired {
		if old, existed := previous[key]; existed && old.value == tag.value {
			continue
		}
		params := map[string]string{nameKey: nameValue, "Tags.member.1.Key": tag.key, "Tags.member.1.Value": tag.value}
		undoAction := untagAction
		undoParams := map[string]string{nameKey: nameValue, "TagKeys.member.1": tag.key}
		if old, existed := previous[key]; existed {
			undoAction = tagAction
			undoParams = map[string]string{nameKey: nameValue, "Tags.member.1.Key": old.key, "Tags.member.1.Value": old.value}
		}
		mutations = append(mutations, iamMutation{action: tagAction, params: params, undoAction: undoAction, undoParams: undoParams})
	}
	return mutations, nil
}

// iamTagParams flattens the Tags property into Query Tags.member.N parameters
// for a create call that applies tags inline.
func iamTagParams(params map[string]string, tags map[string]iamTag) {
	i := 1
	for _, tag := range tags {
		params[fmt.Sprintf("Tags.member.%d.Key", i)] = tag.key
		params[fmt.Sprintf("Tags.member.%d.Value", i)] = tag.value
		i++
	}
}

// ─── AWS::IAM::Group ────────────────────────────────────────────────────────

// iamGroupHandler provisions the group principal that User.Groups and
// ManagedPolicy.Groups attach to. Before it existed, AWS::IAM::Group fell
// through to the unknown-type stub and every membership silently pointed at
// nothing.
type iamGroupHandler struct{}

func (h *iamGroupHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if err := iamValidatePrincipalProperties(props, false); err != nil {
		return "", nil, err
	}
	name, _ := props["GroupName"].(string)
	if name == "" {
		name = rCtx.generatedNameWithin(maxNameLenIAMPolicy)
	}
	params := map[string]string{"GroupName": name}
	if path, _ := props["Path"].(string); path != "" {
		params["Path"] = path
	}
	if err := iamQuery(ctx, router, rCtx.Region, "CreateGroup", params); err != nil {
		return "", nil, err
	}
	managed, err := iamManagedPolicyMutations("Group", name, props, nil)
	if err != nil {
		return "", nil, err
	}
	inline, err := iamInlinePolicyMutations("Group", name, props, nil)
	if err != nil {
		return "", nil, err
	}
	if err := newIAMTransaction(ctx, router, rCtx.Region).apply(append(managed, inline...)); err != nil {
		if cleanupErr := iamQuery(ctx, router, rCtx.Region, "DeleteGroup", map[string]string{"GroupName": name}); cleanupErr != nil {
			return "", nil, fmt.Errorf("%w; cleanup newly-created group: %v", err, cleanupErr)
		}
		return "", nil, err
	}
	arn := fmt.Sprintf("arn:aws:iam::%s:group/%s", rCtx.AccountID, name)
	// CloudFormation Ref on AWS::IAM::Group returns the group name.
	return name, map[string]string{"Arn": arn, "GroupName": name}, nil
}

func (h *iamGroupHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalQuery(ctx, router, rCtx.Region, map[string]string{
		"Action": "DeleteGroup", "Version": iamQueryVersion, "GroupName": physicalID,
	})
	return iamTeardownError("DeleteGroup", physicalID, rec, err)
}

func (h *iamGroupHandler) DeleteWithProperties(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, rCtx *resolveContext) error {
	return iamPrincipalTeardown(ctx, router, rCtx, func() error {
		return h.Delete(ctx, router, cfg, physicalID, rCtx)
	}, func() ([]iamMutation, error) {
		managed, err := iamManagedPolicyMutations("Group", physicalID, nil, props)
		if err != nil {
			return nil, err
		}
		inline, err := iamInlinePolicyMutations("Group", physicalID, nil, props)
		if err != nil {
			return nil, err
		}
		return append(managed, inline...), nil
	})
}

func (h *iamGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if err := iamValidatePrincipalProperties(props, false); err != nil {
		return "", nil, failUpdate(err)
	}
	if name, _ := props["GroupName"].(string); name != "" && name != physicalID {
		return "", nil, errReplacementRequired
	}
	// The emulator has no UpdateGroup, so a Path change cannot be applied in
	// place; a replacement at least fails loudly for pinned names instead of
	// silently keeping the old path.
	if iamJSONPropertyChanged(props, oldProps, "Path") {
		return "", nil, errReplacementRequired
	}
	managed, err := iamManagedPolicyMutations("Group", physicalID, props, oldProps)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	inline, err := iamInlinePolicyMutations("Group", physicalID, props, oldProps)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	if err := newIAMTransaction(ctx, router, rCtx.Region).apply(append(managed, inline...)); err != nil {
		return "", nil, classifyIAMTransactionFailure(err)
	}
	arn := fmt.Sprintf("arn:aws:iam::%s:group/%s", rCtx.AccountID, physicalID)
	return physicalID, map[string]string{"Arn": arn, "GroupName": physicalID}, nil
}

// iamPrincipalTeardown is the shared detach-then-delete shape: build the
// removal mutations from the stored properties, apply them tolerating
// already-gone entities, delete the principal, and compensate the removals if
// the delete still refuses — a DELETE_FAILED stack must not additionally have
// lost the relationships its template declares.
func iamPrincipalTeardown(ctx context.Context, router http.Handler, rCtx *resolveContext, deletePrincipal func() error, buildMutations func() ([]iamMutation, error)) error {
	mutations, err := buildMutations()
	if err != nil {
		return fmt.Errorf("%w: %v", errDeletionBlocked, err)
	}
	tx := newIAMTransaction(ctx, router, rCtx.Region)
	if err := tx.apply(ignoreIAMNoSuchEntity(mutations)); err != nil {
		return fmt.Errorf("%w: %v", errDeletionBlocked, err)
	}
	if err := deletePrincipal(); err != nil {
		if rollbackErr := tx.rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("restore attachments: %w", rollbackErr))
		}
		return err
	}
	return nil
}
