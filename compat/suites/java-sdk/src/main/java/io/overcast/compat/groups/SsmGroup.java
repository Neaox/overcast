package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.ssm.SsmClient;
import software.amazon.awssdk.services.ssm.model.*;

import java.util.List;
import java.util.Map;

/**
 * SSM Parameter Store compatibility test group.
 *
 * <p>Groups: ssm-parameters, ssm-secure, ssm-path.
 */
public final class SsmGroup implements ServiceGroup {

    private final AwsClients clients;

    public SsmGroup(AwsClients clients) {
        this.clients = clients;
    }

    private SsmClient ssm() { return clients.ssm(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("PutParameter",                    this::putParameter),
                Map.entry("GetParameter",                    this::getParameter),
                Map.entry("PutParameterOverwrite",           this::putParameterOverwrite),
                Map.entry("GetParameterHistory",             this::getParameterHistory),
                Map.entry("PutMultipleParameters",           this::putMultipleParameters),
                Map.entry("GetParameters",                   this::getParameters),
                Map.entry("DescribeParameters",              this::describeParameters),
                Map.entry("TagParameter",                    this::tagParameter),
                Map.entry("ListSSMTagsForResource",          this::listSsmTagsForResource),
                Map.entry("DeleteParameters",                this::deleteParameters),
                Map.entry("PutSecureStringParameter",        this::putSecureStringParameter),
                Map.entry("GetSecureStringParameter",        this::getSecureStringParameter),
                Map.entry("GetSecureStringWithoutDecryption",this::getSecureStringWithoutDecryption),
                Map.entry("GetParametersByPath",             this::getParametersByPath),
                Map.entry("GetParametersByPathRecursive",    this::getParametersByPathRecursive),
                Map.entry("GetParametersByPathPaginated",    this::getParametersByPathPaginated)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("ssm-parameters", this::setupParameters),
                Map.entry("ssm-secure",     this::setupSecure),
                Map.entry("ssm-path",       this::setupPath)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("ssm-parameters", this::teardownParameters),
                Map.entry("ssm-secure",     ctx -> deleteParamSilently(ctx.getString("ssmSecureParam"))),
                Map.entry("ssm-path",       this::teardownPath)
        );
    }

    // ── ssm-parameters ────────────────────────────────────────────────────────

    private void setupParameters(TestContext ctx) throws Exception {
        String name  = "/" + ctx.runId() + "/param1";
        String name2 = "/" + ctx.runId() + "/param2";
        ssm().putParameter(r -> r.name(name).value("value1").type(ParameterType.STRING));
        ssm().putParameter(r -> r.name(name2).value("value2").type(ParameterType.STRING));
        ctx.set("ssmParam1", name);
        ctx.set("ssmParam2", name2);
    }

    private void teardownParameters(TestContext ctx) {
        deleteParamSilently(ctx.getString("ssmParam1"));
        deleteParamSilently(ctx.getString("ssmParam2"));
    }

    private void putParameter(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("ssmParam1"), "ssmParam1");
    }

    private void getParameter(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmParam1");
        var resp = ssm().getParameter(r -> r.name(name));
        Assertions.assertEquals("value1", resp.parameter().value(), "GetParameter: value mismatch");
    }

    private void putParameterOverwrite(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmParam1");
        ssm().putParameter(r -> r.name(name).value("updated1").type(ParameterType.STRING).overwrite(true));
        var resp = ssm().getParameter(r -> r.name(name));
        Assertions.assertEquals("updated1", resp.parameter().value(), "PutParameterOverwrite: value not updated");
    }

    private void getParameterHistory(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmParam1");
        var resp = ssm().getParameterHistory(r -> r.name(name));
        Assertions.assertNotEmpty(resp.parameters(), "GetParameterHistory: no history entries");
    }

    private void putMultipleParameters(TestContext ctx) throws Exception {
        // Verify both params from setup are accessible.
        String n1 = ctx.getString("ssmParam1");
        String n2 = ctx.getString("ssmParam2");
        var resp = ssm().getParameters(r -> r.names(n1, n2));
        Assertions.assertEquals(2, resp.parameters().size(), "PutMultipleParameters: expected 2 params");
    }

    private void getParameters(TestContext ctx) throws Exception {
        String n1 = ctx.getString("ssmParam1");
        String n2 = ctx.getString("ssmParam2");
        var resp = ssm().getParameters(r -> r.names(n1, n2));
        Assertions.assertEquals(2, resp.parameters().size(), "GetParameters: expected 2 parameters");
    }

    private void describeParameters(TestContext ctx) throws Exception {
        var resp = ssm().describeParameters(r -> r.maxResults(10));
        Assertions.assertNotNull(resp.parameters(), "DescribeParameters: parameters is null");
    }

    private void tagParameter(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmParam1");
        ssm().addTagsToResource(r -> r
                .resourceType(ResourceTypeForTagging.PARAMETER)
                .resourceId(name)
                .tags(software.amazon.awssdk.services.ssm.model.Tag.builder()
                        .key("env").value("test").build()));
    }

    private void listSsmTagsForResource(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmParam1");
        var resp = ssm().listTagsForResource(r -> r
                .resourceType(ResourceTypeForTagging.PARAMETER)
                .resourceId(name));
        Assertions.assertNotNull(resp.tagList(), "ListSSMTagsForResource: tagList is null");
    }

    private void deleteParameters(TestContext ctx) throws Exception {
        String n1 = ctx.getString("ssmParam1");
        String n2 = ctx.getString("ssmParam2");
        ssm().deleteParameters(r -> r.names(n1, n2));
        // Clear ctx so teardown does not attempt double-delete.
        ctx.set("ssmParam1", null);
        ctx.set("ssmParam2", null);
    }

    // ── ssm-secure ────────────────────────────────────────────────────────────

    private void setupSecure(TestContext ctx) {
        ctx.set("ssmSecureParam", "/" + ctx.runId() + "/secure");
    }

    private void putSecureStringParameter(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmSecureParam");
        ssm().putParameter(r -> r.name(name).value("s3cr3t").type(ParameterType.SECURE_STRING));
    }

    private void getSecureStringParameter(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmSecureParam");
        var resp = ssm().getParameter(r -> r.name(name).withDecryption(true));
        Assertions.assertEquals("s3cr3t", resp.parameter().value(), "GetSecureStringParameter: value mismatch");
    }

    private void getSecureStringWithoutDecryption(TestContext ctx) throws Exception {
        String name = ctx.getString("ssmSecureParam");
        var resp = ssm().getParameter(r -> r.name(name).withDecryption(false));
        Assertions.assertNotNull(resp.parameter().value(), "GetSecureStringWithoutDecryption: value is null");
        // The value should be the ciphertext placeholder, not the plaintext.
        Assertions.assertNotEquals("s3cr3t", resp.parameter().value(),
                "GetSecureStringWithoutDecryption: expected encrypted/redacted value");
    }

    // ── ssm-path ──────────────────────────────────────────────────────────────

    private void setupPath(TestContext ctx) throws Exception {
        String prefix = "/" + ctx.runId() + "/app";
        ssm().putParameter(r -> r.name(prefix + "/db-url").value("jdbc://localhost").type(ParameterType.STRING));
        ssm().putParameter(r -> r.name(prefix + "/db-pass").value("secret").type(ParameterType.STRING));
        ssm().putParameter(r -> r.name(prefix + "/nested/key").value("nested-value").type(ParameterType.STRING));
        ctx.set("ssmPathPrefix", prefix);
    }

    private void teardownPath(TestContext ctx) {
        String prefix = ctx.getString("ssmPathPrefix");
        if (prefix == null) return;
        deleteParamSilently(prefix + "/db-url");
        deleteParamSilently(prefix + "/db-pass");
        deleteParamSilently(prefix + "/nested/key");
    }

    private void getParametersByPath(TestContext ctx) throws Exception {
        String prefix = ctx.getString("ssmPathPrefix");
        var resp = ssm().getParametersByPath(r -> r.path(prefix + "/").recursive(false));
        Assertions.assertGreaterThanOrEqual(2, resp.parameters().size(),
                "GetParametersByPath: expected >= 2 parameters at " + prefix + "/");
    }

    private void getParametersByPathRecursive(TestContext ctx) throws Exception {
        String prefix = ctx.getString("ssmPathPrefix");
        var resp = ssm().getParametersByPath(r -> r.path(prefix + "/").recursive(true));
        Assertions.assertGreaterThanOrEqual(3, resp.parameters().size(),
                "GetParametersByPathRecursive: expected >= 3 parameters (incl. nested)");
    }

    private void getParametersByPathPaginated(TestContext ctx) throws Exception {
        String prefix = ctx.getString("ssmPathPrefix");
        var page1 = ssm().getParametersByPath(r -> r.path(prefix + "/").recursive(true).maxResults(1));
        Assertions.assertGreaterThanOrEqual(1, page1.parameters().size(),
                "GetParametersByPathPaginated: page 1 should have at least 1 parameter");
        if (page1.nextToken() != null) {
            var page2 = ssm().getParametersByPath(r -> r.path(prefix + "/")
                    .recursive(true).maxResults(1).nextToken(page1.nextToken()));
            Assertions.assertGreaterThanOrEqual(1, page2.parameters().size(),
                    "GetParametersByPathPaginated: page 2 should have at least 1 parameter");
        }
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteParamSilently(String name) {
        if (name == null) return;
        try { ssm().deleteParameter(r -> r.name(name)); } catch (Exception ignored) {}
    }
}
