package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// IAM returns the IAM service group.
func IAM() ServiceGroup {
	g := &iamGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// iam-users
			"CreateUser":       g.CreateUser,
			"GetUser":          g.GetUser,
			"ListUsers":        g.ListUsers,
			"iam-users:ListUsers": g.ListUsers, // group-qualified to avoid collision with cognito-userpools:ListUsers
			"CreateAccessKey":  g.CreateAccessKey,
			"DeleteAccessKey":  g.DeleteAccessKey,
			"PutUserPolicy":    g.PutUserPolicy,
			"GetUserPolicy":    g.GetUserPolicy,
			"DeleteUserPolicy": g.DeleteUserPolicy,
			"UpdateUser":       g.UpdateUser,
			"ListAccessKeys":   g.ListAccessKeys,
			"DeleteUser":       g.DeleteUser,
			// iam-roles
			"CreateRole":               g.CreateRole,
			"GetRole":                  g.GetRole,
			"ListRoles":                g.ListRoles,
			"AttachRolePolicy":         g.AttachRolePolicy,
			"ListAttachedRolePolicies": g.ListAttachedRolePolicies,
			"DetachRolePolicy":         g.DetachRolePolicy,
			"PutRolePolicy":            g.PutRolePolicy,
			"GetRolePolicy":            g.GetRolePolicy,
			"ListRolePolicies":         g.ListRolePolicies,
			"DeleteRolePolicy":         g.DeleteRolePolicy,
			"CreateInstanceProfile":    g.CreateInstanceProfile,
			"AddRoleToInstanceProfile": g.AddRoleToInstanceProfile,
			"GetInstanceProfile":       g.GetInstanceProfile,
			"DeleteRole":               g.DeleteRole,
			// iam-policies
			"CreatePolicy": g.CreatePolicy,
			"GetPolicy":    g.GetPolicy,
			"ListPolicies": g.ListPolicies,
			"DeletePolicy": g.DeletePolicy,
			// iam-groups
			"CreateGroup":         g.CreateGroup,
			"AddUserToGroup":      g.AddUserToGroup,
			"ListGroupsForUser":   g.ListGroupsForUser,
			"RemoveUserFromGroup": g.RemoveUserFromGroup,
			"GetGroup":            g.GetGroup,
			"DeleteGroup":         g.DeleteGroup,
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
			"iam-groups":   g.teardownIAMGroups,
		},
	}
}

// One Namer per IAM resource type keeps names deterministic and descriptive.
var (
	iamUserNamer    = harness.NewNamer("iam-usr")
	iamRoleNamer    = harness.NewNamer("iam-rol")
	iamProfileNamer = harness.NewNamer("iam-prof")
	iamPolicyNamer  = harness.NewNamer("iam-pol")
	iamGroupNamer   = harness.NewNamer("iam-grp")
	iamPolRoleNamer = harness.NewNamer("iam-pr") // separate role for iam-policies group
	iamGrpUserNamer = harness.NewNamer("iam-gu") // separate user for iam-groups group
)

type iamGroup struct{}

func (g *iamGroup) assumePolicy() string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
}

func (g *iamGroup) s3PolicyDoc() string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject"],"Resource":"*"}]}`
}

// ─── iam-users ───────────────────────────────────────────────────────────────

func (g *iamGroup) setupUsers(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *iamGroup) CreateUser(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-user",
		"--user-name", iamUserNamer.Name(t),
	)
	if err != nil && isAlreadyExists(err) {
		return nil // idempotent — resource from a previous run
	}
	return err
}

func (g *iamGroup) GetUser(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-user",
		"--user-name", iamUserNamer.Name(t),
	)
	return err
}

func (g *iamGroup) ListUsers(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "iam", "list-users")
	return err
}

func (g *iamGroup) CreateAccessKey(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-access-key",
		"--user-name", iamUserNamer.Name(t),
	)
	if err != nil {
		return err
	}
	ak, _ := out["AccessKey"].(map[string]any)
	keyID, _ := ak["AccessKeyId"].(string)
	t.Set("access_key_id", keyID)
	return nil
}

func (g *iamGroup) DeleteAccessKey(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("access_key_id")
	if keyID == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "delete-access-key",
		"--user-name", iamUserNamer.Name(t),
		"--access-key-id", keyID,
	)
}

func (g *iamGroup) PutUserPolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "put-user-policy",
		"--user-name", iamUserNamer.Name(t),
		"--policy-name", "inline-policy",
		"--policy-document", g.s3PolicyDoc(),
	)
}

func (g *iamGroup) GetUserPolicy(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-user-policy",
		"--user-name", iamUserNamer.Name(t),
		"--policy-name", "inline-policy",
	)
	return err
}

func (g *iamGroup) DeleteUserPolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "delete-user-policy",
		"--user-name", iamUserNamer.Name(t),
		"--policy-name", "inline-policy",
	)
}

func (g *iamGroup) UpdateUser(_ context.Context, t *harness.TestContext) error {
	old := iamUserNamer.Name(t)
	newName := old + "-upd"
	if err := awscli.Run(t.Endpoint, t.Region,
		"iam", "update-user",
		"--user-name", old,
		"--new-user-name", newName,
	); err != nil {
		return err
	}
	// Rename back so teardown works.
	awscli.Run(t.Endpoint, t.Region, "iam", "update-user", //nolint:errcheck
		"--user-name", newName, "--new-user-name", old)
	return nil
}

func (g *iamGroup) ListAccessKeys(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "list-access-keys",
		"--user-name", iamUserNamer.Name(t),
	)
	if err != nil {
		return err
	}
	_ = out["AccessKeyMetadata"]
	return nil
}

func (g *iamGroup) DeleteUser(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "delete-user",
		"--user-name", iamUserNamer.Name(t),
	)
}

func (g *iamGroup) teardownUsers(_ context.Context, t *harness.TestContext) error {
	// Best-effort cleanup — delete inline policies and access keys first.
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-user-policy", //nolint:errcheck
		"--user-name", iamUserNamer.Name(t), "--policy-name", "inline-policy")
	if keyID := t.GetString("access_key_id"); keyID != "" {
		awscli.Run(t.Endpoint, t.Region, "iam", "delete-access-key", //nolint:errcheck
			"--user-name", iamUserNamer.Name(t), "--access-key-id", keyID)
	}
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-user", "--user-name", iamUserNamer.Name(t)) //nolint:errcheck
	return nil
}

// ─── iam-roles ───────────────────────────────────────────────────────────────

func (g *iamGroup) setupRoles(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *iamGroup) CreateRole(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-role",
		"--role-name", iamRoleNamer.Name(t),
		"--assume-role-policy-document", g.assumePolicy(),
	)
	if err != nil {
		if isAlreadyExists(err) {
			// Role exists from a previous run — fetch its ARN so tests can proceed.
			out, err = awscli.RunOutput(t.Endpoint, t.Region,
				"iam", "get-role",
				"--role-name", iamRoleNamer.Name(t),
			)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	role, _ := out["Role"].(map[string]any)
	arn, _ := role["Arn"].(string)
	t.Set("role_arn", arn)
	return nil
}

func (g *iamGroup) GetRole(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-role",
		"--role-name", iamRoleNamer.Name(t),
	)
	return err
}

func (g *iamGroup) ListRoles(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "iam", "list-roles")
	return err
}

func (g *iamGroup) AttachRolePolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "attach-role-policy",
		"--role-name", iamRoleNamer.Name(t),
		"--policy-arn", "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
	)
}

func (g *iamGroup) ListAttachedRolePolicies(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "list-attached-role-policies",
		"--role-name", iamRoleNamer.Name(t),
	)
	return err
}

func (g *iamGroup) DetachRolePolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "detach-role-policy",
		"--role-name", iamRoleNamer.Name(t),
		"--policy-arn", "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
	)
}

func (g *iamGroup) CreateInstanceProfile(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-instance-profile",
		"--instance-profile-name", iamProfileNamer.Name(t),
	)
	if err != nil && isAlreadyExists(err) {
		return nil // idempotent
	}
	return err
}

func (g *iamGroup) AddRoleToInstanceProfile(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "add-role-to-instance-profile",
		"--instance-profile-name", iamProfileNamer.Name(t),
		"--role-name", iamRoleNamer.Name(t),
	)
}

func (g *iamGroup) GetInstanceProfile(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-instance-profile",
		"--instance-profile-name", iamProfileNamer.Name(t),
	)
	return err
}

func (g *iamGroup) PutRolePolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "put-role-policy",
		"--role-name", iamRoleNamer.Name(t),
		"--policy-name", "inline-role-policy",
		"--policy-document", g.s3PolicyDoc(),
	)
}

func (g *iamGroup) GetRolePolicy(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-role-policy",
		"--role-name", iamRoleNamer.Name(t),
		"--policy-name", "inline-role-policy",
	)
	return err
}

func (g *iamGroup) ListRolePolicies(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "list-role-policies",
		"--role-name", iamRoleNamer.Name(t),
	)
	if err != nil {
		return err
	}
	names, _ := out["PolicyNames"].([]any)
	for _, v := range names {
		if v == "inline-role-policy" {
			return nil
		}
	}
	return fmt.Errorf("iam ListRolePolicies: inline-role-policy not found")
}

func (g *iamGroup) DeleteRolePolicy(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "delete-role-policy",
		"--role-name", iamRoleNamer.Name(t),
		"--policy-name", "inline-role-policy",
	)
}

func (g *iamGroup) DeleteRole(_ context.Context, t *harness.TestContext) error {
	// Remove role from any instance profiles first.
	awscli.Run(t.Endpoint, t.Region, "iam", "remove-role-from-instance-profile", //nolint:errcheck
		"--instance-profile-name", iamProfileNamer.Name(t),
		"--role-name", iamRoleNamer.Name(t),
	)
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-instance-profile", //nolint:errcheck
		"--instance-profile-name", iamProfileNamer.Name(t),
	)
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "delete-role",
		"--role-name", iamRoleNamer.Name(t),
	)
}

func (g *iamGroup) teardownRoles(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-role-policy", //nolint:errcheck
		"--role-name", iamRoleNamer.Name(t), "--policy-name", "inline-role-policy")
	awscli.Run(t.Endpoint, t.Region, "iam", "detach-role-policy", //nolint:errcheck
		"--role-name", iamRoleNamer.Name(t),
		"--policy-arn", "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
	)
	awscli.Run(t.Endpoint, t.Region, "iam", "remove-role-from-instance-profile", //nolint:errcheck
		"--instance-profile-name", iamProfileNamer.Name(t), "--role-name", iamRoleNamer.Name(t),
	)
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-instance-profile", //nolint:errcheck
		"--instance-profile-name", iamProfileNamer.Name(t))
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-role", "--role-name", iamRoleNamer.Name(t)) //nolint:errcheck
	return nil
}

// ─── iam-policies ────────────────────────────────────────────────────────────

func (g *iamGroup) setupPolicies(_ context.Context, t *harness.TestContext) error {
	// Create a role so we can test policy attachment.
	// Use iamPolRoleNamer (not iamRoleNamer) so this group's role doesn't
	// collide with the iam-roles group that runs in parallel.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-role",
		"--role-name", iamPolRoleNamer.Name(t),
		"--assume-role-policy-document", g.assumePolicy(),
	)
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	if err != nil {
		// Role already exists — fetch its ARN.
		out, err = awscli.RunOutput(t.Endpoint, t.Region,
			"iam", "get-role", "--role-name", iamPolRoleNamer.Name(t))
		if err != nil {
			return err
		}
	}
	role, _ := out["Role"].(map[string]any)
	arn, _ := role["Arn"].(string)
	t.Set("role_arn", arn)
	return nil
}

func (g *iamGroup) CreatePolicy(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-policy",
		"--policy-name", iamPolicyNamer.Name(t),
		"--policy-document", g.s3PolicyDoc(),
	)
	if err != nil {
		return err
	}
	policy, _ := out["Policy"].(map[string]any)
	arn, _ := policy["Arn"].(string)
	t.Set("policy_arn", arn)
	return nil
}

func (g *iamGroup) GetPolicy(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("policy_arn")
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-policy",
		"--policy-arn", arn,
	)
	return err
}

func (g *iamGroup) ListPolicies(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "list-policies",
		"--scope", "Local",
	)
	return err
}

func (g *iamGroup) DeletePolicy(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("policy_arn")
	return awscli.Run(t.Endpoint, t.Region, "iam", "delete-policy", "--policy-arn", arn)
}

func (g *iamGroup) teardownPolicies(_ context.Context, t *harness.TestContext) error {
	if policyArn := t.GetString("policy_arn"); policyArn != "" {
		awscli.Run(t.Endpoint, t.Region, "iam", "detach-role-policy", //nolint:errcheck
			"--role-name", iamPolRoleNamer.Name(t), "--policy-arn", policyArn)
		awscli.Run(t.Endpoint, t.Region, "iam", "delete-policy", "--policy-arn", policyArn) //nolint:errcheck
	}
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-role", "--role-name", iamPolRoleNamer.Name(t)) //nolint:errcheck
	return nil
}

// ─── iam-groups ──────────────────────────────────────────────────────────────

func (g *iamGroup) setupGroups(_ context.Context, t *harness.TestContext) error {
	// Create a user to add to the group.
	// Use iamGrpUserNamer (not iamUserNamer) so this doesn't
	// collide with the iam-users group that runs in parallel.
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-user", "--user-name", iamGrpUserNamer.Name(t),
	)
	if err != nil && isAlreadyExists(err) {
		return nil
	}
	return err
}

func (g *iamGroup) CreateGroup(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-group",
		"--group-name", iamGroupNamer.Name(t),
	)
	return err
}

func (g *iamGroup) AddUserToGroup(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "add-user-to-group",
		"--group-name", iamGroupNamer.Name(t),
		"--user-name", iamGrpUserNamer.Name(t),
	)
}

func (g *iamGroup) ListGroupsForUser(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "list-groups-for-user",
		"--user-name", iamGrpUserNamer.Name(t),
	)
	return err
}

func (g *iamGroup) RemoveUserFromGroup(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "remove-user-from-group",
		"--group-name", iamGroupNamer.Name(t),
		"--user-name", iamGrpUserNamer.Name(t),
	)
}

func (g *iamGroup) GetGroup(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "get-group",
		"--group-name", iamGroupNamer.Name(t),
	)
	if err != nil {
		return err
	}
	grp, _ := out["Group"].(map[string]any)
	if grp == nil || grp["GroupName"] != iamGroupNamer.Name(t) {
		return fmt.Errorf("iam GetGroup: wrong group name")
	}
	return nil
}

func (g *iamGroup) DeleteGroup(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"iam", "delete-group",
		"--group-name", iamGroupNamer.Name(t),
	)
}

func (g *iamGroup) teardownIAMGroups(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "iam", "remove-user-from-group", //nolint:errcheck
		"--group-name", iamGroupNamer.Name(t), "--user-name", iamGrpUserNamer.Name(t))
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-group", "--group-name", iamGroupNamer.Name(t)) //nolint:errcheck
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-user", "--user-name", iamGrpUserNamer.Name(t)) //nolint:errcheck
	return nil
}
