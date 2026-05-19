package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// STS returns the STS service group.
func STS() ServiceGroup {
	g := &stsGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// sts-identity
			"GetCallerIdentity":  g.GetCallerIdentity,
			"GetSessionToken":    g.GetSessionToken,
			"GetFederationToken": g.GetFederationToken,
			// sts-assume
			"AssumeRole":                g.AssumeRole,
			"AssumeRoleWithWebIdentity": g.AssumeRoleWithWebIdentity,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"sts-identity": g.setupIdentity,
			"sts-assume":   g.setupAssume,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"sts-identity": g.teardownNoop,
			"sts-assume":   g.teardownAssume,
		},
	}
}

type stsGroup struct{}

func (g *stsGroup) roleName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-sts-role", t.RunID)
}

func (g *stsGroup) assumePolicy() string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}`
}

// ─── sts-identity ────────────────────────────────────────────────────────────

func (g *stsGroup) setupIdentity(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *stsGroup) teardownNoop(_ context.Context, _ *harness.TestContext) error  { return nil }

func (g *stsGroup) GetCallerIdentity(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "sts", "get-caller-identity")
	if err != nil {
		return err
	}
	if out["Account"] == nil {
		return fmt.Errorf("sts GetCallerIdentity: missing Account")
	}
	return nil
}

func (g *stsGroup) GetSessionToken(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "sts", "get-session-token")
	if err != nil {
		return err
	}
	creds, _ := out["Credentials"].(map[string]any)
	if creds["AccessKeyId"] == nil {
		return fmt.Errorf("sts GetSessionToken: missing AccessKeyId")
	}
	return nil
}

func (g *stsGroup) GetFederationToken(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sts", "get-federation-token",
		"--name", fmt.Sprintf("fed-%s", t.RunID),
	)
	if err != nil {
		return err
	}
	creds, _ := out["Credentials"].(map[string]any)
	if creds["AccessKeyId"] == nil {
		return fmt.Errorf("sts GetFederationToken: missing AccessKeyId")
	}
	return nil
}

// ─── sts-assume ──────────────────────────────────────────────────────────────

func (g *stsGroup) setupAssume(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"iam", "create-role",
		"--role-name", g.roleName(t),
		"--assume-role-policy-document", g.assumePolicy(),
	)
	if err != nil {
		return err
	}
	role, _ := out["Role"].(map[string]any)
	arn, _ := role["Arn"].(string)
	t.Set("role_arn", arn)
	return nil
}

func (g *stsGroup) AssumeRole(_ context.Context, t *harness.TestContext) error {
	roleArn := t.GetString("role_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sts", "assume-role",
		"--role-arn", roleArn,
		"--role-session-name", fmt.Sprintf("ses-%s", t.RunID),
	)
	if err != nil {
		return err
	}
	creds, _ := out["Credentials"].(map[string]any)
	if creds["AccessKeyId"] == nil {
		return fmt.Errorf("sts AssumeRole: missing AccessKeyId")
	}
	return nil
}

func (g *stsGroup) AssumeRoleWithWebIdentity(_ context.Context, t *harness.TestContext) error {
	roleArn := t.GetString("role_arn")
	// Emulator accepts any token — use a placeholder.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"sts", "assume-role-with-web-identity",
		"--role-arn", roleArn,
		"--role-session-name", fmt.Sprintf("web-%s", t.RunID),
		"--web-identity-token", "dummy-token",
	)
	if err != nil {
		return err
	}
	creds, _ := out["Credentials"].(map[string]any)
	if creds["AccessKeyId"] == nil {
		return fmt.Errorf("sts AssumeRoleWithWebIdentity: missing AccessKeyId")
	}
	return nil
}

func (g *stsGroup) teardownAssume(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "iam", "delete-role", "--role-name", g.roleName(t)) //nolint:errcheck
	return nil
}
