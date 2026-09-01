package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
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
			"secretsmanager-crud:TagResource":   g.TagResource,
			"secretsmanager-crud:UntagResource": g.UntagResource,
			"GetRandomPassword":                 g.GetRandomPassword,
			"BatchGetSecretValue":               g.BatchGetSecretValue,
			"ListSecrets":                       g.ListSecrets,
			"DeleteSecret":                      g.DeleteSecret,
			// secretsmanager-rotate
			"RotateSecretWithoutLambda": g.RotateSecretWithoutLambda,
			"RotateSecret":              g.RotateSecret,
			"PutSecretValuePending":     g.PutSecretValuePending,
			"GetSecretValueByStage":     g.GetSecretValueByStage,
			"UpdateSecretVersionStage":  g.UpdateSecretVersionStage,
			"CancelRotateSecret":        g.CancelRotateSecret,
			// secretsmanager-policy
			"ValidateResourcePolicy": g.ValidateResourcePolicy,
			"PutResourcePolicy":      g.PutResourcePolicy,
			"GetResourcePolicy":      g.GetResourcePolicy,
			"DeleteResourcePolicy":   g.DeleteResourcePolicy,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.setupCRUD,
			"secretsmanager-rotate": g.setupRotate,
			"secretsmanager-policy": g.setupPolicy,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.teardownSecret,
			"secretsmanager-rotate": g.teardownRotateSecret,
			"secretsmanager-policy": g.teardownPolicySecret,
		},
	}
}

// pendingToken is the ClientRequestToken the staging-label tests use. AWS wants
// 32–64 characters and the token becomes the version ID, so a fixed UUID keeps
// the assertions readable.
const pendingToken = "0f9c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f"

// compatResourcePolicy is a minimal, well-formed secret resource policy.
const compatResourcePolicy = `{"Version":"2012-10-17","Statement":[{"Sid":"OvercastCompatRead","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`

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

// rotationLambdaARN is a placeholder function ARN. This group always sets
// --no-rotate-immediately, so it is never invoked: driving the four-step
// protocol needs a real rotation Lambda and belongs with the Docker-dependent
// Lambda groups.
func (g *smGroup) rotationLambdaARN(t *harness.TestContext) string {
	return fmt.Sprintf("arn:aws:lambda:%s:000000000000:function:oc-rotate-fn", t.Region)
}

func (g *smGroup) RotateSecretWithoutLambda(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "rotate-secret",
		"--secret-id", g.rotateSecretName(t),
		"--rotation-rules", `{"AutomaticallyAfterDays":30}`,
	)
	if err == nil {
		return fmt.Errorf("sm RotateSecretWithoutLambda: expected InvalidRequestException, call succeeded")
	}
	if harness.IsUnimplemented(err) {
		return err
	}
	if !strings.Contains(err.Error(), "InvalidRequestException") {
		return fmt.Errorf("sm RotateSecretWithoutLambda: expected InvalidRequestException, got %w", err)
	}
	return nil
}

func (g *smGroup) RotateSecret(_ context.Context, t *harness.TestContext) error {
	arn := g.rotationLambdaARN(t)
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "rotate-secret",
		"--secret-id", g.rotateSecretName(t),
		"--rotation-lambda-arn", arn,
		"--rotation-rules", `{"AutomaticallyAfterDays":30}`,
		"--no-rotate-immediately",
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
	if got, _ := out["RotationLambdaARN"].(string); got != arn {
		return fmt.Errorf("sm RotateSecret: RotationLambdaARN = %q, want %q", got, arn)
	}
	if _, ok := out["NextRotationDate"]; !ok {
		return fmt.Errorf("sm RotateSecret: NextRotationDate not set")
	}
	return nil
}

func (g *smGroup) PutSecretValuePending(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "put-secret-value",
		"--secret-id", g.rotateSecretName(t),
		"--secret-string", "pending-value",
		"--client-request-token", pendingToken,
		"--version-stages", "AWSPENDING",
	)
	if err != nil {
		return err
	}
	if got, _ := out["VersionId"].(string); got != pendingToken {
		return fmt.Errorf("sm PutSecretValuePending: VersionId = %q, want the ClientRequestToken %q", got, pendingToken)
	}
	if !jsonListContains(out["VersionStages"], "AWSPENDING") {
		return fmt.Errorf("sm PutSecretValuePending: VersionStages = %v, want AWSPENDING", out["VersionStages"])
	}
	// Staging a pending version must not change AWSCURRENT.
	cur, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-secret-value",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return fmt.Errorf("sm PutSecretValuePending: get-secret-value failed: %w", err)
	}
	if got, _ := cur["SecretString"].(string); got != "rotate-me" {
		return fmt.Errorf("sm PutSecretValuePending: AWSCURRENT = %q, want the untouched %q", got, "rotate-me")
	}
	return nil
}

func (g *smGroup) GetSecretValueByStage(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-secret-value",
		"--secret-id", g.rotateSecretName(t),
		"--version-stage", "AWSPENDING",
	)
	if err != nil {
		return err
	}
	if got, _ := out["SecretString"].(string); got != "pending-value" {
		return fmt.Errorf("sm GetSecretValueByStage: SecretString = %q, want %q", got, "pending-value")
	}
	return nil
}

func (g *smGroup) UpdateSecretVersionStage(_ context.Context, t *harness.TestContext) error {
	desc, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return err
	}
	stages, _ := desc["VersionIdsToStages"].(map[string]any)
	current := ""
	for id, v := range stages {
		if jsonListContains(v, "AWSCURRENT") {
			current = id
		}
	}
	if current == "" {
		return fmt.Errorf("sm UpdateSecretVersionStage: no AWSCURRENT version")
	}

	// This is what a rotation function's finishSecret step does.
	if _, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "update-secret-version-stage",
		"--secret-id", g.rotateSecretName(t),
		"--version-stage", "AWSCURRENT",
		"--move-to-version-id", pendingToken,
		"--remove-from-version-id", current,
	); err != nil {
		return err
	}

	after, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-secret-value",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return fmt.Errorf("sm UpdateSecretVersionStage: get-secret-value failed: %w", err)
	}
	if got, _ := after["SecretString"].(string); got != "pending-value" {
		return fmt.Errorf("sm UpdateSecretVersionStage: AWSCURRENT = %q, want %q", got, "pending-value")
	}
	if got, _ := after["VersionId"].(string); got != pendingToken {
		return fmt.Errorf("sm UpdateSecretVersionStage: AWSCURRENT version = %q, want %q", got, pendingToken)
	}

	post, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "describe-secret",
		"--secret-id", g.rotateSecretName(t),
	)
	if err != nil {
		return fmt.Errorf("sm UpdateSecretVersionStage: describe-secret failed: %w", err)
	}
	postStages, _ := post["VersionIdsToStages"].(map[string]any)
	if !jsonListContains(postStages[current], "AWSPREVIOUS") {
		return fmt.Errorf("sm UpdateSecretVersionStage: displaced version stages = %v, want AWSPREVIOUS", postStages[current])
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
	// AWS keeps the function configured so rotation can be turned back on.
	if got, want := out["RotationLambdaARN"], g.rotationLambdaARN(t); got != want {
		return fmt.Errorf("sm CancelRotateSecret: RotationLambdaARN = %v, want it retained as %q", got, want)
	}
	return nil
}

// jsonListContains reports whether a decoded JSON array of strings holds want.
func jsonListContains(v any, want string) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}

// ─── secretsmanager-policy ───────────────────────────────────────────────────
//
// Overcast stores and validates a secret's resource policy but does not
// evaluate it, so these assert the round-trip and the validation verdict,
// never an access decision.

func (g *smGroup) policySecretName(t *harness.TestContext) string {
	return fmt.Sprintf("%s-smpol", t.RunID)
}

func (g *smGroup) setupPolicy(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "create-secret",
		"--name", g.policySecretName(t),
		"--secret-string", "policy-value",
	)
	return err
}

func (g *smGroup) teardownPolicySecret(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, //nolint:errcheck
		"secretsmanager", "delete-secret",
		"--secret-id", g.policySecretName(t),
		"--force-delete-without-recovery",
	)
	return nil
}

func (g *smGroup) ValidateResourcePolicy(_ context.Context, t *harness.TestContext) error {
	ok, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "validate-resource-policy",
		"--resource-policy", compatResourcePolicy,
	)
	if err != nil {
		return err
	}
	if ok["PolicyValidationPassed"] != true {
		return fmt.Errorf("sm ValidateResourcePolicy: valid policy rejected: %v", ok["ValidationErrors"])
	}

	bad, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "validate-resource-policy",
		"--resource-policy", `{"Version":"2012-10-17"}`,
	)
	if err != nil {
		return fmt.Errorf("sm ValidateResourcePolicy: validating a bad policy errored: %w", err)
	}
	if bad["PolicyValidationPassed"] == true {
		return fmt.Errorf("sm ValidateResourcePolicy: policy with no Statement should not pass")
	}
	errs, _ := bad["ValidationErrors"].([]any)
	if len(errs) == 0 {
		return fmt.Errorf("sm ValidateResourcePolicy: no ValidationErrors for a failing policy")
	}
	return nil
}

func (g *smGroup) PutResourcePolicy(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "put-resource-policy",
		"--secret-id", g.policySecretName(t),
		"--resource-policy", compatResourcePolicy,
	)
	if err != nil {
		return err
	}
	if arn, _ := out["ARN"].(string); arn == "" {
		return fmt.Errorf("sm PutResourcePolicy: missing ARN")
	}
	if got, want := out["Name"], g.policySecretName(t); got != want {
		return fmt.Errorf("sm PutResourcePolicy: Name = %v, want %q", got, want)
	}
	return nil
}

func (g *smGroup) GetResourcePolicy(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-resource-policy",
		"--secret-id", g.policySecretName(t),
	)
	if err != nil {
		return err
	}
	policy, _ := out["ResourcePolicy"].(string)
	if !strings.Contains(policy, "OvercastCompatRead") {
		return fmt.Errorf("sm GetResourcePolicy: policy %q does not carry the Sid that was stored", policy)
	}
	return nil
}

func (g *smGroup) DeleteResourcePolicy(_ context.Context, t *harness.TestContext) error {
	if _, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "delete-resource-policy",
		"--secret-id", g.policySecretName(t),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"secretsmanager", "get-resource-policy",
		"--secret-id", g.policySecretName(t),
	)
	if err != nil {
		return fmt.Errorf("sm DeleteResourcePolicy: get-resource-policy failed: %w", err)
	}
	if policy, _ := out["ResourcePolicy"].(string); policy != "" {
		return fmt.Errorf("sm DeleteResourcePolicy: policy still present: %q", policy)
	}
	return nil
}
