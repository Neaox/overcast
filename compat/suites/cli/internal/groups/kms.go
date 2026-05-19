package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// KMS returns the KMS service group.
func KMS() ServiceGroup {
	g := &kmsGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// kms-keys
			"CreateKey":           g.CreateKey,
			"DescribeKey":         g.DescribeKey,
			"CreateKmsAlias":      g.CreateAlias,
			"ListKmsAliases":      g.ListAliases,
			"ListKeys":            g.ListKeys,
			"DisableKey":          g.DisableKey,
			"EnableKey":           g.EnableKey,
			"ScheduleKeyDeletion": g.ScheduleKeyDeletion,
			"CancelKeyDeletion":   g.CancelKeyDeletion,
			"TagKMSResource":      g.TagKMSResource,
			"ListKMSResourceTags": g.ListKMSResourceTags,
			"UntagKMSResource":    g.UntagKMSResource,
			// kms-crypto
			"Encrypt":                         g.Encrypt,
			"Decrypt":                         g.Decrypt,
			"GenerateDataKey":                 g.GenerateDataKey,
			"GenerateDataKeyWithoutPlaintext": g.GenerateDataKeyWithoutPlaintext,
			// kms-asymmetric
			"Sign":   g.Sign,
			"Verify": g.Verify,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"kms-keys":       g.setupKeys,
			"kms-crypto":     g.setupCrypto,
			"kms-asymmetric": g.setupAsymmetric,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"kms-keys":       g.teardownKeys,
			"kms-crypto":     g.teardownCrypto,
			"kms-asymmetric": g.teardownAsymmetric,
		},
	}
}

type kmsGroup struct{}

func (g *kmsGroup) aliasName(t *harness.TestContext) string {
	return fmt.Sprintf("alias/oc-%s", t.RunID)
}

// ─── kms-keys ────────────────────────────────────────────────────────────────

func (g *kmsGroup) setupKeys(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *kmsGroup) CreateKey(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "create-key",
		"--description", fmt.Sprintf("%s-key", t.RunID),
	)
	if err != nil {
		return err
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	keyID, _ := meta["KeyId"].(string)
	if keyID == "" {
		return fmt.Errorf("kms CreateKey: missing KeyId")
	}
	t.Set("key_id", keyID)
	return nil
}

func (g *kmsGroup) DescribeKey(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "describe-key", "--key-id", keyID,
	)
	if err != nil {
		return err
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	if state, _ := meta["KeyState"].(string); state == "" {
		return fmt.Errorf("kms DescribeKey: missing KeyState")
	}
	return nil
}

func (g *kmsGroup) CreateAlias(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	if err := awscli.Run(t.Endpoint, t.Region,
		"kms", "create-alias",
		"--alias-name", g.aliasName(t),
		"--target-key-id", keyID,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "list-aliases")
	if err != nil {
		return fmt.Errorf("kms CreateAlias: list-aliases failed: %w", err)
	}
	aliases, _ := out["Aliases"].([]any)
	for _, raw := range aliases {
		if m, ok := raw.(map[string]any); ok && m["AliasName"] == g.aliasName(t) {
			return nil
		}
	}
	return fmt.Errorf("kms CreateAlias: alias %q not found", g.aliasName(t))
}

func (g *kmsGroup) ListAliases(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "list-aliases")
	if err != nil {
		return err
	}
	aliases, _ := out["Aliases"].([]any)
	want := g.aliasName(t)
	for _, raw := range aliases {
		if m, ok := raw.(map[string]any); ok {
			if m["AliasName"] == want {
				return nil
			}
		}
	}
	return fmt.Errorf("kms ListAliases: alias %q not found", want)
}

func (g *kmsGroup) ListKeys(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "list-keys")
	if err != nil {
		return err
	}
	keys, _ := out["Keys"].([]any)
	want := t.GetString("key_id")
	for _, raw := range keys {
		if m, ok := raw.(map[string]any); ok {
			if m["KeyId"] == want {
				return nil
			}
		}
	}
	return fmt.Errorf("kms ListKeys: key %q not found", want)
}

func (g *kmsGroup) DisableKey(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	if err := awscli.Run(t.Endpoint, t.Region, "kms", "disable-key", "--key-id", keyID); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "describe-key", "--key-id", keyID)
	if err != nil {
		return fmt.Errorf("kms DisableKey: describe-key failed: %w", err)
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	if meta["KeyState"] != "Disabled" {
		return fmt.Errorf("kms DisableKey: expected KeyState=Disabled, got %v", meta["KeyState"])
	}
	return nil
}

func (g *kmsGroup) EnableKey(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	if err := awscli.Run(t.Endpoint, t.Region, "kms", "enable-key", "--key-id", keyID); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "describe-key", "--key-id", keyID)
	if err != nil {
		return fmt.Errorf("kms EnableKey: describe-key failed: %w", err)
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	if meta["KeyState"] != "Enabled" {
		return fmt.Errorf("kms EnableKey: expected KeyState=Enabled, got %v", meta["KeyState"])
	}
	return nil
}

func (g *kmsGroup) ScheduleKeyDeletion(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	if err := awscli.Run(t.Endpoint, t.Region,
		"kms", "schedule-key-deletion",
		"--key-id", keyID,
		"--pending-window-in-days", "7",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "describe-key", "--key-id", keyID)
	if err != nil {
		return fmt.Errorf("kms ScheduleKeyDeletion: describe-key failed: %w", err)
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	if meta["KeyState"] != "PendingDeletion" {
		return fmt.Errorf("kms ScheduleKeyDeletion: expected KeyState=PendingDeletion, got %v", meta["KeyState"])
	}
	return nil
}

func (g *kmsGroup) CancelKeyDeletion(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	if err := awscli.Run(t.Endpoint, t.Region,
		"kms", "cancel-key-deletion",
		"--key-id", keyID,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "kms", "describe-key", "--key-id", keyID)
	if err != nil {
		return fmt.Errorf("kms CancelKeyDeletion: describe-key failed: %w", err)
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	if state, _ := meta["KeyState"].(string); state == "PendingDeletion" {
		return fmt.Errorf("kms CancelKeyDeletion: key still in PendingDeletion after cancel")
	}
	return nil
}

func (g *kmsGroup) TagKMSResource(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	return awscli.Run(t.Endpoint, t.Region,
		"kms", "tag-resource",
		"--key-id", keyID,
		"--tags", `[{"TagKey":"env","TagValue":"test"}]`,
	)
}

func (g *kmsGroup) ListKMSResourceTags(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "list-resource-tags",
		"--key-id", keyID,
	)
	if err != nil {
		return err
	}
	tags, _ := out["Tags"].([]any)
	for _, raw := range tags {
		if m, ok := raw.(map[string]any); ok && m["TagKey"] == "env" {
			return nil
		}
	}
	return fmt.Errorf("kms ListKMSResourceTags: tag 'env' not found")
}

func (g *kmsGroup) UntagKMSResource(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	return awscli.Run(t.Endpoint, t.Region,
		"kms", "untag-resource",
		"--key-id", keyID,
		"--tag-keys", "env",
	)
}

func (g *kmsGroup) teardownKeys(_ context.Context, t *harness.TestContext) error {
	if keyID := t.GetString("key_id"); keyID != "" {
		// Delete alias first.
		awscli.Run(t.Endpoint, t.Region, "kms", "delete-alias", "--alias-name", g.aliasName(t)) //nolint:errcheck
		// Schedule deletion with minimum window.
		awscli.Run(t.Endpoint, t.Region, "kms", "schedule-key-deletion", //nolint:errcheck
			"--key-id", keyID, "--pending-window-in-days", "7")
	}
	return nil
}

// ─── kms-crypto ──────────────────────────────────────────────────────────────

func (g *kmsGroup) setupCrypto(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "create-key",
		"--description", fmt.Sprintf("%s-crypto", t.RunID),
	)
	if err != nil {
		return err
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	keyID, _ := meta["KeyId"].(string)
	t.Set("key_id", keyID)
	return nil
}

func (g *kmsGroup) Encrypt(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	plaintext := encodeBase64([]byte("hello from CLI"))
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "encrypt",
		"--key-id", keyID,
		"--plaintext", plaintext,
	)
	if err != nil {
		return err
	}
	blob, _ := out["CiphertextBlob"].(string)
	if blob == "" {
		return fmt.Errorf("kms Encrypt: missing CiphertextBlob")
	}
	t.Set("ciphertext_blob", blob)
	return nil
}

func (g *kmsGroup) Decrypt(_ context.Context, t *harness.TestContext) error {
	blob := t.GetString("ciphertext_blob")
	if blob == "" {
		return fmt.Errorf("kms Decrypt: missing ciphertext_blob in state")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "decrypt",
		"--ciphertext-blob", blob,
	)
	if err != nil {
		return err
	}
	if out["Plaintext"] == nil {
		return fmt.Errorf("kms Decrypt: missing Plaintext")
	}
	return nil
}

func (g *kmsGroup) GenerateDataKey(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "generate-data-key",
		"--key-id", keyID,
		"--key-spec", "AES_256",
	)
	if err != nil {
		return err
	}
	if out["Plaintext"] == nil {
		return fmt.Errorf("kms GenerateDataKey: missing Plaintext")
	}
	return nil
}

func (g *kmsGroup) GenerateDataKeyWithoutPlaintext(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("key_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "generate-data-key-without-plaintext",
		"--key-id", keyID,
		"--key-spec", "AES_256",
	)
	if err != nil {
		return err
	}
	if out["CiphertextBlob"] == nil {
		return fmt.Errorf("kms GenerateDataKeyWithoutPlaintext: missing CiphertextBlob")
	}
	return nil
}

func (g *kmsGroup) teardownCrypto(_ context.Context, t *harness.TestContext) error {
	if keyID := t.GetString("key_id"); keyID != "" {
		awscli.Run(t.Endpoint, t.Region, "kms", "schedule-key-deletion", //nolint:errcheck
			"--key-id", keyID, "--pending-window-in-days", "7")
	}
	return nil
}

// ─── kms-asymmetric ──────────────────────────────────────────────────────────

func (g *kmsGroup) setupAsymmetric(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "create-key",
		"--description", fmt.Sprintf("%s-asym", t.RunID),
		"--key-spec", "RSA_2048",
		"--key-usage", "SIGN_VERIFY",
	)
	if err != nil {
		return err
	}
	meta, _ := out["KeyMetadata"].(map[string]any)
	keyID, _ := meta["KeyId"].(string)
	t.Set("asym_key_id", keyID)
	return nil
}

func (g *kmsGroup) Sign(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("asym_key_id")
	msg := encodeBase64([]byte("message to sign"))
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "sign",
		"--key-id", keyID,
		"--message", msg,
		"--message-type", "RAW",
		"--signing-algorithm", "RSASSA_PKCS1_V1_5_SHA_256",
	)
	if err != nil {
		return err
	}
	sig, _ := out["Signature"].(string)
	t.Set("signature", sig)
	return nil
}

func (g *kmsGroup) Verify(_ context.Context, t *harness.TestContext) error {
	keyID := t.GetString("asym_key_id")
	sig := t.GetString("signature")
	if sig == "" {
		return fmt.Errorf("kms Verify: missing signature in state")
	}
	msg := encodeBase64([]byte("message to sign"))
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"kms", "verify",
		"--key-id", keyID,
		"--message", msg,
		"--message-type", "RAW",
		"--signing-algorithm", "RSASSA_PKCS1_V1_5_SHA_256",
		"--signature", sig,
	)
	if err != nil {
		return err
	}
	if valid, _ := out["SignatureValid"].(bool); !valid {
		return fmt.Errorf("kms Verify: SignatureValid is not true")
	}
	return nil
}

func (g *kmsGroup) teardownAsymmetric(_ context.Context, t *harness.TestContext) error {
	if keyID := t.GetString("asym_key_id"); keyID != "" {
		awscli.Run(t.Endpoint, t.Region, "kms", "schedule-key-deletion", //nolint:errcheck
			"--key-id", keyID, "--pending-window-in-days", "7")
	}
	return nil
}
