package groups

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

func IAM(c *clients.Clients) ServiceGroup {
	g := &iamGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateUser":               g.CreateUser,
			"GetUser":                  g.GetUser,
			"ListUsers":                g.ListUsers,
			"DeleteUser":               g.DeleteUser,
			"CreateAccessKey":          g.CreateAccessKey,
			"DeleteAccessKey":          g.DeleteAccessKey,
			"PutUserPolicy":            g.PutUserPolicy,
			"GetUserPolicy":            g.GetUserPolicy,
			"DeleteUserPolicy":         g.DeleteUserPolicy,
			"UpdateUser":               g.UpdateUser,
			"ListAccessKeys":           g.ListAccessKeys,
			"CreateRole":               g.CreateRole,
			"GetRole":                  g.GetRole,
			"ListRoles":                g.ListRoles,
			"DeleteRole":               g.DeleteRole,
			"CreateInstanceProfile":    g.CreateInstanceProfile,
			"AddRoleToInstanceProfile": g.AddRoleToInstanceProfile,
			"GetInstanceProfile":       g.GetInstanceProfile,
			"CreatePolicy":             g.CreatePolicy,
			"GetPolicy":                g.GetPolicy,
			"ListPolicies":             g.ListPolicies,
			"DeletePolicy":             g.DeletePolicy,
			"AttachRolePolicy":         g.AttachRolePolicy,
			"ListAttachedRolePolicies": g.ListAttachedRolePolicies,
			"DetachRolePolicy":         g.DetachRolePolicy,
			"PutRolePolicy":            g.PutRolePolicy,
			"GetRolePolicy":            g.GetRolePolicy,
			"ListRolePolicies":         g.ListRolePolicies,
			"DeleteRolePolicy":         g.DeleteRolePolicy,
			"CreateGroup":              g.CreateGroup,
			"AddUserToGroup":           g.AddUserToGroup,
			"ListGroupsForUser":        g.ListGroupsForUser,
			"RemoveUserFromGroup":      g.RemoveUserFromGroup,
			"GetGroup":                 g.GetGroup,
			"DeleteGroup":              g.DeleteGroup,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"iam-users":    g.setupUsers,
			"iam-roles":    g.setupRoles,
			"iam-policies": g.setupPolicies,
			"iam-groups":   g.setupGroups,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"iam-users":    g.teardownUsers,
			"iam-roles":    g.teardownRoles,
			"iam-policies": g.teardownPolicies,
			"iam-groups":   g.teardownGroups,
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
	})
	if err != nil {
		return err
	}
	t.Set("iam_policy_arn", aws.ToString(resp.Policy.Arn))
	t.Set("iam_policy_name", name)
	return nil
}

func (g *iamGroup) teardownPolicies(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("iam_policy_arn")
	if arn == "" {
		return nil
	}
	// detach from all roles first
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
	resp, err := g.cl().GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(t.GetString("iam_policy_arn"))})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

func (g *iamGroup) ListPolicies(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListPolicies(ctx, &iam.ListPoliciesInput{Scope: "Local"})
	return err
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
