package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	iamsdk "github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func STS(c *clients.Clients) ServiceGroup {
	g := &stsGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"GetCallerIdentity":         g.GetCallerIdentity,
			"GetSessionToken":           g.GetSessionToken,
			"GetFederationToken":        g.GetFederationToken,
			"AssumeRole":                g.AssumeRole,
			"AssumeRoleWithWebIdentity": g.AssumeRoleWithWebIdentity,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"sts-identity": g.setupIdentity,
			"sts-assume":   g.setupAssume,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"sts-identity": g.noopTeardown,
			"sts-assume":   g.teardownAssume,
		},
	}
}

type stsGroup struct{ c *clients.Clients }

func (g *stsGroup) cl() *sts.Client { return g.c.STS() }

func (g *stsGroup) noopTeardown(_ context.Context, _ *harness.TestContext) error { return nil }

// ── sts-identity ──────────────────────────────────────────────────────────────

func (g *stsGroup) setupIdentity(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *stsGroup) GetCallerIdentity(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Account) == "" {
		return fmt.Errorf("GetCallerIdentity: empty account")
	}
	return nil
}

func (g *stsGroup) GetSessionToken(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetSessionToken(ctx, &sts.GetSessionTokenInput{
		DurationSeconds: aws.Int32(900),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if resp.Credentials == nil {
		return fmt.Errorf("GetSessionToken: nil credentials")
	}
	return nil
}

func (g *stsGroup) GetFederationToken(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetFederationToken(ctx, &sts.GetFederationTokenInput{
		Name: aws.String("oc-fed"),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if resp.Credentials == nil {
		return fmt.Errorf("GetFederationToken: nil credentials")
	}
	return nil
}

// ── sts-assume ────────────────────────────────────────────────────────────────

func (g *stsGroup) setupAssume(ctx context.Context, t *harness.TestContext) error {
	// Create an IAM role to assume
	iamCl := g.c.IAM()
	roleName := fmt.Sprintf("oc-sts-%s", t.RunID)
	resp, err := iamCl.CreateRole(ctx, &iamsdk.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(iamAssumePolicy()),
	})
	if err != nil {
		// If IAM is not implemented, skip
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	t.Set("sts_role_name", roleName)
	t.Set("sts_role_arn", aws.ToString(resp.Role.Arn))
	return nil
}

func (g *stsGroup) teardownAssume(ctx context.Context, t *harness.TestContext) error {
	if name := t.GetString("sts_role_name"); name != "" {
		g.c.IAM().DeleteRole(ctx, &iamsdk.DeleteRoleInput{RoleName: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *stsGroup) AssumeRole(ctx context.Context, t *harness.TestContext) error {
	roleArn := t.GetString("sts_role_arn")
	if roleArn == "" {
		// IAM might not be implemented; use a fake ARN and expect 501 or error
		roleArn = "arn:aws:iam::000000000000:role/oc-sts-fake"
	}
	resp, err := g.cl().AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String("oc-session"),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if resp.Credentials == nil {
		return fmt.Errorf("AssumeRole: nil credentials")
	}
	return nil
}

func (g *stsGroup) AssumeRoleWithWebIdentity(ctx context.Context, t *harness.TestContext) error {
	roleArn := t.GetString("sts_role_arn")
	if roleArn == "" {
		roleArn = "arn:aws:iam::000000000000:role/oc-sts-fake"
	}
	resp, err := g.cl().AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleArn),
		RoleSessionName:  aws.String("oc-web-session"),
		WebIdentityToken: aws.String("fake-jwt-token"),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if resp.Credentials == nil {
		return fmt.Errorf("AssumeRoleWithWebIdentity: nil credentials")
	}
	return nil
}
