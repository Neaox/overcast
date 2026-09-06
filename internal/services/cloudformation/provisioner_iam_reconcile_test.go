package cloudformation

// Unit coverage for the IAM reconcile machinery (issue #521): mutation
// diffing, transaction compensation, and the teardown idempotency contract.
// Test shapes derived from the earlier salvage branch codex/issue-521-iam-cfn.

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func TestIAMResourceDelete_isIdempotentOnNoSuchEntity(t *testing.T) {
	router := iamReconcileTestRouter(new([]string), func(string) int { return http.StatusNotFound })
	rCtx := &resolveContext{Region: "us-east-1"}
	tests := map[string]func() error{
		"role": func() error {
			return (&iamRoleHandler{}).Delete(context.Background(), router, nil, "missing-role", rCtx)
		},
		"user": func() error {
			return (&iamUserHandler{}).Delete(context.Background(), router, nil, "missing-user", rCtx)
		},
		"group": func() error {
			return (&iamGroupHandler{}).Delete(context.Background(), router, nil, "missing-group", rCtx)
		},
		"managed policy": func() error {
			return (&iamManagedPolicyHandler{}).Delete(context.Background(), router, nil, "arn:aws:iam::000000000000:policy/missing", rCtx)
		},
	}
	for name, deleteResource := range tests {
		t.Run(name, func(t *testing.T) {
			if err := deleteResource(); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
		})
	}
}

func TestIAMRoleUpdate_classifiesCompensationOutcome(t *testing.T) {
	oldProps := map[string]any{"RoleName": "role", "Tags": []any{map[string]any{"Key": "old", "Value": "value"}}}
	newProps := map[string]any{"RoleName": "role", "Tags": []any{map[string]any{"Key": "new", "Value": "value"}}}
	rCtx := &resolveContext{Region: "us-east-1"}

	for name, failRollback := range map[string]bool{"clean rollback": false, "failed rollback": true} {
		t.Run(name, func(t *testing.T) {
			var actions []string
			tagCalls := 0
			router := iamReconcileTestRouter(&actions, func(action string) int {
				if action != "TagRole" {
					return http.StatusOK
				}
				tagCalls++
				if tagCalls == 1 || failRollback {
					return http.StatusInternalServerError
				}
				return http.StatusOK
			})

			// When: the tag addition fails after the removal already applied.
			_, _, err := (&iamRoleHandler{}).Update(context.Background(), router, nil, "role", newProps, oldProps, rCtx)

			// Then: the failure is terminal, and the dirty bit reports whether
			// the compensation itself failed.
			var failure updateFailure
			if !errors.As(err, &failure) {
				t.Fatalf("Update() error = %v, want updateFailure", err)
			}
			if failure.dirty != failRollback {
				t.Fatalf("dirty = %v, want %v; actions=%v", failure.dirty, failRollback, actions)
			}
			if want := []string{"UntagRole", "TagRole", "TagRole"}; !reflect.DeepEqual(actions, want) {
				t.Fatalf("actions = %v, want %v", actions, want)
			}
		})
	}
}

func TestIAMManagedPolicyUpdate_compensatesPrincipalChangeWhenVersionCreationFails(t *testing.T) {
	// Given: an update that re-attaches the policy and changes its document,
	// where minting the new document version fails.
	var actions []string
	router := iamReconcileTestRouter(&actions, func(action string) int {
		if action == "CreatePolicyVersion" {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	})
	oldProps := map[string]any{"PolicyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{}}}
	newProps := map[string]any{
		"PolicyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow"}}},
		"Roles":          []any{"role"},
	}
	rCtx := &resolveContext{Region: "us-east-1"}

	// When: the update runs.
	_, _, err := (&iamManagedPolicyHandler{}).Update(context.Background(), router, nil, "arn:aws:iam::000000000000:policy/policy", newProps, oldProps, rCtx)

	// Then: the failure is terminal but clean — the attachment made before the
	// version failure was compensated, so the resource is as it was.
	var failure updateFailure
	if !errors.As(err, &failure) || failure.dirty {
		t.Fatalf("Update() error = %v, want clean updateFailure", err)
	}
	want := []string{"AttachRolePolicy", "CreatePolicyVersion", "DetachRolePolicy"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestIAMRoleCreate_cleansUpRoleWhenAttachmentFails(t *testing.T) {
	// Given: a role whose managed-policy attachment the service refuses.
	var actions []string
	router := iamReconcileTestRouter(&actions, func(action string) int {
		if action == "AttachRolePolicy" {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	})
	props := map[string]any{
		// A trust policy is required (the handler refuses a role without one since
		// #1717); this test is about the attachment failing, not the document.
		"AssumeRolePolicyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Principal": map[string]any{"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}}},
		"RoleName":                 "half-made-role",
		"ManagedPolicyArns":        []any{"arn:aws:iam::aws:policy/ReadOnlyAccess"},
	}
	rCtx := &resolveContext{Region: "us-east-1"}

	// When: Create runs.
	_, _, err := (&iamRoleHandler{}).Create(context.Background(), router, nil, props, rCtx)

	// Then: the failure propagates and the half-created role was deleted
	// rather than left behind with no permissions.
	if err == nil {
		t.Fatal("Create() error = nil, want attachment failure")
	}
	want := []string{"CreateRole", "AttachRolePolicy", "DeleteRole"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
}

func TestIAMValidatePrincipalProperties_rejectsMalformedShapes(t *testing.T) {
	tests := map[string]map[string]any{
		"managed policy arns not a list": {"ManagedPolicyArns": "arn:aws:iam::aws:policy/ReadOnlyAccess"},
		"empty managed policy arn":       {"ManagedPolicyArns": []any{""}},
		"policies entry without name":    {"Policies": []any{map[string]any{"PolicyDocument": map[string]any{}}}},
		"tags entry without value":       {"Tags": []any{map[string]any{"Key": "stage"}}},
		"duplicate tag keys":             {"Tags": []any{map[string]any{"Key": "stage", "Value": "a"}, map[string]any{"Key": "stage", "Value": "b"}}},
		"groups not strings":             {"Groups": []any{1}},
	}
	for name, props := range tests {
		t.Run(name, func(t *testing.T) {
			if err := iamValidatePrincipalProperties(props, true); err == nil {
				t.Fatalf("iamValidatePrincipalProperties(%v) error = nil, want rejection", props)
			}
		})
	}
}

func iamReconcileTestRouter(actions *[]string, statusFor func(action string) int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			panic(err)
		}
		action := r.FormValue("Action")
		*actions = append(*actions, action)
		if status := statusFor(action); status != http.StatusOK {
			w.WriteHeader(status)
			if status == http.StatusNotFound {
				_, _ = w.Write([]byte("<ErrorResponse><Error><Code>NoSuchEntity</Code></Error></ErrorResponse>"))
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
