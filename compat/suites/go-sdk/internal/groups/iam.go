package groups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

func IAM(c *clients.Clients) ServiceGroup {
	g := &iamGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"iam-users:CreateUser":                             g.CreateUser,
			"iam-users:GetUser":                                g.GetUser,
			"iam-users:ListUsers":                              g.ListUsers,
			"iam-users:DeleteUser":                             g.DeleteUser,
			"iam-users:CreateAccessKey":                        g.CreateAccessKey,
			"iam-users:DeleteAccessKey":                        g.DeleteAccessKey,
			"iam-users:PutUserPolicy":                          g.PutUserPolicy,
			"iam-users:GetUserPolicy":                          g.GetUserPolicy,
			"iam-users:DeleteUserPolicy":                       g.DeleteUserPolicy,
			"iam-users:UpdateUser":                             g.UpdateUser,
			"iam-users:ListAccessKeys":                         g.ListAccessKeys,
			"iam-roles:CreateRole":                             g.CreateRole,
			"iam-roles:CreateRoleMalformedDocument":            g.CreateRoleMalformedDocument,
			"iam-roles:GetRole":                                g.GetRole,
			"iam-roles:GetRoleReturnsTags":                     g.GetRoleReturnsTags,
			"iam-roles:ListRoles":                              g.ListRoles,
			"iam-roles:DeleteRole":                             g.DeleteRole,
			"iam-roles:CreateInstanceProfile":                  g.CreateInstanceProfile,
			"iam-roles:AddRoleToInstanceProfile":               g.AddRoleToInstanceProfile,
			"iam-roles:GetInstanceProfile":                     g.GetInstanceProfile,
			"iam-policies:CreatePolicy":                        g.CreatePolicy,
			"iam-policies:CreatePolicyMalformedDocument":       g.CreatePolicyMalformedDocument,
			"iam-policies:GetPolicy":                           g.GetPolicy,
			"iam-policies:GetPolicyReturnsTags":                g.GetPolicyReturnsTags,
			"iam-policies:ListPolicies":                        g.ListPolicies,
			"iam-policies:GetPolicyAttachmentCountAfterAttach": g.GetPolicyAttachmentCountAfterAttach,
			"iam-policies:GetPolicyAttachmentCountAfterDetach": g.GetPolicyAttachmentCountAfterDetach,
			"iam-policies:DeletePolicy":                        g.DeletePolicy,
			"iam-roles:AttachRolePolicy":                       g.AttachRolePolicy,
			"iam-roles:ListAttachedRolePolicies":               g.ListAttachedRolePolicies,
			"iam-roles:DetachRolePolicy":                       g.DetachRolePolicy,
			"iam-roles:PutRolePolicy":                          g.PutRolePolicy,
			"iam-roles:GetRolePolicy":                          g.GetRolePolicy,
			"iam-roles:ListRolePolicies":                       g.ListRolePolicies,
			"iam-roles:DeleteRolePolicy":                       g.DeleteRolePolicy,
			"iam-groups:CreateGroup":                           g.CreateGroup,
			"iam-groups:AddUserToGroup":                        g.AddUserToGroup,
			"iam-groups:ListGroupsForUser":                     g.ListGroupsForUser,
			"iam-groups:RemoveUserFromGroup":                   g.RemoveUserFromGroup,
			"iam-groups:GetGroup":                              g.GetGroup,
			"iam-groups:DeleteGroup":                           g.DeleteGroup,

			"iam-simulate:SimulateCustomPolicyAllowed":         g.SimulateCustomPolicyAllowed,
			"iam-simulate:SimulateCustomPolicyImplicitDeny":    g.SimulateCustomPolicyImplicitDeny,
			"iam-simulate:SimulateCustomPolicyExplicitDeny":    g.SimulateCustomPolicyExplicitDeny,
			"iam-simulate:SimulatePrincipalPolicyAllowed":      g.SimulatePrincipalPolicyAllowed,
			"iam-simulate:SimulatePrincipalPolicyImplicitDeny": g.SimulatePrincipalPolicyImplicitDeny,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"iam-users":    g.setupUsers,
			"iam-roles":    g.setupRoles,
			"iam-policies": g.setupPolicies,
			"iam-groups":   g.setupGroups,
			"iam-simulate": g.setupSimulate,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"iam-users":    g.teardownUsers,
			"iam-roles":    g.teardownRoles,
			"iam-policies": g.teardownPolicies,
			"iam-groups":   g.teardownGroups,
			"iam-simulate": g.teardownSimulate,
		},
	}
}

type iamGroup struct{ c *clients.Clients }

func (g *iamGroup) cl() *iam.Client { return g.c.IAM() }

func iamAssumePolicy() string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":    "Allow",
				"Principal": map[string]interface{}{"Service": "lambda.amazonaws.com"},
				"Action":    "sts:AssumeRole",
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// iamMalformedPolicy is a document AWS refuses with MalformedPolicyDocument:
// "Statements must include either an Action or NotAction element" (IAM User
// Guide, reference_policies_elements_action.html). Every writer that takes a
// document names that error (IAM API Reference, API_CreatePolicy.html and
// API_CreateRole.html, Errors).
const iamMalformedPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Resource":"*"}]}`

// iamResourceTags is the tag set the role and policy fixtures are created with,
// and the one GetRole and GetPolicy must hand back on the resource itself.
func iamResourceTags() []iamtypes.Tag {
	return []iamtypes.Tag{
		{Key: aws.String("owner"), Value: aws.String("compat")},
		{Key: aws.String("stage"), Value: aws.String("dev")},
	}
}

// assertMalformedPolicyDocument checks both halves of the error contract: the
// code AWS's model names, and the 400 the Query protocol binds it to.
func assertMalformedPolicyDocument(op string, err error) error {
	if err == nil {
		return fmt.Errorf("%s: expected MalformedPolicyDocument, got success", op)
	}
	var malformed *iamtypes.MalformedPolicyDocumentException
	if !errors.As(err, &malformed) {
		return fmt.Errorf("%s: expected MalformedPolicyDocument, got %v", op, err)
	}
	var resp *awshttp.ResponseError
	if errors.As(err, &resp) && resp.HTTPStatusCode() != 400 {
		return fmt.Errorf("%s: expected HTTP 400 for MalformedPolicyDocument, got %d", op, resp.HTTPStatusCode())
	}
	return nil
}

// assertResourceTags checks the two fixture tags came back on the resource.
func assertResourceTags(op string, tags []iamtypes.Tag) error {
	got := map[string]string{}
	for _, tag := range tags {
		got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	for _, want := range iamResourceTags() {
		key, value := aws.ToString(want.Key), aws.ToString(want.Value)
		if got[key] != value {
			return fmt.Errorf("%s: tag %s = %q, want %q (tags: %v)", op, key, got[key], value, got)
		}
	}
	return nil
}

// ── iam-users ──────────────────────────────────────────────────────────────────

func (g *iamGroup) setupUsers(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-user-%s", t.RunID)
	resp, err := g.cl().CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)})
	if err != nil {
		return err
	}
	t.Set("iam_user", name)
	t.Set("iam_user_arn", aws.ToString(resp.User.Arn))
	return nil
}

func (g *iamGroup) teardownUsers(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("iam_user"); name != "" {
		g.cl().DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *iamGroup) CreateUser(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cu-%s", t.RunID)
	_, err := g.cl().CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)})
	if err == nil {
		g.cl().DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String(name)}) //nolint:errcheck
	}
	return err
}

func (g *iamGroup) GetUser(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetUser(ctx, &iam.GetUserInput{UserName: aws.String(t.GetString("iam_user"))})
	if err != nil {
		return err
	}
	if aws.ToString(resp.User.UserName) != t.GetString("iam_user") {
		return fmt.Errorf("GetUser name mismatch")
	}
	return nil
}

func (g *iamGroup) ListUsers(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListUsers(ctx, &iam.ListUsersInput{})
	return err
}

func (g *iamGroup) UpdateUser(ctx context.Context, t *harness.TestContext) error {
	old := t.GetString("iam_user")
	newName := fmt.Sprintf("oc-upd-%s", t.RunID)
	if _, err := g.cl().UpdateUser(ctx, &iam.UpdateUserInput{
		UserName:    aws.String(old),
		NewUserName: aws.String(newName),
	}); err != nil {
		return err
	}
	// rename back
	g.cl().UpdateUser(ctx, &iam.UpdateUserInput{UserName: aws.String(newName), NewUserName: aws.String(old)}) //nolint:errcheck
	return nil
}

func (g *iamGroup) DeleteUser(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-del-u-%s", t.RunID)
	g.cl().CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}) //nolint:errcheck
	_, err := g.cl().DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String(name)})
	return err
}

func (g *iamGroup) CreateAccessKey(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateAccessKey(ctx, &iam.CreateAccessKeyInput{
		UserName: aws.String(t.GetString("iam_user")),
	})
	if err != nil {
		return err
	}
	if resp.AccessKey == nil || resp.AccessKey.AccessKeyId == nil {
		return fmt.Errorf("CreateAccessKey: missing AccessKeyId")
	}
	t.Set("iam_ak_id", aws.ToString(resp.AccessKey.AccessKeyId))
	return nil
}

func (g *iamGroup) DeleteAccessKey(ctx context.Context, t *harness.TestContext) error {
	akID := t.GetString("iam_ak_id")
	if akID == "" {
		return fmt.Errorf("DeleteAccessKey: no AccessKeyId from CreateAccessKey")
	}
	_, err := g.cl().DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
		UserName:    aws.String(t.GetString("iam_user")),
		AccessKeyId: aws.String(akID),
	})
	return err
}

func (g *iamGroup) PutUserPolicy(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       aws.String(t.GetString("iam_user")),
		PolicyName:     aws.String("inline-policy"),
		PolicyDocument: aws.String(iamPolicyDoc()),
	})
	return err
}

func (g *iamGroup) GetUserPolicy(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetUserPolicy(ctx, &iam.GetUserPolicyInput{
		UserName:   aws.String(t.GetString("iam_user")),
		PolicyName: aws.String("inline-policy"),
	})
	if err != nil {
		return err
	}
	if resp.PolicyDocument == nil {
		return fmt.Errorf("GetUserPolicy: missing PolicyDocument")
	}
	return nil
}

func (g *iamGroup) DeleteUserPolicy(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DeleteUserPolicy(ctx, &iam.DeleteUserPolicyInput{
		UserName:   aws.String(t.GetString("iam_user")),
		PolicyName: aws.String("inline-policy"),
	})
	return err
}

func (g *iamGroup) ListAccessKeys(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListAccessKeys(ctx, &iam.ListAccessKeysInput{
		UserName: aws.String(t.GetString("iam_user")),
	})
	return err
}

// ── iam-roles ──────────────────────────────────────────────────────────────────

func (g *iamGroup) setupRoles(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-role-%s", t.RunID)
	resp, err := g.cl().CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(name),
		AssumeRolePolicyDocument: aws.String(iamAssumePolicy()),
		Tags:                     iamResourceTags(),
	})
	if err != nil {
		return err
	}
	t.Set("iam_role", name)
	t.Set("iam_role_arn", aws.ToString(resp.Role.Arn))
	return nil
}

func (g *iamGroup) teardownRoles(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("iam_role"); name != "" {
		g.cl().DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{RoleName: aws.String(name), PolicyName: aws.String("inline-role-policy")}) //nolint:errcheck
		g.cl().DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(name)})                                                           //nolint:errcheck
	}
	return nil
}

func (g *iamGroup) CreateRole(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cr-%s", t.RunID)
	_, err := g.cl().CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(name),
		AssumeRolePolicyDocument: aws.String(iamAssumePolicy()),
	})
	if err == nil {
		g.cl().DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(name)}) //nolint:errcheck
	}
	return err
}

func (g *iamGroup) GetRole(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(t.GetString("iam_role"))})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Role.RoleName) != t.GetString("iam_role") {
		return fmt.Errorf("GetRole name mismatch")
	}
	return nil
}

// CreateRoleMalformedDocument pins that a trust policy AWS would refuse is
// refused here too, rather than stored unparsed.
func (g *iamGroup) CreateRoleMalformedDocument(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-crmd-%s", t.RunID)
	_, err := g.cl().CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(name),
		AssumeRolePolicyDocument: aws.String(iamMalformedPolicy),
	})
	if err == nil {
		g.cl().DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(name)}) //nolint:errcheck
	}
	return assertMalformedPolicyDocument("CreateRoleMalformedDocument", err)
}

// GetRoleReturnsTags pins that Tags supplied to CreateRole come back on the
// role itself, which is where AWS documents them (API_Role.html).
func (g *iamGroup) GetRoleReturnsTags(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(t.GetString("iam_role"))})
	if err != nil {
		return err
	}
	if resp.Role == nil {
		return fmt.Errorf("GetRoleReturnsTags: missing Role")
	}
	return assertResourceTags("GetRoleReturnsTags", resp.Role.Tags)
}

func (g *iamGroup) ListRoles(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListRoles(ctx, &iam.ListRolesInput{})
	return err
}

func (g *iamGroup) UpdateRole(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().UpdateRole(ctx, &iam.UpdateRoleInput{
		RoleName:    aws.String(t.GetString("iam_role")),
		Description: aws.String("updated"),
	})
	return err
}

func (g *iamGroup) DeleteRole(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-dr-%s", t.RunID)
	g.cl().CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String(name), AssumeRolePolicyDocument: aws.String(iamAssumePolicy()),
	}) //nolint:errcheck
	_, err := g.cl().DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(name)})
	return err
}

func (g *iamGroup) PutRolePolicy(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(t.GetString("iam_role")),
		PolicyName:     aws.String("inline-role-policy"),
		PolicyDocument: aws.String(iamPolicyDoc()),
	})
	return err
}

func (g *iamGroup) GetRolePolicy(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetRolePolicy(ctx, &iam.GetRolePolicyInput{
		RoleName:   aws.String(t.GetString("iam_role")),
		PolicyName: aws.String("inline-role-policy"),
	})
	if err != nil {
		return err
	}
	if resp.PolicyDocument == nil {
		return fmt.Errorf("GetRolePolicy: missing PolicyDocument")
	}
	return nil
}

func (g *iamGroup) ListRolePolicies(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
		RoleName: aws.String(t.GetString("iam_role")),
	})
	if err != nil {
		return err
	}
	for _, name := range resp.PolicyNames {
		if name == "inline-role-policy" {
			return nil
		}
	}
	return fmt.Errorf("ListRolePolicies: inline-role-policy not found")
}

func (g *iamGroup) DeleteRolePolicy(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
		RoleName:   aws.String(t.GetString("iam_role")),
		PolicyName: aws.String("inline-role-policy"),
	})
	return err
}

func (g *iamGroup) CreateInstanceProfile(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-profile", t.RunID)
	_, err := g.cl().CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(name),
	})
	if err != nil {
		return err
	}
	t.Set("iam_instance_profile", name)
	return nil
}

func (g *iamGroup) AddRoleToInstanceProfile(ctx context.Context, t *harness.TestContext) error {
	profileName := t.GetString("iam_instance_profile")
	if profileName == "" {
		return fmt.Errorf("AddRoleToInstanceProfile: no profile from CreateInstanceProfile")
	}
	_, err := g.cl().AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(t.GetString("iam_role")),
	})
	return err
}

func (g *iamGroup) GetInstanceProfile(ctx context.Context, t *harness.TestContext) error {
	profileName := t.GetString("iam_instance_profile")
	if profileName == "" {
		return fmt.Errorf("GetInstanceProfile: no profile from CreateInstanceProfile")
	}
	resp, err := g.cl().GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		return err
	}
	if resp.InstanceProfile == nil {
		return fmt.Errorf("GetInstanceProfile: missing InstanceProfile")
	}
	return nil
}

// ── iam-policies ──────────────────────────────────────────────────────────────

func iamPolicyDoc() string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{"Effect": "Allow", "Action": []string{"s3:GetObject"}, "Resource": "*"},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func (g *iamGroup) setupPolicies(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-pol-%s", t.RunID)
	resp, err := g.cl().CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String(name),
		PolicyDocument: aws.String(iamPolicyDoc()),
		Tags:           iamResourceTags(),
	})
	if err != nil {
		return err
	}
	t.Set("iam_policy_arn", aws.ToString(resp.Policy.Arn))
	t.Set("iam_policy_name", name)

	// The counter tests attach this group's own policy to a role of its own, so
	// AttachmentCount moves for a customer managed policy rather than for the
	// AWS managed one iam-roles attaches.
	roleName := fmt.Sprintf("oc-polrole-%s", t.RunID)
	if _, err := g.cl().CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String(roleName), AssumeRolePolicyDocument: aws.String(iamAssumePolicy()),
	}); err != nil {
		return err
	}
	t.Set("iam_policy_role", roleName)
	return nil
}

func (g *iamGroup) teardownPolicies(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("iam_policy_arn")
	if arn == "" {
		return nil
	}
	// detach from all roles first
	if role := t.GetString("iam_policy_role"); role != "" {
		g.cl().DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName: aws.String(role), PolicyArn: aws.String(arn),
		}) //nolint:errcheck
		g.cl().DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(role)}) //nolint:errcheck
	}
	if role := t.GetString("iam_attach_role"); role != "" {
		g.cl().DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName: aws.String(role), PolicyArn: aws.String(arn),
		}) //nolint:errcheck
		g.cl().DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(role)}) //nolint:errcheck
	}
	g.cl().DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: aws.String(arn)}) //nolint:errcheck
	return nil
}

func (g *iamGroup) CreatePolicy(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cp-%s", t.RunID)
	resp, err := g.cl().CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String(name),
		PolicyDocument: aws.String(iamPolicyDoc()),
	})
	if err == nil {
		g.cl().DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: resp.Policy.Arn}) //nolint:errcheck
	}
	return err
}

func (g *iamGroup) GetPolicy(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("iam_policy_arn")
	resp, err := g.cl().GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(arn)})
	if err != nil {
		return err
	}
	if resp.Policy == nil || aws.ToString(resp.Policy.Arn) != arn {
		return fmt.Errorf("GetPolicy: wrong ARN %v", resp.Policy)
	}
	return nil
}

func (g *iamGroup) ListPolicies(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListPolicies(ctx, &iam.ListPoliciesInput{Scope: "Local"})
	return err
}

// CreatePolicyMalformedDocument pins the same refusal on the identity-policy
// writer.
func (g *iamGroup) CreatePolicyMalformedDocument(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cpmd-%s", t.RunID)
	resp, err := g.cl().CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String(name),
		PolicyDocument: aws.String(iamMalformedPolicy),
	})
	if err == nil && resp.Policy != nil {
		g.cl().DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: resp.Policy.Arn}) //nolint:errcheck
	}
	return assertMalformedPolicyDocument("CreatePolicyMalformedDocument", err)
}

// GetPolicyReturnsTags pins Tags on the policy resource (API_Policy.html).
func (g *iamGroup) GetPolicyReturnsTags(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(t.GetString("iam_policy_arn"))})
	if err != nil {
		return err
	}
	if resp.Policy == nil {
		return fmt.Errorf("GetPolicyReturnsTags: missing Policy")
	}
	return assertResourceTags("GetPolicyReturnsTags", resp.Policy.Tags)
}

// policyAttachmentCount reads AttachmentCount back through GetPolicy.
func (g *iamGroup) policyAttachmentCount(ctx context.Context, t *harness.TestContext, op string) (int32, error) {
	resp, err := g.cl().GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(t.GetString("iam_policy_arn"))})
	if err != nil {
		return 0, err
	}
	if resp.Policy == nil || resp.Policy.AttachmentCount == nil {
		return 0, fmt.Errorf("%s: GetPolicy returned no AttachmentCount", op)
	}
	return *resp.Policy.AttachmentCount, nil
}

// GetPolicyAttachmentCountAfterAttach pins AttachmentCount moving 0 to 1.
// "The number of entities (users, groups, and roles) that the policy is
// attached to" (IAM API Reference, API_Policy.html) is what a cleanup script
// reads before deleting a policy, so a stuck 0 deletes something in use.
func (g *iamGroup) GetPolicyAttachmentCountAfterAttach(ctx context.Context, t *harness.TestContext) error {
	const op = "GetPolicyAttachmentCountAfterAttach"
	before, err := g.policyAttachmentCount(ctx, t, op)
	if err != nil {
		return err
	}
	if before != 0 {
		return fmt.Errorf("%s: AttachmentCount = %d before the attach, want 0", op, before)
	}
	role := t.GetString("iam_policy_role")
	if role == "" {
		return fmt.Errorf("%s: no role from setup", op)
	}
	if _, err := g.cl().AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName: aws.String(role), PolicyArn: aws.String(t.GetString("iam_policy_arn")),
	}); err != nil {
		return err
	}
	after, err := g.policyAttachmentCount(ctx, t, op)
	if err != nil {
		return err
	}
	if after != 1 {
		return fmt.Errorf("%s: AttachmentCount = %d after attaching to one role, want 1", op, after)
	}
	return nil
}

// GetPolicyAttachmentCountAfterDetach pins the counter moving back to 0, which
// a counter incremented but never decremented would fail.
func (g *iamGroup) GetPolicyAttachmentCountAfterDetach(ctx context.Context, t *harness.TestContext) error {
	const op = "GetPolicyAttachmentCountAfterDetach"
	role := t.GetString("iam_policy_role")
	if role == "" {
		return fmt.Errorf("%s: no role from setup", op)
	}
	if _, err := g.cl().DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName: aws.String(role), PolicyArn: aws.String(t.GetString("iam_policy_arn")),
	}); err != nil {
		return err
	}
	after, err := g.policyAttachmentCount(ctx, t, op)
	if err != nil {
		return err
	}
	if after != 0 {
		return fmt.Errorf("%s: AttachmentCount = %d after the detach, want 0", op, after)
	}
	return nil
}

// iamManagedPolicy is an AWS-managed policy that always exists; used to
// test Attach/DetachRolePolicy without depending on a customer-managed policy
// created by a different test group.
const iamManagedPolicy = "arn:aws:iam::aws:policy/ReadOnlyAccess"

func (g *iamGroup) AttachRolePolicy(ctx context.Context, t *harness.TestContext) error {
	roleName := fmt.Sprintf("oc-ar-%s", t.RunID)
	g.cl().CreateRole(ctx, &iam.CreateRoleInput{
		RoleName: aws.String(roleName), AssumeRolePolicyDocument: aws.String(iamAssumePolicy()),
	}) //nolint:errcheck
	t.Set("iam_attach_role", roleName)
	_, err := g.cl().AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String(iamManagedPolicy),
	})
	return err
}

func (g *iamGroup) ListAttachedRolePolicies(ctx context.Context, t *harness.TestContext) error {
	role := t.GetString("iam_attach_role")
	if role == "" {
		return nil
	}
	_, err := g.cl().ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(role),
	})
	return err
}

func (g *iamGroup) DetachRolePolicy(ctx context.Context, t *harness.TestContext) error {
	role := t.GetString("iam_attach_role")
	if role == "" {
		return nil
	}
	_, err := g.cl().DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  aws.String(role),
		PolicyArn: aws.String(iamManagedPolicy),
	})
	return err
}

func (g *iamGroup) DeletePolicy(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("iam_policy_arn")
	if arn == "" {
		return fmt.Errorf("DeletePolicy: no policy ARN")
	}
	_, err := g.cl().DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: aws.String(arn)})
	return err
}

// ── iam-groups ─────────────────────────────────────────────────────────────────

func (g *iamGroup) setupGroups(ctx context.Context, t *harness.TestContext) error {
	groupName := fmt.Sprintf("oc-grp-%s", t.RunID)
	if _, err := g.cl().CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String(groupName)}); err != nil {
		return err
	}
	userName := fmt.Sprintf("oc-gu-%s", t.RunID)
	if _, err := g.cl().CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(userName)}); err != nil {
		return err
	}
	t.Set("iam_grp", groupName)
	t.Set("iam_grp_user", userName)
	return nil
}

func (g *iamGroup) teardownGroups(ctx context.Context, t *harness.TestContext) error {
	user := t.GetString("iam_grp_user")
	group := t.GetString("iam_grp")
	if user != "" && group != "" {
		g.cl().RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{
			GroupName: aws.String(group), UserName: aws.String(user),
		}) //nolint:errcheck
		g.cl().DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String(user)}) //nolint:errcheck
	}
	if group != "" {
		g.cl().DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String(group)}) //nolint:errcheck
	}
	return nil
}

func (g *iamGroup) CreateGroup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-cg-%s", t.RunID)
	_, err := g.cl().CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String(name)})
	if err == nil {
		g.cl().DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String(name)}) //nolint:errcheck
	}
	return err
}

func (g *iamGroup) ListGroups(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListGroups(ctx, &iam.ListGroupsInput{})
	return err
}

func (g *iamGroup) AddUserToGroup(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().AddUserToGroup(ctx, &iam.AddUserToGroupInput{
		GroupName: aws.String(t.GetString("iam_grp")),
		UserName:  aws.String(t.GetString("iam_grp_user")),
	})
	return err
}

func (g *iamGroup) ListGroupsForUser(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListGroupsForUser(ctx, &iam.ListGroupsForUserInput{
		UserName: aws.String(t.GetString("iam_grp_user")),
	})
	if err != nil {
		return err
	}
	grpName := t.GetString("iam_grp")
	for _, grp := range resp.Groups {
		if aws.ToString(grp.GroupName) == grpName {
			return nil
		}
	}
	return fmt.Errorf("ListGroupsForUser: group %s not found", grpName)
}

func (g *iamGroup) RemoveUserFromGroup(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{
		GroupName: aws.String(t.GetString("iam_grp")),
		UserName:  aws.String(t.GetString("iam_grp_user")),
	})
	return err
}

func (g *iamGroup) GetGroup(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetGroup(ctx, &iam.GetGroupInput{
		GroupName: aws.String(t.GetString("iam_grp")),
	})
	if err != nil {
		return err
	}
	if resp.Group == nil || aws.ToString(resp.Group.GroupName) != t.GetString("iam_grp") {
		return fmt.Errorf("GetGroup: name mismatch")
	}
	return nil
}

func (g *iamGroup) DeleteGroup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-dg-%s", t.RunID)
	g.cl().CreateGroup(ctx, &iam.CreateGroupInput{GroupName: aws.String(name)}) //nolint:errcheck
	_, err := g.cl().DeleteGroup(ctx, &iam.DeleteGroupInput{GroupName: aws.String(name)})
	return err
}

// ── iam-simulate ───────────────────────────────────────────────────────────────

// iamSimPolicy is the identity policy the simulate group evaluates: read access
// to one run-scoped bucket prefix and nothing else.
func iamSimPolicy(runID string) string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   "s3:GetObject",
				"Resource": fmt.Sprintf("arn:aws:s3:::oc-sim-%s/*", runID),
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func (g *iamGroup) setupSimulate(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-sim-user-%s", t.RunID)
	if _, err := g.cl().CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}); err != nil {
		return err
	}
	if _, err := g.cl().PutUserPolicy(ctx, &iam.PutUserPolicyInput{
		UserName:       aws.String(name),
		PolicyName:     aws.String("sim-allow-read"),
		PolicyDocument: aws.String(iamSimPolicy(t.RunID)),
	}); err != nil {
		return err
	}
	t.Set("iam_sim_user", name)
	return nil
}

func (g *iamGroup) teardownSimulate(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("iam_sim_user")
	if name == "" {
		return nil
	}
	g.cl().DeleteUserPolicy(ctx, &iam.DeleteUserPolicyInput{ //nolint:errcheck
		UserName: aws.String(name), PolicyName: aws.String("sim-allow-read"),
	})
	g.cl().DeleteUser(ctx, &iam.DeleteUserInput{UserName: aws.String(name)}) //nolint:errcheck
	return nil
}

func (g *iamGroup) simResource(t *harness.TestContext) string {
	return fmt.Sprintf("arn:aws:s3:::oc-sim-%s/report.csv", t.RunID)
}

func (g *iamGroup) SimulateCustomPolicyAllowed(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().SimulateCustomPolicy(ctx, &iam.SimulateCustomPolicyInput{
		PolicyInputList: []string{iamSimPolicy(t.RunID)},
		ActionNames:     []string{"s3:GetObject"},
		ResourceArns:    []string{g.simResource(t)},
	})
	if err != nil {
		return err
	}
	if len(resp.EvaluationResults) == 0 {
		return fmt.Errorf("SimulateCustomPolicy: missing EvaluationResults")
	}
	if got := string(resp.EvaluationResults[0].EvalDecision); got != "allowed" {
		return fmt.Errorf("SimulateCustomPolicy: EvalDecision = %s, want allowed", got)
	}
	if got := aws.ToString(resp.EvaluationResults[0].EvalActionName); got != "s3:GetObject" {
		return fmt.Errorf("SimulateCustomPolicy: EvalActionName = %s, want s3:GetObject", got)
	}
	return nil
}

func (g *iamGroup) SimulateCustomPolicyImplicitDeny(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().SimulateCustomPolicy(ctx, &iam.SimulateCustomPolicyInput{
		PolicyInputList: []string{iamSimPolicy(t.RunID)},
		ActionNames:     []string{"s3:PutObject"},
		ResourceArns:    []string{g.simResource(t)},
	})
	if err != nil {
		return err
	}
	if len(resp.EvaluationResults) == 0 {
		return fmt.Errorf("SimulateCustomPolicy: missing EvaluationResults")
	}
	if got := string(resp.EvaluationResults[0].EvalDecision); got != "implicitDeny" {
		return fmt.Errorf("SimulateCustomPolicy: EvalDecision = %s, want implicitDeny", got)
	}
	return nil
}

func (g *iamGroup) SimulateCustomPolicyExplicitDeny(ctx context.Context, t *harness.TestContext) error {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
			{"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"},
		},
	}
	raw, _ := json.Marshal(doc)
	resp, err := g.cl().SimulateCustomPolicy(ctx, &iam.SimulateCustomPolicyInput{
		PolicyInputList: []string{string(raw)},
		ActionNames:     []string{"s3:DeleteObject"},
		ResourceArns:    []string{g.simResource(t)},
	})
	if err != nil {
		return err
	}
	if len(resp.EvaluationResults) == 0 {
		return fmt.Errorf("SimulateCustomPolicy: missing EvaluationResults")
	}
	if got := string(resp.EvaluationResults[0].EvalDecision); got != "explicitDeny" {
		return fmt.Errorf("SimulateCustomPolicy: EvalDecision = %s, want explicitDeny", got)
	}
	return nil
}

func (g *iamGroup) SimulatePrincipalPolicyAllowed(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(fmt.Sprintf("arn:aws:iam::000000000000:user/%s", t.GetString("iam_sim_user"))),
		ActionNames:     []string{"s3:GetObject"},
		ResourceArns:    []string{g.simResource(t)},
	})
	if err != nil {
		return err
	}
	if len(resp.EvaluationResults) == 0 {
		return fmt.Errorf("SimulatePrincipalPolicy: missing EvaluationResults")
	}
	result := resp.EvaluationResults[0]
	if got := string(result.EvalDecision); got != "allowed" {
		return fmt.Errorf("SimulatePrincipalPolicy: EvalDecision = %s, want allowed", got)
	}
	for _, m := range result.MatchedStatements {
		if aws.ToString(m.SourcePolicyId) == "sim-allow-read" {
			return nil
		}
	}
	return fmt.Errorf("SimulatePrincipalPolicy: MatchedStatements missing sim-allow-read")
}

func (g *iamGroup) SimulatePrincipalPolicyImplicitDeny(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(fmt.Sprintf("arn:aws:iam::000000000000:user/%s", t.GetString("iam_sim_user"))),
		ActionNames:     []string{"s3:DeleteObject"},
		ResourceArns:    []string{g.simResource(t)},
	})
	if err != nil {
		return err
	}
	if len(resp.EvaluationResults) == 0 {
		return fmt.Errorf("SimulatePrincipalPolicy: missing EvaluationResults")
	}
	if got := string(resp.EvaluationResults[0].EvalDecision); got != "implicitDeny" {
		return fmt.Errorf("SimulatePrincipalPolicy: EvalDecision = %s, want implicitDeny", got)
	}
	return nil
}
