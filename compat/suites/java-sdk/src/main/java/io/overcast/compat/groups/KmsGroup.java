package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.core.SdkBytes;
import software.amazon.awssdk.services.kms.KmsClient;
import software.amazon.awssdk.services.kms.model.*;

import java.util.Map;

/**
 * KMS compatibility test group.
 *
 * <p>Groups: kms-keys, kms-crypto, kms-asymmetric.
 */
public final class KmsGroup implements ServiceGroup {

    private final AwsClients clients;

    public KmsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private KmsClient kms() { return clients.kms(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateKey",                      this::createKey),
                Map.entry("DescribeKey",                    this::describeKey),
                Map.entry("CreateKmsAlias",                 this::createKmsAlias),
                Map.entry("ListKmsAliases",                 this::listKmsAliases),
                Map.entry("ListKeys",                       this::listKeys),
                Map.entry("DisableKey",                     this::disableKey),
                Map.entry("EnableKey",                      this::enableKey),
                Map.entry("ScheduleKeyDeletion",            this::scheduleKeyDeletion),
                Map.entry("CancelKeyDeletion",              this::cancelKeyDeletion),
                Map.entry("TagKMSResource",                 this::tagKmsResource),
                Map.entry("UntagKMSResource",               this::untagKmsResource),
                Map.entry("ListKMSResourceTags",            this::listKmsResourceTags),
                Map.entry("Encrypt",                        this::encrypt),
                Map.entry("Decrypt",                        this::decrypt),
                Map.entry("GenerateDataKey",                this::generateDataKey),
                Map.entry("GenerateDataKeyWithoutPlaintext",this::generateDataKeyWithoutPlaintext),
                Map.entry("Sign",                           this::sign),
                Map.entry("Verify",                         this::verify)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("kms-keys",       this::setupKeys),
                Map.entry("kms-crypto",     this::setupCrypto),
                Map.entry("kms-asymmetric", this::setupAsymmetric)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("kms-keys",       this::teardownKeys),
                Map.entry("kms-crypto",     ctx -> scheduleDeleteSilently(ctx.getString("kmsCryptoKeyId"))),
                Map.entry("kms-asymmetric", ctx -> scheduleDeleteSilently(ctx.getString("kmsAsymKeyId")))
        );
    }

    // ── kms-keys ───────────────────────────────────────────────────────────────

    private void setupKeys(TestContext ctx) throws Exception {
        var resp = kms().createKey(r -> r.description("compat-test-key").keyUsage(KeyUsageType.ENCRYPT_DECRYPT));
        ctx.set("kmsKeyId", resp.keyMetadata().keyId());
        ctx.set("kmsAliasName", "alias/compat-" + ctx.runId());
    }

    private void teardownKeys(TestContext ctx) {
        // Delete alias first, then schedule key deletion.
        String alias = ctx.getString("kmsAliasName");
        if (alias != null) {
            try { kms().deleteAlias(r -> r.aliasName(alias)); } catch (Exception ignored) {}
        }
        scheduleDeleteSilently(ctx.getString("kmsKeyId"));
    }

    private void createKey(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("kmsKeyId"), "kmsKeyId");
    }

    private void describeKey(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        var resp = kms().describeKey(r -> r.keyId(keyId));
        Assertions.assertEquals(keyId, resp.keyMetadata().keyId(), "DescribeKey: keyId mismatch");
        Assertions.assertEquals(KeyState.ENABLED, resp.keyMetadata().keyState(), "DescribeKey: key not ENABLED");
    }

    private void createKmsAlias(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        String alias = ctx.getString("kmsAliasName");
        kms().createAlias(r -> r.aliasName(alias).targetKeyId(keyId));
    }

    private void listKmsAliases(TestContext ctx) throws Exception {
        String alias = ctx.getString("kmsAliasName");
        var resp = kms().listAliases();
        boolean found = resp.aliases().stream().anyMatch(a -> a.aliasName().equals(alias));
        Assertions.assertTrue(found, "ListKmsAliases: alias " + alias + " not found");
    }

    private void listKeys(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        var resp = kms().listKeys();
        boolean found = resp.keys().stream().anyMatch(k -> k.keyId().equals(keyId));
        Assertions.assertTrue(found, "ListKeys: key not found");
    }

    private void disableKey(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        kms().disableKey(r -> r.keyId(keyId));
        var resp = kms().describeKey(r -> r.keyId(keyId));
        Assertions.assertEquals(KeyState.DISABLED, resp.keyMetadata().keyState(), "DisableKey: state should be DISABLED");
    }

    private void enableKey(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        kms().enableKey(r -> r.keyId(keyId));
        var resp = kms().describeKey(r -> r.keyId(keyId));
        Assertions.assertEquals(KeyState.ENABLED, resp.keyMetadata().keyState(), "EnableKey: state should be ENABLED");
    }

    private void scheduleKeyDeletion(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        kms().scheduleKeyDeletion(r -> r.keyId(keyId).pendingWindowInDays(7));
        var resp = kms().describeKey(r -> r.keyId(keyId));
        Assertions.assertEquals(KeyState.PENDING_DELETION, resp.keyMetadata().keyState(),
                "ScheduleKeyDeletion: state should be PENDING_DELETION");
    }

    private void cancelKeyDeletion(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        kms().cancelKeyDeletion(r -> r.keyId(keyId));
        var resp = kms().describeKey(r -> r.keyId(keyId));
        Assertions.assertEquals(KeyState.DISABLED, resp.keyMetadata().keyState(),
                "CancelKeyDeletion: state should be DISABLED after cancel");
    }

    private void tagKmsResource(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        kms().tagResource(r -> r.keyId(keyId).tags(
                Tag.builder().tagKey("env").tagValue("test").build()));
    }

    private void untagKmsResource(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        kms().untagResource(r -> r.keyId(keyId).tagKeys("env"));
    }

    private void listKmsResourceTags(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsKeyId");
        var resp = kms().listResourceTags(r -> r.keyId(keyId));
        Assertions.assertNotNull(resp.tags(), "ListKMSResourceTags: tags is null");
    }

    // ── kms-crypto ────────────────────────────────────────────────────────────

    private void setupCrypto(TestContext ctx) throws Exception {
        var resp = kms().createKey(r -> r.description("compat-crypto-key").keyUsage(KeyUsageType.ENCRYPT_DECRYPT));
        ctx.set("kmsCryptoKeyId", resp.keyMetadata().keyId());
    }

    private void encrypt(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsCryptoKeyId");
        var resp = kms().encrypt(r -> r.keyId(keyId)
                .plaintext(SdkBytes.fromUtf8String("hello KMS")));
        Assertions.assertNotNull(resp.ciphertextBlob(), "Encrypt: ciphertextBlob");
        ctx.set("kmsCiphertext", resp.ciphertextBlob());
    }

    private void decrypt(TestContext ctx) throws Exception {
        String keyId     = ctx.getString("kmsCryptoKeyId");
        SdkBytes cipher  = ctx.get("kmsCiphertext");
        Assertions.assertNotNull(cipher, "Decrypt: no ciphertext from Encrypt step");
        var resp = kms().decrypt(r -> r.keyId(keyId).ciphertextBlob(cipher));
        String plaintext = resp.plaintext().asUtf8String();
        Assertions.assertEquals("hello KMS", plaintext, "Decrypt: plaintext mismatch");
    }

    private void generateDataKey(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsCryptoKeyId");
        var resp = kms().generateDataKey(r -> r.keyId(keyId).keySpec(DataKeySpec.AES_256));
        Assertions.assertNotNull(resp.plaintext(), "GenerateDataKey: plaintext");
        Assertions.assertNotNull(resp.ciphertextBlob(), "GenerateDataKey: ciphertextBlob");
    }

    private void generateDataKeyWithoutPlaintext(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsCryptoKeyId");
        var resp = kms().generateDataKeyWithoutPlaintext(r -> r.keyId(keyId).keySpec(DataKeySpec.AES_256));
        Assertions.assertNotNull(resp.ciphertextBlob(), "GenerateDataKeyWithoutPlaintext: ciphertextBlob");
    }

    // ── kms-asymmetric ────────────────────────────────────────────────────────

    private void setupAsymmetric(TestContext ctx) throws Exception {
        var resp = kms().createKey(r -> r
                .description("compat-asym-key")
                .keyUsage(KeyUsageType.SIGN_VERIFY)
                .keySpec(KeySpec.RSA_2048));
        ctx.set("kmsAsymKeyId", resp.keyMetadata().keyId());
    }

    private void sign(TestContext ctx) throws Exception {
        String keyId = ctx.getString("kmsAsymKeyId");
        var resp = kms().sign(r -> r.keyId(keyId)
                .message(SdkBytes.fromUtf8String("sign this message"))
                .messageType(MessageType.RAW)
                .signingAlgorithm(SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256));
        Assertions.assertNotNull(resp.signature(), "Sign: signature");
        ctx.set("kmsSignature", resp.signature());
    }

    private void verify(TestContext ctx) throws Exception {
        String keyId      = ctx.getString("kmsAsymKeyId");
        SdkBytes sigBytes = ctx.get("kmsSignature");
        Assertions.assertNotNull(sigBytes, "Verify: no signature from Sign step");
        var resp = kms().verify(r -> r.keyId(keyId)
                .message(SdkBytes.fromUtf8String("sign this message"))
                .messageType(MessageType.RAW)
                .signingAlgorithm(SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256)
                .signature(sigBytes));
        Assertions.assertTrue(resp.signatureValid(), "Verify: signature is not valid");
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void scheduleDeleteSilently(String keyId) {
        if (keyId == null) return;
        try { kms().scheduleKeyDeletion(r -> r.keyId(keyId).pendingWindowInDays(7)); }
        catch (Exception ignored) {}
    }
}
