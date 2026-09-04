package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.secretsmanager.SecretsManagerClient;
import software.amazon.awssdk.services.secretsmanager.model.*;

import java.util.Map;

/**
 * Secrets Manager compatibility test group.
 *
 * <p>Groups: secretsmanager-crud, secretsmanager-rotate, secretsmanager-policy.
 */
public final class SecretsManagerGroup implements ServiceGroup {

    /**
     * AWS wants a ClientRequestToken of 32-64 characters, and the token becomes
     * the version ID, so a fixed UUID keeps the assertions readable.
     */
    private static final String PENDING_TOKEN = "0f9c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f";

    /** A minimal, well-formed secret resource policy. */
    private static final String COMPAT_RESOURCE_POLICY =
            "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Sid\":\"OvercastCompatRead\","
            + "\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::000000000000:root\"},"
            + "\"Action\":\"secretsmanager:GetSecretValue\",\"Resource\":\"*\"}]}";

    private final AwsClients clients;

    public SecretsManagerGroup(AwsClients clients) {
        this.clients = clients;
    }

    private SecretsManagerClient sm() { return clients.secretsManager(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("secretsmanager-crud:CreateSecret",                this::createSecret),
                Map.entry("secretsmanager-crud:GetSecretValue",              this::getSecretValue),
                Map.entry("secretsmanager-crud:DescribeSecret",              this::describeSecret),
                Map.entry("secretsmanager-crud:PutSecretValue",              this::putSecretValue),
                Map.entry("secretsmanager-crud:ListSecretVersionIds",        this::listSecretVersionIds),
                Map.entry("secretsmanager-crud:UpdateSecret",                this::updateSecret),
                Map.entry("secretsmanager-crud:TagResource",                 this::tagResource),
                Map.entry("secretsmanager-crud:UntagResource",               this::untagResource),
                Map.entry("secretsmanager-crud:GetRandomPassword",           this::getRandomPassword),
                Map.entry("secretsmanager-crud:BatchGetSecretValue",         this::batchGetSecretValue),
                Map.entry("secretsmanager-crud:ListSecrets",                 this::listSecrets),
                Map.entry("secretsmanager-crud:DeleteSecret",                this::deleteSecret),
                Map.entry("secretsmanager-rotate:RotateSecretWithoutLambda", this::rotateSecretWithoutLambda),
                Map.entry("secretsmanager-rotate:RotateSecret",              this::rotateSecret),
                Map.entry("secretsmanager-rotate:PutSecretValuePending",     this::putSecretValuePending),
                Map.entry("secretsmanager-rotate:GetSecretValueByStage",     this::getSecretValueByStage),
                Map.entry("secretsmanager-rotate:UpdateSecretVersionStage",  this::updateSecretVersionStage),
                Map.entry("secretsmanager-rotate:CancelRotateSecret",        this::cancelRotateSecret),
                Map.entry("secretsmanager-policy:ValidateResourcePolicy",    this::validateResourcePolicy),
                Map.entry("secretsmanager-policy:PutResourcePolicy",         this::putResourcePolicy),
                Map.entry("secretsmanager-policy:GetResourcePolicy",         this::getResourcePolicy),
                Map.entry("secretsmanager-policy:DeleteResourcePolicy",      this::deleteResourcePolicy)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("secretsmanager-crud",   this::setupCrud),
                Map.entry("secretsmanager-rotate", this::setupRotate),
                Map.entry("secretsmanager-policy", this::setupPolicy)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("secretsmanager-crud",   ctx -> deleteSecretSilently(ctx.getString("smSecretName"))),
                Map.entry("secretsmanager-rotate", ctx -> deleteSecretSilently(ctx.getString("smRotateSecretName"))),
                Map.entry("secretsmanager-policy", ctx -> deleteSecretSilently(ctx.getString("smPolicySecretName")))
        );
    }

    // ── secretsmanager-crud ───────────────────────────────────────────────────

    private void setupCrud(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-smcrud";
        sm().createSecret(r -> r.name(name).secretString("{\"user\":\"admin\",\"pass\":\"secret\"}"));
        ctx.set("smSecretName", name);
    }

    private void createSecret(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("smSecretName"), "smSecretName");
    }

    private void getSecretValue(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        var resp = sm().getSecretValue(r -> r.secretId(name));
        Assertions.assertNotBlank(resp.secretString(), "GetSecretValue: secretString");
        Assertions.assertContains(resp.secretString(), "admin", "GetSecretValue: secretString should contain 'admin'");
    }

    private void describeSecret(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        var resp = sm().describeSecret(r -> r.secretId(name));
        Assertions.assertEquals(name, resp.name(), "DescribeSecret: name mismatch");
    }

    private void putSecretValue(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        var resp = sm().putSecretValue(r -> r.secretId(name)
                .secretString("{\"user\":\"admin\",\"pass\":\"newpassword\"}"));
        Assertions.assertNotBlank(resp.versionId(), "PutSecretValue: versionId");
        ctx.set("smVersionId", resp.versionId());
    }

    private void listSecretVersionIds(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        var resp = sm().listSecretVersionIds(r -> r.secretId(name));
        Assertions.assertNotEmpty(resp.versions(), "ListSecretVersionIds: no versions");
    }

    private void updateSecret(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        sm().updateSecret(r -> r.secretId(name).description("updated by compat test"));
        var resp = sm().describeSecret(r -> r.secretId(name));
        Assertions.assertEquals("updated by compat test", resp.description(), "UpdateSecret: description mismatch");
    }

    private void tagResource(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        sm().tagResource(r -> r.secretId(name).tags(
                software.amazon.awssdk.services.secretsmanager.model.Tag.builder()
                        .key("env").value("test").build()));
    }

    private void untagResource(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        sm().untagResource(r -> r.secretId(name).tagKeys("env"));
    }

    private void getRandomPassword(TestContext ctx) throws Exception {
        var resp = sm().getRandomPassword(r -> r.passwordLength(24L));
        Assertions.assertNotBlank(resp.randomPassword(), "GetRandomPassword: randomPassword");
        Assertions.assertEquals(24, resp.randomPassword().length(), "GetRandomPassword: length mismatch");
    }

    private void batchGetSecretValue(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        // Create a second secret for the batch request.
        String name2 = ctx.runId() + "-smbatch";
        try {
            sm().createSecret(r -> r.name(name2).secretString("batch-value"));
            var resp = sm().batchGetSecretValue(r -> r.secretIdList(name, name2));
            Assertions.assertGreaterThanOrEqual(1, resp.secretValues().size(),
                    "BatchGetSecretValue: expected at least 1 result");
        } finally {
            deleteSecretSilently(name2);
        }
    }

    private void listSecrets(TestContext ctx) throws Exception {
        String name = ctx.getString("smSecretName");
        var resp = sm().listSecrets();
        boolean found = resp.secretList().stream().anyMatch(s -> s.name().equals(name));
        Assertions.assertTrue(found, "ListSecrets: secret " + name + " not found");
    }

    private void deleteSecret(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-smdel";
        sm().createSecret(r -> r.name(name).secretString("to-be-deleted"));
        sm().deleteSecret(r -> r.secretId(name).forceDeleteWithoutRecovery(true));
    }

    // ── secretsmanager-rotate ─────────────────────────────────────────────────

    private void setupRotate(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-smrot";
        sm().createSecret(r -> r.name(name).secretString("rotate-me"));
        ctx.set("smRotateSecretName", name);
    }

    /**
     * A placeholder rotation function. This group always sets
     * {@code rotateImmediately(false)}, so it is never invoked: driving the
     * four-step protocol needs a real rotation Lambda and belongs with the
     * Docker-dependent Lambda groups.
     */
    private String rotationLambdaArn(TestContext ctx) {
        return "arn:aws:lambda:" + ctx.region() + ":000000000000:function:oc-rotate-fn";
    }

    private void rotateSecretWithoutLambda(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        try {
            sm().rotateSecret(r -> r.secretId(name)
                    .rotationRules(rr -> rr.automaticallyAfterDays(30L)));
        } catch (SecretsManagerException e) {
            String code = e.awsErrorDetails() == null ? "" : e.awsErrorDetails().errorCode();
            Assertions.assertEquals("InvalidRequestException", code,
                    "RotateSecretWithoutLambda: unexpected error code");
            return;
        }
        throw new AssertionError(
                "RotateSecretWithoutLambda: expected InvalidRequestException, call succeeded");
    }

    private void rotateSecret(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        String arn = rotationLambdaArn(ctx);
        sm().rotateSecret(r -> r.secretId(name)
                .rotationLambdaARN(arn)
                .rotationRules(rr -> rr.automaticallyAfterDays(30L))
                .rotateImmediately(false));

        var desc = sm().describeSecret(r -> r.secretId(name));
        Assertions.assertTrue(Boolean.TRUE.equals(desc.rotationEnabled()),
                "RotateSecret: RotationEnabled not set");
        Assertions.assertEquals(arn, desc.rotationLambdaARN(), "RotateSecret: RotationLambdaARN mismatch");
        Assertions.assertTrue(desc.nextRotationDate() != null, "RotateSecret: NextRotationDate not set");
    }

    private void putSecretValuePending(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        var resp = sm().putSecretValue(r -> r.secretId(name)
                .secretString("pending-value")
                .clientRequestToken(PENDING_TOKEN)
                .versionStages("AWSPENDING"));
        Assertions.assertEquals(PENDING_TOKEN, resp.versionId(),
                "PutSecretValuePending: VersionId should be the ClientRequestToken");
        Assertions.assertTrue(resp.versionStages().contains("AWSPENDING"),
                "PutSecretValuePending: AWSPENDING missing from VersionStages");

        // Staging a pending version must not change AWSCURRENT.
        var current = sm().getSecretValue(r -> r.secretId(name));
        Assertions.assertEquals("rotate-me", current.secretString(),
                "PutSecretValuePending: AWSCURRENT changed");
    }

    private void getSecretValueByStage(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        var resp = sm().getSecretValue(r -> r.secretId(name).versionStage("AWSPENDING"));
        Assertions.assertEquals("pending-value", resp.secretString(),
                "GetSecretValueByStage: wrong value for the AWSPENDING stage");
    }

    private void updateSecretVersionStage(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        var desc = sm().describeSecret(r -> r.secretId(name));
        String current = desc.versionIdsToStages().entrySet().stream()
                .filter(e -> e.getValue().contains("AWSCURRENT"))
                .map(Map.Entry::getKey)
                .findFirst()
                .orElse(null);
        Assertions.assertNotBlank(current, "UpdateSecretVersionStage: no AWSCURRENT version");

        // This is what a rotation function's finishSecret step does.
        sm().updateSecretVersionStage(r -> r.secretId(name)
                .versionStage("AWSCURRENT")
                .moveToVersionId(PENDING_TOKEN)
                .removeFromVersionId(current));

        var after = sm().getSecretValue(r -> r.secretId(name));
        Assertions.assertEquals("pending-value", after.secretString(),
                "UpdateSecretVersionStage: AWSCURRENT did not move");
        Assertions.assertEquals(PENDING_TOKEN, after.versionId(),
                "UpdateSecretVersionStage: AWSCURRENT version mismatch");

        var post = sm().describeSecret(r -> r.secretId(name));
        Assertions.assertTrue(post.versionIdsToStages().get(current).contains("AWSPREVIOUS"),
                "UpdateSecretVersionStage: displaced version did not become AWSPREVIOUS");
    }

    private void cancelRotateSecret(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        sm().cancelRotateSecret(r -> r.secretId(name));
        var desc = sm().describeSecret(r -> r.secretId(name));
        Assertions.assertTrue(!Boolean.TRUE.equals(desc.rotationEnabled()),
                "CancelRotateSecret: rotation still enabled after cancel");
        // AWS keeps the function configured so rotation can be turned back on.
        Assertions.assertEquals(rotationLambdaArn(ctx), desc.rotationLambdaARN(),
                "CancelRotateSecret: rotation function should stay configured");
    }

    // ── secretsmanager-policy ─────────────────────────────────────────────────
    //
    // Overcast stores and validates a secret's resource policy but does not
    // evaluate it, so these assert the round-trip and the validation verdict,
    // never an access decision.

    private void setupPolicy(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-smpol";
        sm().createSecret(r -> r.name(name).secretString("policy-value"));
        ctx.set("smPolicySecretName", name);
    }

    private void validateResourcePolicy(TestContext ctx) throws Exception {
        var ok = sm().validateResourcePolicy(r -> r.resourcePolicy(COMPAT_RESOURCE_POLICY));
        Assertions.assertTrue(ok.policyValidationPassed(),
                "ValidateResourcePolicy: valid policy rejected: " + ok.validationErrors());

        var bad = sm().validateResourcePolicy(r -> r.resourcePolicy("{\"Version\":\"2012-10-17\"}"));
        Assertions.assertTrue(!Boolean.TRUE.equals(bad.policyValidationPassed()),
                "ValidateResourcePolicy: policy with no Statement should not pass");
        Assertions.assertNotEmpty(bad.validationErrors(),
                "ValidateResourcePolicy: no ValidationErrors for a failing policy");
    }

    private void putResourcePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("smPolicySecretName");
        var resp = sm().putResourcePolicy(r -> r.secretId(name).resourcePolicy(COMPAT_RESOURCE_POLICY));
        Assertions.assertNotBlank(resp.arn(), "PutResourcePolicy: ARN");
        Assertions.assertEquals(name, resp.name(), "PutResourcePolicy: name mismatch");
    }

    private void getResourcePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("smPolicySecretName");
        var resp = sm().getResourcePolicy(r -> r.secretId(name));
        Assertions.assertContains(resp.resourcePolicy(), "OvercastCompatRead",
                "GetResourcePolicy: policy does not carry the Sid that was stored");
    }

    private void deleteResourcePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("smPolicySecretName");
        sm().deleteResourcePolicy(r -> r.secretId(name));
        var resp = sm().getResourcePolicy(r -> r.secretId(name));
        Assertions.assertTrue(resp.resourcePolicy() == null || resp.resourcePolicy().isEmpty(),
                "DeleteResourcePolicy: policy still present");
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteSecretSilently(String name) {
        if (name == null) return;
        try { sm().deleteSecret(r -> r.secretId(name).forceDeleteWithoutRecovery(true)); }
        catch (Exception ignored) {}
    }
}
