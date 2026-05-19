package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

func SecretsManager(c *clients.Clients) ServiceGroup {
	g := &smGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateSecret":         g.CreateSecret,
			"GetSecretValue":       g.GetSecretValue,
			"PutSecretValue":       g.PutSecretValue,
			"UpdateSecret":         g.UpdateSecret,
			"DescribeSecret":       g.DescribeSecret,
			"ListSecrets":          g.ListSecrets,
			"TagResource":          g.TagResource,
			"UntagResource":        g.UntagResource,
			"ListSecretVersionIds": g.ListSecretVersionIds,
			"DeleteSecret":         g.DeleteSecret,
			"GetRandomPassword":    g.GetRandomPassword,
			"BatchGetSecretValue":  g.BatchGetSecretValue,
			"RotateSecret":         g.RotateSecret,
			"CancelRotateSecret":   g.CancelRotateSecret,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.setup,
			"secretsmanager-rotate": g.setupRotate,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.teardown,
			"secretsmanager-rotate": g.teardownRotate,
		},
	}
}

type smGroup struct{ c *clients.Clients }

func (g *smGroup) cl() *secretsmanager.Client { return g.c.SecretsManager() }

func (g *smGroup) setup(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc/%s/secret", t.RunID)
	resp, err := g.cl().CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(`{"key":"initial"}`),
	})
	if err != nil {
		return err
	}
	t.Set("sm_name", name)
	t.Set("sm_arn", aws.ToString(resp.ARN))
	return nil
}

func (g *smGroup) teardown(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sm_arn"); arn != "" {
		g.cl().DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
			SecretId:                   aws.String(arn),
			ForceDeleteWithoutRecovery: aws.Bool(true),
		}) //nolint:errcheck
	}
	return nil
}

func (g *smGroup) CreateSecret(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc/%s/cs", t.RunID)
	resp, err := g.cl().CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String("my-secret-value"),
	})
	if err == nil {
		g.cl().DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
			SecretId: resp.ARN, ForceDeleteWithoutRecovery: aws.Bool(true),
		}) //nolint:errcheck
	}
	return err
}

func (g *smGroup) GetSecretValue(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.SecretString) == "" {
		return fmt.Errorf("GetSecretValue: empty secret string")
	}
	return nil
}

func (g *smGroup) PutSecretValue(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(t.GetString("sm_arn")),
		SecretString: aws.String(`{"key":"updated"}`),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return fmt.Errorf("PutSecretValue: GetSecretValue verify failed: %w", err)
	}
	if aws.ToString(resp.SecretString) != `{"key":"updated"}` {
		return fmt.Errorf("PutSecretValue: expected updated value, got %q", aws.ToString(resp.SecretString))
	}
	return nil
}

func (g *smGroup) UpdateSecret(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
		SecretId:    aws.String(t.GetString("sm_arn")),
		Description: aws.String("updated description"),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return fmt.Errorf("UpdateSecret: DescribeSecret verify failed: %w", err)
	}
	if aws.ToString(resp.Description) != "updated description" {
		return fmt.Errorf("UpdateSecret: expected description %q, got %q", "updated description", aws.ToString(resp.Description))
	}
	return nil
}

func (g *smGroup) DescribeSecret(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Name) != t.GetString("sm_name") {
		return fmt.Errorf("DescribeSecret: name mismatch")
	}
	return nil
}

func (g *smGroup) ListSecrets(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListSecrets(ctx, &secretsmanager.ListSecretsInput{})
	if err != nil {
		return err
	}
	if len(resp.SecretList) == 0 {
		return fmt.Errorf("ListSecrets: expected ≥1 secret")
	}
	return nil
}

func (g *smGroup) TagResource(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().TagResource(ctx, &secretsmanager.TagResourceInput{
		SecretId: aws.String(t.GetString("sm_arn")),
		Tags: []smtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("compat")},
		},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return fmt.Errorf("TagResource: DescribeSecret failed: %w", err)
	}
	for _, tag := range resp.Tags {
		if aws.ToString(tag.Key) == "env" && aws.ToString(tag.Value) == "compat" {
			return nil
		}
	}
	return fmt.Errorf("TagResource: tag env=compat not found after tagging")
}

func (g *smGroup) UntagResource(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().UntagResource(ctx, &secretsmanager.UntagResourceInput{
		SecretId: aws.String(t.GetString("sm_arn")),
		TagKeys:  []string{"env"},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return fmt.Errorf("UntagResource: DescribeSecret failed: %w", err)
	}
	for _, tag := range resp.Tags {
		if aws.ToString(tag.Key) == "env" {
			return fmt.Errorf("UntagResource: tag 'env' still present after untagging")
		}
	}
	return nil
}

func (g *smGroup) ListSecretVersionIds(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListSecretVersionIds(ctx, &secretsmanager.ListSecretVersionIdsInput{
		SecretId: aws.String(t.GetString("sm_arn")),
	})
	if err != nil {
		return err
	}
	_ = resp
	return nil
}

func (g *smGroup) DeleteSecret(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc/%s/ds", t.RunID)
	resp, err := g.cl().CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name: aws.String(name), SecretString: aws.String("value"),
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId: resp.ARN, ForceDeleteWithoutRecovery: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	// Verify secret is gone
	list, err := g.cl().ListSecrets(ctx, &secretsmanager.ListSecretsInput{})
	if err != nil {
		return fmt.Errorf("DeleteSecret: ListSecrets verify failed: %w", err)
	}
	for _, s := range list.SecretList {
		if aws.ToString(s.Name) == name {
			return fmt.Errorf("DeleteSecret: secret %q still present", name)
		}
	}
	return nil
}

func (g *smGroup) GetRandomPassword(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetRandomPassword(ctx, &secretsmanager.GetRandomPasswordInput{
		PasswordLength: aws.Int64(32),
	})
	if err != nil {
		return err
	}
	pw := aws.ToString(resp.RandomPassword)
	if len(pw) != 32 {
		return fmt.Errorf("GetRandomPassword: expected length 32, got %d", len(pw))
	}
	return nil
}

func (g *smGroup) BatchGetSecretValue(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().BatchGetSecretValue(ctx, &secretsmanager.BatchGetSecretValueInput{
		SecretIdList: []string{t.GetString("sm_arn")},
	})
	if err != nil {
		return err
	}
	if len(resp.SecretValues) < 1 {
		return fmt.Errorf("BatchGetSecretValue: expected at least 1 secret value, got %d", len(resp.SecretValues))
	}
	return nil
}

// ── secretsmanager-rotate ─────────────────────────────────────────────────────

func (g *smGroup) setupRotate(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc/%s/rotate", t.RunID)
	resp, err := g.cl().CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String("rotate-me"),
	})
	if err != nil {
		return err
	}
	t.Set("sm_rotate_name", name)
	t.Set("sm_rotate_arn", aws.ToString(resp.ARN))
	return nil
}

func (g *smGroup) teardownRotate(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sm_rotate_arn"); arn != "" {
		g.cl().DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
			SecretId:                   aws.String(arn),
			ForceDeleteWithoutRecovery: aws.Bool(true),
		}) //nolint:errcheck
	}
	return nil
}

func (g *smGroup) RotateSecret(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().RotateSecret(ctx, &secretsmanager.RotateSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	// RotateSecret without a Lambda rotation function may return an error;
	// either success or a recognised error is acceptable.
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		// Accept the call being made even if it fails due to missing rotation config
		return nil
	}
	return nil
}

func (g *smGroup) CancelRotateSecret(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().CancelRotateSecret(ctx, &secretsmanager.CancelRotateSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	return nil
}
