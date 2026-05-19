package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// SecretsManager returns the Secrets Manager service group.
func SecretsManager() ServiceGroup {
	g := &smGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// secretsmanager-crud — also register TagResource/UntagResource under
			// group-qualified keys so they are not overridden by the appsync group
			// which shares the same bare test names.
			"CreateSecret":                      g.CreateSecret,
			"GetSecretValue":                    g.GetSecretValue,
			"DescribeSecret":                    g.DescribeSecret,
			"PutSecretValue":                    g.PutSecretValue,
			"ListSecretVersionIds":              g.ListSecretVersionIds,
			"UpdateSecret":                      g.UpdateSecret,
			"TagResource":                       g.TagResource,
			"UntagResource":                     g.UntagResource,
			"secretsmanager-crud:TagResource":   g.TagResource,
			"secretsmanager-crud:UntagResource": g.UntagResource,
			"GetRandomPassword":                 g.GetRandomPassword,
			"BatchGetSecretValue":               g.BatchGetSecretValue,
			"ListSecrets":                       g.ListSecrets,
			"DeleteSecret":                      g.DeleteSecret,
			// secretsmanager-rotate
			"RotateSecret":       g.RotateSecret,
			"CancelRotateSecret": g.CancelRotateSecret,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.setupCRUD,
			"secretsmanager-rotate": g.setupRotate,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.teardownSecret,
			"secretsmanager-rotate": g.teardownRotateSecret,
		},
	}
}

type smGroup struct{}

func (g *smGroup) secretName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-secret", t.RunID)
}

// rotateSecretName returns the secret name used by the secretsmanager-rotate
// group. Uses a distinct suffix from secretName to avoid intra-suite conflicts
// when crud and rotate groups run in parallel.
func (g *smGroup) rotateSecretName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-smrot", t.RunID)
}

// ─── secretsmanager-crud ─────────────────────────────────────────────────────

func (g *smGroup) setupCRUD(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *smGroup) CreateSecret(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "create-secret",
		"--name", g.secretName(t),
		"--secret-string", `{"user":"admin","pass":"s3cr3t"}`,
	)
	if err != nil {
		return err
	}
	arn, _ := out["ARN"].(string)
	t.Set("secret_arn", arn)
	return nil
}

func (g *smGroup) GetSecretValue(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-secret-value",
		"--secret-id", g.secretName(t),
	)
	if err != nil {
		return err
	}
	if out["SecretString"] == nil {
		return fmt.Errorf("secretsmanager GetSecretValue: missing SecretString")
	}
	return nil
}

func (g *smGroup) DescribeSecret(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", g.secretName(t),
	)
	if err != nil {
		return err
	}
	if arn, _ := out["ARN"].(string); arn == "" {
		return fmt.Errorf("secretsmanager DescribeSecret: missing ARN")
	}
	if name, _ := out["Name"].(string); name != g.secretName(t) {
		return fmt.Errorf("secretsmanager DescribeSecret: Name mismatch: got %q, want %q", name, g.secretName(t))
	}
	return nil
}

func (g *smGroup) PutSecretValue(_ context.Context, t *harness.TestContext) error {
	if _, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "put-secret-value",
		"--secret-id", g.secretName(t),
		"--secret-string", `{"user":"admin","pass":"newpass"}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-secret-value",
		"--secret-id", g.secretName(t),
	)
	if err != nil {
		return fmt.Errorf("secretsmanager PutSecretValue: get-secret-value failed: %w", err)
	}
	if out["SecretString"] != `{"user":"admin","pass":"newpass"}` {
		return fmt.Errorf("secretsmanager PutSecretValue: SecretString mismatch; got %v", out["SecretString"])
	}
	return nil
}

func (g *smGroup) ListSecretVersionIds(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "list-secret-version-ids",
		"--secret-id", g.secretName(t),
	)
	if err != nil {
		return err
	}
	versions, _ := out["Versions"].([]any)
	if len(versions) < 1 {
		return fmt.Errorf("secretsmanager ListSecretVersionIds: expected at least 1 version, got %d", len(versions))
	}
	return nil
}

func (g *smGroup) UpdateSecret(_ context.Context, t *harness.TestContext) error {
	if _, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "update-secret",
		"--secret-id", g.secretName(t),
		"--description", "Updated by CLI test",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", g.secretName(t),
	)
	if err != nil {
		return fmt.Errorf("secretsmanager UpdateSecret: describe-secret failed: %w", err)
	}
	if out["Description"] != "Updated by CLI test" {
		return fmt.Errorf("secretsmanager UpdateSecret: Description not updated; got %v", out["Description"])
	}
	return nil
}

func (g *smGroup) TagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("secret_arn")
	if err := awscli.Run(t.Endpoint, t.Region,
		"secretsmanager", "tag-resource",
		"--secret-id", arn,
		"--tags", `[{"Key":"env","Value":"compat"}]`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", arn,
	)
	if err != nil {
		return fmt.Errorf("TagResource: describe-secret failed: %w", err)
	}
	tags, _ := out["Tags"].([]interface{})
	for _, raw := range tags {
		if m, ok := raw.(map[string]interface{}); ok {
			if m["Key"] == "env" && m["Value"] == "compat" {
				return nil
			}
		}
	}
	return fmt.Errorf("secretsmanager TagResource: tag env=compat not found after tagging")
}

func (g *smGroup) UntagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("secret_arn")
	if err := awscli.Run(t.Endpoint, t.Region,
		"secretsmanager", "untag-resource",
		"--secret-id", arn,
		"--tag-keys", "env",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", arn,
	)
	if err != nil {
		return fmt.Errorf("UntagResource: describe-secret failed: %w", err)
	}
	tags, _ := out["Tags"].([]interface{})
	for _, raw := range tags {
		if m, ok := raw.(map[string]interface{}); ok {
			if m["Key"] == "env" {
				return fmt.Errorf("secretsmanager UntagResource: tag 'env' still present after untagging")
			}
		}
	}
	return nil
}

func (g *smGroup) GetRandomPassword(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-random-password",
		"--password-length", "20",
	)
	if err != nil {
		return err
	}
	if out["RandomPassword"] == nil {
		return fmt.Errorf("secretsmanager GetRandomPassword: missing RandomPassword")
	}
	return nil
}

func (g *smGroup) BatchGetSecretValue(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "batch-get-secret-value",
		"--secret-id-list", g.secretName(t),
	)
	if err != nil {
		return err
	}
	values, _ := out["SecretValues"].([]any)
	if len(values) < 1 {
		return fmt.Errorf("secretsmanager BatchGetSecretValue: expected at least 1 SecretValue, got %d", len(values))
	}
	return nil
}

func (g *smGroup) ListSecrets(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "secretsmanager", "list-secrets")
	if err != nil {
		return err
	}
	secrets, _ := out["SecretList"].([]any)
	want := g.secretName(t)
	for _, raw := range secrets {
		if m, ok := raw.(map[string]any); ok {
			if m["Name"] == want {
				return nil
			}
		}
	}
	return fmt.Errorf("secretsmanager ListSecrets: secret %q not found in list", want)
}

func (g *smGroup) DeleteSecret(_ context.Context, t *harness.TestContext) error {
	secretName := g.secretName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"secretsmanager", "delete-secret",
		"--secret-id", secretName,
		"--force-delete-without-recovery",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "secretsmanager", "list-secrets")
	if err != nil {
		return fmt.Errorf("secretsmanager DeleteSecret: list-secrets failed: %w", err)
	}
	secrets, _ := out["SecretList"].([]any)
	for _, raw := range secrets {
		if m, ok := raw.(map[string]any); ok {
			if m["Name"] == secretName {
				return fmt.Errorf("secretsmanager DeleteSecret: secret %q still present after deletion", secretName)
			}
		}
	}
	return nil
}

func (g *smGroup) teardownSecret(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, //nolint:errcheck
		"secretsmanager", "delete-secret",
		"--secret-id", g.secretName(t),
		"--force-delete-without-recovery",
	)
	return nil
}

func (g *smGroup) teardownRotateSecret(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, //nolint:errcheck
		"secretsmanager", "delete-secret",
		"--secret-id", g.rotateSecretName(t),
		"--force-delete-without-recovery",
	)
	return nil
}

// ─── secretsmanager-rotate ───────────────────────────────────────────────────

func (g *smGroup) setupRotate(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "create-secret",
		"--name", g.rotateSecretName(t),
		"--secret-string", "rotate-me",
	)
	if err != nil {
		return err
	}
	arn, _ := out["ARN"].(string)
	t.Set("secret_arn", arn)
	return nil
}

func (g *smGroup) RotateSecret(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "rotate-secret",
		"--secret-id", g.rotateSecretName(t),
		"--rotation-rules", `{"AutomaticallyAfterDays":30}`,
	)
	if err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return fmt.Errorf("sm RotateSecret: describe-secret failed: %w", err)
	}
	if out["RotationEnabled"] != true {
		return fmt.Errorf("sm RotateSecret: RotationEnabled not true")
	}
	return nil
}

func (g *smGroup) CancelRotateSecret(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "cancel-rotate-secret",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return fmt.Errorf("sm CancelRotateSecret: describe-secret failed: %w", err)
	}
	if out["RotationEnabled"] == true {
		return fmt.Errorf("sm CancelRotateSecret: RotationEnabled still true")
	}
	return nil
}
