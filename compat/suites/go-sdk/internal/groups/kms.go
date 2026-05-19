package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func KMS(c *clients.Clients) ServiceGroup {
	g := &kmsGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateKey":                       g.CreateKey,
			"DescribeKey":                     g.DescribeKey,
			"ListKeys":                        g.ListKeys,
			"EnableKey":                       g.EnableKey,
			"DisableKey":                      g.DisableKey,
			"Encrypt":                         g.Encrypt,
			"Decrypt":                         g.Decrypt,
			"GenerateDataKey":                 g.GenerateDataKey,
			"GenerateDataKeyWithoutPlaintext": g.GenerateDataKeyWithoutPlaintext,
			"TagKMSResource":                  g.TagKMSResource,
			"UntagKMSResource":                g.UntagKMSResource,
			"ListKMSResourceTags":             g.ListKMSResourceTags,
			"ScheduleKeyDeletion":             g.ScheduleKeyDeletion,
			"CancelKeyDeletion":               g.CancelKeyDeletion,
			"Sign":                            g.Sign,
			"Verify":                          g.Verify,
			"CreateKmsAlias":                  g.CreateKmsAlias,
			"ListKmsAliases":                  g.ListKmsAliases,
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

type kmsGroup struct{ c *clients.Clients }

func (g *kmsGroup) cl() *kms.Client { return g.c.KMS() }

// ── kms-keys ──────────────────────────────────────────────────────────────────

func (g *kmsGroup) setupKeys(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String(fmt.Sprintf("oc-key-%s", t.RunID)),
	})
	if err != nil {
		return err
	}
	t.Set("kms_key_id", aws.ToString(resp.KeyMetadata.KeyId))
	return nil
}

func (g *kmsGroup) teardownKeys(ctx context.Context, t *harness.TestContext) error {
	if keyID := t.GetString("kms_key_id"); keyID != "" {
		// Delete alias before scheduling key deletion — alias outlives the key otherwise.
		g.cl().DeleteAlias(ctx, &kms.DeleteAliasInput{ //nolint:errcheck
			AliasName: aws.String(fmt.Sprintf("alias/compat-%s", t.RunID)),
		})
		g.cl().ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
			KeyId:               aws.String(keyID),
			PendingWindowInDays: aws.Int32(7),
		}) //nolint:errcheck
	}
	if keyID := t.GetString("kms_pending_key_id"); keyID != "" {
		g.cl().CancelKeyDeletion(ctx, &kms.CancelKeyDeletionInput{KeyId: aws.String(keyID)}) //nolint:errcheck
		g.cl().ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
			KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
		}) //nolint:errcheck
	}
	return nil
}

func (g *kmsGroup) CreateKey(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String("oc-test-key"),
	})
	if err == nil {
		g.cl().ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
			KeyId: resp.KeyMetadata.KeyId, PendingWindowInDays: aws.Int32(7),
		}) //nolint:errcheck
	}
	return err
}

func (g *kmsGroup) DescribeKey(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: aws.String(t.GetString("kms_key_id")),
	})
	if err != nil {
		return err
	}
	if resp.KeyMetadata == nil {
		return fmt.Errorf("DescribeKey: nil metadata")
	}
	return nil
}

func (g *kmsGroup) ListKeys(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListKeys(ctx, &kms.ListKeysInput{})
	if err != nil {
		return err
	}
	if len(resp.Keys) == 0 {
		return fmt.Errorf("ListKeys: expected ≥1 key")
	}
	return nil
}

func (g *kmsGroup) EnableKey(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().EnableKey(ctx, &kms.EnableKeyInput{KeyId: aws.String(t.GetString("kms_key_id"))})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(t.GetString("kms_key_id"))})
	if err != nil {
		return fmt.Errorf("EnableKey: DescribeKey verify failed: %w", err)
	}
	if !resp.KeyMetadata.Enabled {
		return fmt.Errorf("EnableKey: key not enabled after EnableKey")
	}
	return nil
}

func (g *kmsGroup) DisableKey(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DisableKey(ctx, &kms.DisableKeyInput{KeyId: aws.String(t.GetString("kms_key_id"))})
	if err != nil {
		return err
	}
	resp, err := g.cl().DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(t.GetString("kms_key_id"))})
	if err != nil {
		return fmt.Errorf("DisableKey: DescribeKey verify failed: %w", err)
	}
	if resp.KeyMetadata.Enabled {
		return fmt.Errorf("DisableKey: key still enabled after DisableKey")
	}
	// re-enable for subsequent tests
	g.cl().EnableKey(ctx, &kms.EnableKeyInput{KeyId: aws.String(t.GetString("kms_key_id"))}) //nolint:errcheck
	return nil
}

func (g *kmsGroup) TagKMSResource(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().TagResource(ctx, &kms.TagResourceInput{
		KeyId: aws.String(t.GetString("kms_key_id")),
		Tags: []types.Tag{
			{TagKey: aws.String("env"), TagValue: aws.String("test")},
			{TagKey: aws.String("run"), TagValue: aws.String(t.RunID)},
		},
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ListResourceTags(ctx, &kms.ListResourceTagsInput{
		KeyId: aws.String(t.GetString("kms_key_id")),
	})
	if err != nil {
		return fmt.Errorf("TagKMSResource: ListResourceTags verify failed: %w", err)
	}
	for _, tag := range resp.Tags {
		if aws.ToString(tag.TagKey) == "env" && aws.ToString(tag.TagValue) == "test" {
			return nil
		}
	}
	return fmt.Errorf("TagKMSResource: env=test tag not found")
}

func (g *kmsGroup) UntagKMSResource(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().UntagResource(ctx, &kms.UntagResourceInput{
		KeyId:   aws.String(t.GetString("kms_key_id")),
		TagKeys: []string{"run"},
	})
	return err
}

func (g *kmsGroup) ListKMSResourceTags(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListResourceTags(ctx, &kms.ListResourceTagsInput{
		KeyId: aws.String(t.GetString("kms_key_id")),
	})
	if err != nil {
		return err
	}
	if len(resp.Tags) == 0 {
		return fmt.Errorf("ListKMSResourceTags: expected ≥1 tag")
	}
	return nil
}

func (g *kmsGroup) ScheduleKeyDeletion(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String("oc-sched-del"),
	})
	if err != nil {
		return err
	}
	keyID := aws.ToString(resp.KeyMetadata.KeyId)
	t.Set("kms_pending_key_id", keyID)
	_, err = g.cl().ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
		KeyId:               aws.String(keyID),
		PendingWindowInDays: aws.Int32(7),
	})
	return err
}

func (g *kmsGroup) CancelKeyDeletion(ctx context.Context, t *harness.TestContext) error {
	keyID := t.GetString("kms_pending_key_id")
	if keyID == "" {
		return nil
	}
	_, err := g.cl().CancelKeyDeletion(ctx, &kms.CancelKeyDeletionInput{KeyId: aws.String(keyID)})
	return err
}

func (g *kmsGroup) CreateKmsAlias(ctx context.Context, t *harness.TestContext) error {
	aliasName := fmt.Sprintf("alias/compat-%s", t.RunID)
	_, err := g.cl().CreateAlias(ctx, &kms.CreateAliasInput{
		AliasName:   aws.String(aliasName),
		TargetKeyId: aws.String(t.GetString("kms_key_id")),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ListAliases(ctx, &kms.ListAliasesInput{KeyId: aws.String(t.GetString("kms_key_id"))})
	if err != nil {
		return fmt.Errorf("CreateKmsAlias: ListAliases verify failed: %w", err)
	}
	for _, a := range resp.Aliases {
		if aws.ToString(a.AliasName) == aliasName {
			return nil
		}
	}
	return fmt.Errorf("CreateKmsAlias: alias %q not found", aliasName)
}

func (g *kmsGroup) ListKmsAliases(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().ListAliases(ctx, &kms.ListAliasesInput{
		KeyId: aws.String(t.GetString("kms_key_id")),
	})
	if err != nil {
		return err
	}
	aliasName := fmt.Sprintf("alias/compat-%s", t.RunID)
	for _, a := range resp.Aliases {
		if aws.ToString(a.AliasName) == aliasName {
			return nil
		}
	}
	return fmt.Errorf("ListAliases: alias %q not found", aliasName)
}

// ── kms-crypto ────────────────────────────────────────────────────────────────

func (g *kmsGroup) setupCrypto(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String(fmt.Sprintf("oc-crypto-%s", t.RunID)),
	})
	if err != nil {
		return err
	}
	t.Set("kms_crypto_key_id", aws.ToString(resp.KeyMetadata.KeyId))
	return nil
}

func (g *kmsGroup) teardownCrypto(ctx context.Context, t *harness.TestContext) error {
	if keyID := t.GetString("kms_crypto_key_id"); keyID != "" {
		g.cl().ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
			KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
		}) //nolint:errcheck
	}
	return nil
}

func (g *kmsGroup) Encrypt(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(t.GetString("kms_crypto_key_id")),
		Plaintext: []byte("hello world"),
	})
	if err != nil {
		return err
	}
	t.Set("kms_ciphertext", resp.CiphertextBlob)
	return nil
}

func (g *kmsGroup) Decrypt(ctx context.Context, t *harness.TestContext) error {
	ct, ok := t.Get("kms_ciphertext")
	if !ok {
		// encrypt first
		encResp, err := g.cl().Encrypt(ctx, &kms.EncryptInput{
			KeyId: aws.String(t.GetString("kms_crypto_key_id")), Plaintext: []byte("hello world"),
		})
		if err != nil {
			return err
		}
		ct = encResp.CiphertextBlob
	}
	resp, err := g.cl().Decrypt(ctx, &kms.DecryptInput{CiphertextBlob: ct.([]byte)})
	if err != nil {
		return err
	}
	if string(resp.Plaintext) != "hello world" {
		return fmt.Errorf("Decrypt: expected plaintext %q, got %q", "hello world", string(resp.Plaintext))
	}
	return nil
}

func (g *kmsGroup) GenerateDataKey(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:   aws.String(t.GetString("kms_crypto_key_id")),
		KeySpec: types.DataKeySpecAes256,
	})
	if err != nil {
		return err
	}
	if len(resp.Plaintext) == 0 {
		return fmt.Errorf("GenerateDataKey: empty plaintext")
	}
	return nil
}

func (g *kmsGroup) GenerateDataKeyWithoutPlaintext(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GenerateDataKeyWithoutPlaintext(ctx, &kms.GenerateDataKeyWithoutPlaintextInput{
		KeyId:   aws.String(t.GetString("kms_crypto_key_id")),
		KeySpec: types.DataKeySpecAes256,
	})
	if err != nil {
		return err
	}
	if len(resp.CiphertextBlob) == 0 {
		return fmt.Errorf("GenerateDataKeyWithoutPlaintext: empty ciphertext")
	}
	return nil
}

func (g *kmsGroup) GenerateRandom(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().GenerateRandom(ctx, &kms.GenerateRandomInput{
		NumberOfBytes: aws.Int32(32),
	})
	if err != nil {
		if harness.IsUnimplemented(err) {
			return nil
		}
		return err
	}
	if len(resp.Plaintext) == 0 {
		return fmt.Errorf("GenerateRandom: empty bytes")
	}
	return nil
}

// ── kms-asymmetric ────────────────────────────────────────────────────────────

func (g *kmsGroup) setupAsymmetric(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String(fmt.Sprintf("oc-asym-%s", t.RunID)),
		KeySpec:     types.KeySpecRsa2048,
		KeyUsage:    types.KeyUsageTypeSignVerify,
	})
	if err != nil {
		return err
	}
	t.Set("kms_asym_key_id", aws.ToString(resp.KeyMetadata.KeyId))
	return nil
}

func (g *kmsGroup) teardownAsymmetric(ctx context.Context, t *harness.TestContext) error {
	if keyID := t.GetString("kms_asym_key_id"); keyID != "" {
		g.cl().ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
			KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
		}) //nolint:errcheck
	}
	return nil
}

func (g *kmsGroup) Sign(ctx context.Context, t *harness.TestContext) error {
	keyID := t.GetString("kms_asym_key_id")
	resp, err := g.cl().Sign(ctx, &kms.SignInput{
		KeyId:            aws.String(keyID),
		Message:          []byte("test data"),
		MessageType:      types.MessageTypeRaw,
		SigningAlgorithm: types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
	})
	if err != nil {
		return err
	}
	if len(resp.Signature) == 0 {
		return fmt.Errorf("Sign: empty signature")
	}
	t.Set("kms_signature", resp.Signature)
	return nil
}

func (g *kmsGroup) Verify(ctx context.Context, t *harness.TestContext) error {
	keyID := t.GetString("kms_asym_key_id")
	sig, ok := t.Get("kms_signature")
	if !ok {
		// Sign must run first; sign inline
		signResp, err := g.cl().Sign(ctx, &kms.SignInput{
			KeyId:            aws.String(keyID),
			Message:          []byte("test data"),
			MessageType:      types.MessageTypeRaw,
			SigningAlgorithm: types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
		})
		if err != nil {
			return err
		}
		sig = signResp.Signature
	}
	resp, err := g.cl().Verify(ctx, &kms.VerifyInput{
		KeyId:            aws.String(keyID),
		Message:          []byte("test data"),
		MessageType:      types.MessageTypeRaw,
		Signature:        sig.([]byte),
		SigningAlgorithm: types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
	})
	if err != nil {
		return err
	}
	if !resp.SignatureValid {
		return fmt.Errorf("Verify: signature not valid")
	}
	return nil
}
