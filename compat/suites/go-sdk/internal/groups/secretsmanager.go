package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

// pendingToken is the ClientRequestToken the staging-label tests use. AWS wants
// 32–64 characters and the token becomes the version ID, so a fixed UUID keeps
// the assertions readable.
const pendingToken = "0f9c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f"

// compatResourcePolicy is a minimal, well-formed secret resource policy.
const compatResourcePolicy = `{"Version":"2012-10-17","Statement":[{"Sid":"OvercastCompatRead","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`

func SecretsManager(c *clients.Clients) ServiceGroup {
	g := &smGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateSecret":                      g.CreateSecret,
			"GetSecretValue":                    g.GetSecretValue,
			"PutSecretValue":                    g.PutSecretValue,
			"UpdateSecret":                      g.UpdateSecret,
			"DescribeSecret":                    g.DescribeSecret,
			"ListSecrets":                       g.ListSecrets,
			"secretsmanager-crud:TagResource":   g.TagResource,
			"secretsmanager-crud:UntagResource": g.UntagResource,
			"ListSecretVersionIds":              g.ListSecretVersionIds,
			"DeleteSecret":                      g.DeleteSecret,
			"GetRandomPassword":                 g.GetRandomPassword,
			"BatchGetSecretValue":               g.BatchGetSecretValue,
			"RotateSecretWithoutLambda":         g.RotateSecretWithoutLambda,
			"RotateSecret":                      g.RotateSecret,
			"PutSecretValuePending":             g.PutSecretValuePending,
			"GetSecretValueByStage":             g.GetSecretValueByStage,
			"UpdateSecretVersionStage":          g.UpdateSecretVersionStage,
			"CancelRotateSecret":                g.CancelRotateSecret,
			"ValidateResourcePolicy":            g.ValidateResourcePolicy,
			"PutResourcePolicy":                 g.PutResourcePolicy,
			"GetResourcePolicy":                 g.GetResourcePolicy,
			"DeleteResourcePolicy":              g.DeleteResourcePolicy,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.setup,
			"secretsmanager-rotate": g.setupRotate,
			"secretsmanager-policy": g.setupPolicy,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"secretsmanager-crud":   g.teardown,
			"secretsmanager-rotate": g.teardownRotate,
			"secretsmanager-policy": g.teardownPolicy,
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
	if len(resp.Versions) == 0 {
		return fmt.Errorf("ListSecretVersionIds: no versions returned")
	}
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

// rotationLambdaARN is a placeholder function ARN. Rotation is configured with
// RotateImmediately=false throughout this group, so it is never invoked —
// driving the four-step protocol needs a real rotation Lambda and belongs with
// the Docker-dependent Lambda groups.
func rotationLambdaARN(t *harness.TestContext) string {
	return fmt.Sprintf("arn:aws:lambda:%s:000000000000:function:oc-rotate-fn", t.Region)
}

func (g *smGroup) RotateSecretWithoutLambda(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().RotateSecret(ctx, &secretsmanager.RotateSecretInput{
		SecretId:      aws.String(t.GetString("sm_rotate_arn")),
		RotationRules: &smtypes.RotationRulesType{AutomaticallyAfterDays: aws.Int64(30)},
	})
	if err == nil {
		return fmt.Errorf("RotateSecretWithoutLambda: expected InvalidRequestException, call succeeded")
	}
	if harness.IsUnimplemented(err) {
		return err
	}
	if !strings.Contains(err.Error(), "InvalidRequestException") {
		return fmt.Errorf("RotateSecretWithoutLambda: expected InvalidRequestException, got %w", err)
	}
	return nil
}

func (g *smGroup) RotateSecret(ctx context.Context, t *harness.TestContext) error {
	arn := rotationLambdaARN(t)
	_, err := g.cl().RotateSecret(ctx, &secretsmanager.RotateSecretInput{
		SecretId:          aws.String(t.GetString("sm_rotate_arn")),
		RotationLambdaARN: aws.String(arn),
		RotationRules:     &smtypes.RotationRulesType{AutomaticallyAfterDays: aws.Int64(30)},
		RotateImmediately: aws.Bool(false),
	})
	if err != nil {
		return err
	}
	desc, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return fmt.Errorf("RotateSecret: DescribeSecret verify failed: %w", err)
	}
	if !aws.ToBool(desc.RotationEnabled) {
		return fmt.Errorf("RotateSecret: RotationEnabled is false after configuring rotation")
	}
	if aws.ToString(desc.RotationLambdaARN) != arn {
		return fmt.Errorf("RotateSecret: RotationLambdaARN = %q, want %q", aws.ToString(desc.RotationLambdaARN), arn)
	}
	if desc.NextRotationDate == nil {
		return fmt.Errorf("RotateSecret: NextRotationDate not set")
	}
	return nil
}

func (g *smGroup) PutSecretValuePending(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:           aws.String(t.GetString("sm_rotate_arn")),
		SecretString:       aws.String("pending-value"),
		ClientRequestToken: aws.String(pendingToken),
		VersionStages:      []string{"AWSPENDING"},
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.VersionId) != pendingToken {
		return fmt.Errorf("PutSecretValuePending: VersionId = %q, want the ClientRequestToken %q",
			aws.ToString(resp.VersionId), pendingToken)
	}
	if !containsStage(resp.VersionStages, "AWSPENDING") {
		return fmt.Errorf("PutSecretValuePending: VersionStages = %v, want AWSPENDING", resp.VersionStages)
	}
	// Staging a pending version must not change AWSCURRENT.
	cur, err := g.cl().GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return fmt.Errorf("PutSecretValuePending: GetSecretValue verify failed: %w", err)
	}
	if aws.ToString(cur.SecretString) != "rotate-me" {
		return fmt.Errorf("PutSecretValuePending: AWSCURRENT = %q, want the untouched %q",
			aws.ToString(cur.SecretString), "rotate-me")
	}
	return nil
}

func (g *smGroup) GetSecretValueByStage(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String(t.GetString("sm_rotate_arn")),
		VersionStage: aws.String("AWSPENDING"),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.SecretString) != "pending-value" {
		return fmt.Errorf("GetSecretValueByStage: SecretString = %q, want %q",
			aws.ToString(resp.SecretString), "pending-value")
	}
	return nil
}

func (g *smGroup) UpdateSecretVersionStage(ctx context.Context, t *harness.TestContext) error {
	desc, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return err
	}
	current := ""
	for id, stages := range desc.VersionIdsToStages {
		if containsStage(stages, "AWSCURRENT") {
			current = id
		}
	}
	if current == "" {
		return fmt.Errorf("UpdateSecretVersionStage: no AWSCURRENT version")
	}

	// This is what a rotation function's finishSecret step does.
	if _, err := g.cl().UpdateSecretVersionStage(ctx, &secretsmanager.UpdateSecretVersionStageInput{
		SecretId:            aws.String(t.GetString("sm_rotate_arn")),
		VersionStage:        aws.String("AWSCURRENT"),
		MoveToVersionId:     aws.String(pendingToken),
		RemoveFromVersionId: aws.String(current),
	}); err != nil {
		return err
	}

	after, err := g.cl().GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return fmt.Errorf("UpdateSecretVersionStage: GetSecretValue verify failed: %w", err)
	}
	if aws.ToString(after.SecretString) != "pending-value" {
		return fmt.Errorf("UpdateSecretVersionStage: AWSCURRENT = %q, want %q",
			aws.ToString(after.SecretString), "pending-value")
	}
	if aws.ToString(after.VersionId) != pendingToken {
		return fmt.Errorf("UpdateSecretVersionStage: AWSCURRENT version = %q, want %q",
			aws.ToString(after.VersionId), pendingToken)
	}

	post, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return fmt.Errorf("UpdateSecretVersionStage: DescribeSecret verify failed: %w", err)
	}
	if !containsStage(post.VersionIdsToStages[current], "AWSPREVIOUS") {
		return fmt.Errorf("UpdateSecretVersionStage: displaced version stages = %v, want AWSPREVIOUS",
			post.VersionIdsToStages[current])
	}
	return nil
}

func (g *smGroup) CancelRotateSecret(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().CancelRotateSecret(ctx, &secretsmanager.CancelRotateSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return err
	}
	desc, err := g.cl().DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(t.GetString("sm_rotate_arn")),
	})
	if err != nil {
		return fmt.Errorf("CancelRotateSecret: DescribeSecret verify failed: %w", err)
	}
	if aws.ToBool(desc.RotationEnabled) {
		return fmt.Errorf("CancelRotateSecret: rotation still enabled after cancel")
	}
	// AWS keeps the function configured so rotation can be turned back on.
	if want := rotationLambdaARN(t); aws.ToString(desc.RotationLambdaARN) != want {
		return fmt.Errorf("CancelRotateSecret: RotationLambdaARN = %q, want it retained as %q",
			aws.ToString(desc.RotationLambdaARN), want)
	}
	return nil
}

func containsStage(stages []string, want string) bool {
	for _, s := range stages {
		if s == want {
			return true
		}
	}
	return false
}

// ── secretsmanager-policy ─────────────────────────────────────────────────────
//
// Overcast stores and validates a secret's resource policy but does not
// evaluate it, so these assert the round-trip and the validation verdict,
// never an access decision.

func (g *smGroup) setupPolicy(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc/%s/policy", t.RunID)
	resp, err := g.cl().CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String("policy-value"),
	})
	if err != nil {
		return err
	}
	t.Set("sm_policy_name", name)
	t.Set("sm_policy_arn", aws.ToString(resp.ARN))
	return nil
}

func (g *smGroup) teardownPolicy(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("sm_policy_arn"); arn != "" {
		g.cl().DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
			SecretId:                   aws.String(arn),
			ForceDeleteWithoutRecovery: aws.Bool(true),
		}) //nolint:errcheck
	}
	return nil
}

func (g *smGroup) ValidateResourcePolicy(ctx context.Context, t *harness.TestContext) error {
	ok, err := g.cl().ValidateResourcePolicy(ctx, &secretsmanager.ValidateResourcePolicyInput{
		ResourcePolicy: aws.String(compatResourcePolicy),
	})
	if err != nil {
		return err
	}
	if !ok.PolicyValidationPassed {
		return fmt.Errorf("ValidateResourcePolicy: valid policy rejected: %v", ok.ValidationErrors)
	}

	bad, err := g.cl().ValidateResourcePolicy(ctx, &secretsmanager.ValidateResourcePolicyInput{
		ResourcePolicy: aws.String(`{"Version":"2012-10-17"}`),
	})
	if err != nil {
		return fmt.Errorf("ValidateResourcePolicy: validating a bad policy errored: %w", err)
	}
	if bad.PolicyValidationPassed {
		return fmt.Errorf("ValidateResourcePolicy: policy with no Statement should not pass")
	}
	if len(bad.ValidationErrors) == 0 {
		return fmt.Errorf("ValidateResourcePolicy: no ValidationErrors for a failing policy")
	}
	return nil
}

func (g *smGroup) PutResourcePolicy(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().PutResourcePolicy(ctx, &secretsmanager.PutResourcePolicyInput{
		SecretId:       aws.String(t.GetString("sm_policy_arn")),
		ResourcePolicy: aws.String(compatResourcePolicy),
	})
	if err != nil {
		return err
	}
	if aws.ToString(resp.ARN) == "" {
		return fmt.Errorf("PutResourcePolicy: missing ARN")
	}
	if got, want := aws.ToString(resp.Name), t.GetString("sm_policy_name"); got != want {
		return fmt.Errorf("PutResourcePolicy: Name = %q, want %q", got, want)
	}
	return nil
}

func (g *smGroup) GetResourcePolicy(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GetResourcePolicy(ctx, &secretsmanager.GetResourcePolicyInput{
		SecretId: aws.String(t.GetString("sm_policy_arn")),
	})
	if err != nil {
		return err
	}
	policy := aws.ToString(resp.ResourcePolicy)
	if !strings.Contains(policy, "OvercastCompatRead") {
		return fmt.Errorf("GetResourcePolicy: policy %q does not carry the Sid that was stored", policy)
	}
	return nil
}

func (g *smGroup) DeleteResourcePolicy(ctx context.Context, t *harness.TestContext) error {
	if _, err := g.cl().DeleteResourcePolicy(ctx, &secretsmanager.DeleteResourcePolicyInput{
		SecretId: aws.String(t.GetString("sm_policy_arn")),
	}); err != nil {
		return err
	}
	resp, err := g.cl().GetResourcePolicy(ctx, &secretsmanager.GetResourcePolicyInput{
		SecretId: aws.String(t.GetString("sm_policy_arn")),
	})
	if err != nil {
		return fmt.Errorf("DeleteResourcePolicy: GetResourcePolicy verify failed: %w", err)
	}
	if aws.ToString(resp.ResourcePolicy) != "" {
		return fmt.Errorf("DeleteResourcePolicy: policy still present: %q", aws.ToString(resp.ResourcePolicy))
	}
	return nil
}
