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
 * <p>Groups: secretsmanager-crud, secretsmanager-rotate.
 */
public final class SecretsManagerGroup implements ServiceGroup {

    private final AwsClients clients;

    public SecretsManagerGroup(AwsClients clients) {
        this.clients = clients;
    }

    private SecretsManagerClient sm() { return clients.secretsManager(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateSecret",        this::createSecret),
                Map.entry("GetSecretValue",      this::getSecretValue),
                Map.entry("DescribeSecret",      this::describeSecret),
                Map.entry("PutSecretValue",      this::putSecretValue),
                Map.entry("ListSecretVersionIds",this::listSecretVersionIds),
                Map.entry("UpdateSecret",        this::updateSecret),
                Map.entry("secretsmanager-crud/TagResource",   this::tagResource),
                Map.entry("secretsmanager-crud/UntagResource", this::untagResource),
                Map.entry("GetRandomPassword",   this::getRandomPassword),
                Map.entry("BatchGetSecretValue", this::batchGetSecretValue),
                Map.entry("ListSecrets",         this::listSecrets),
                Map.entry("DeleteSecret",        this::deleteSecret),
                Map.entry("RotateSecret",        this::rotateSecret),
                Map.entry("CancelRotateSecret",  this::cancelRotateSecret)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("secretsmanager-crud",   this::setupCrud),
                Map.entry("secretsmanager-rotate", this::setupRotate)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("secretsmanager-crud",   ctx -> deleteSecretSilently(ctx.getString("smSecretName"))),
                Map.entry("secretsmanager-rotate", ctx -> deleteSecretSilently(ctx.getString("smRotateSecretName")))
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

    private void rotateSecret(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        // Overcast may not implement rotation; the test still verifies the API call
        // is accepted (or returns a recognisable 501).
        sm().rotateSecret(r -> r.secretId(name)
                .rotationRules(rr -> rr.automaticallyAfterDays(30L)));
    }

    private void cancelRotateSecret(TestContext ctx) throws Exception {
        String name = ctx.getString("smRotateSecretName");
        sm().cancelRotateSecret(r -> r.secretId(name));
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteSecretSilently(String name) {
        if (name == null) return;
        try { sm().deleteSecret(r -> r.secretId(name).forceDeleteWithoutRecovery(true)); }
        catch (Exception ignored) {}
    }
}
