package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.core.SdkBytes;
import software.amazon.awssdk.services.lambda.LambdaAsyncClient;
import software.amazon.awssdk.services.lambda.LambdaClient;
import software.amazon.awssdk.services.lambda.model.*;
import software.amazon.awssdk.services.lambda.model.Runtime;
import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.QueueAttributeName;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

/**
 * Lambda compatibility test group.
 *
 * <p>Groups: lambda-crud, lambda-invoke (docker-gated), lambda-aliases,
 * lambda-layers.
 */
public final class LambdaGroup implements ServiceGroup {

    private final AwsClients clients;

    public LambdaGroup(AwsClients clients) {
        this.clients = clients;
    }

    private LambdaClient lambda() { return clients.lambda(); }
    private SqsClient sqs() { return clients.sqs(); }
    private LambdaAsyncClient lambdaAsync() { return clients.lambdaAsync(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("lambda-crud:CreateFunction",             this::createFunction),
                Map.entry("lambda-crud:GetFunction",                this::getFunction),
                Map.entry("lambda-crud:ListFunctions",              this::listFunctions),
                Map.entry("UpdateFunctionCode",         this::updateFunctionCode),
                Map.entry("UpdateFunctionConfiguration",this::updateFunctionConfiguration),
                Map.entry("lambda-crud:DeleteFunction",             this::deleteFunction),
                Map.entry("lambda-policy:AddPermission",             this::addPermission),
                Map.entry("lambda-policy:GetPolicy",                 this::getPolicy),
                Map.entry("lambda-policy:RemovePermission",          this::removePermission),
                Map.entry("InvokeDryRun",               this::invokeDryRun),
                Map.entry("InvokeSync",                 this::invokeSync),
                Map.entry("InvokeAsync",                this::invokeAsync),
                Map.entry("InvokeWithResponseStream", this::invokeWithResponseStream),
                Map.entry("PublishVersion",             this::publishVersion),
                Map.entry("ListVersionsByFunction",     this::listVersionsByFunction),
                Map.entry("CreateAlias",                this::createAlias),
                Map.entry("GetAlias",                   this::getAlias),
                Map.entry("ListAliases",                this::listAliases),
                Map.entry("UpdateAlias",                this::updateAlias),
                Map.entry("DeleteAlias",                this::deleteAlias),
                Map.entry("PublishLayerVersion",        this::publishLayerVersion),
                Map.entry("ListLayers",                 this::listLayers),
                Map.entry("DeleteLayerVersion",         this::deleteLayerVersion)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("lambda-crud",    this::setupCrud),
                Map.entry("lambda-policy",  this::setupPolicy),
                Map.entry("lambda-invoke",  this::setupInvoke),
                Map.entry("lambda-aliases", this::setupAliases),
                Map.entry("lambda-invoke-stream", this::setupInvokeStream),
                Map.entry("lambda-layers",         this::setupLayers)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("lambda-crud",    ctx -> deleteFunctionSilently(ctx.getString("lambdaFuncName"))),
                Map.entry("lambda-policy",  ctx -> deleteFunctionSilently(ctx.getString("lambdaPolicyName"))),
                Map.entry("lambda-invoke",  ctx -> deleteFunctionSilently(ctx.getString("lambdaInvokeName"))),
                Map.entry("lambda-aliases", ctx -> deleteFunctionSilently(ctx.getString("lambdaAliasFuncName"))),
                Map.entry("lambda-invoke-stream", ctx -> deleteFunctionSilently(ctx.getString("lambdaStreamName"))),
                Map.entry("lambda-layers",         ctx -> deleteLayerVersionSilently(ctx.getString("lambdaLayerName"), ctx.<Long>get("lambdaLayerVersion")))
        );
    }

    // ── lambda-crud ───────────────────────────────────────────────────────────

    private void setupCrud(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-lamcrud";
        lambda().createFunction(r -> r
                .functionName(name)
                .runtime(Runtime.NODEJS20_X)
                .role("arn:aws:iam::000000000000:role/lambda-role")
                .handler("index.handler")
                .code(fc -> fc.zipFile(minimalNodeZip())));
        ctx.set("lambdaFuncName", name);
    }

    private void createFunction(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("lambdaFuncName"), "lambdaFuncName");
    }

    private void getFunction(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaFuncName");
        var resp = lambda().getFunction(r -> r.functionName(name));
        Assertions.assertNotNull(resp.configuration(), "GetFunction: configuration is null");
        Assertions.assertEquals(name, resp.configuration().functionName(), "GetFunction: name mismatch");
    }

    private void listFunctions(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaFuncName");
        var resp = lambda().listFunctions();
        boolean found = resp.functions().stream().anyMatch(f -> f.functionName().equals(name));
        Assertions.assertTrue(found, "ListFunctions: function " + name + " not found");
    }

    private void updateFunctionCode(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaFuncName");
        lambda().updateFunctionCode(r -> r.functionName(name).zipFile(minimalNodeZip()));
    }

    private void updateFunctionConfiguration(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaFuncName");
        // A real queue, so DeadLetterConfig names a target that exists rather
        // than a plausible-looking string.
        String queue = "oc-" + ctx.runId() + "-lamcrud-dlq";
        String queueUrl = sqs().createQueue(r -> r.queueName(queue)).queueUrl();
        try {
            String dlqArn = sqs().getQueueAttributes(r -> r.queueUrl(queueUrl)
                    .attributeNames(QueueAttributeName.QUEUE_ARN))
                    .attributes().get(QueueAttributeName.QUEUE_ARN);

            // DeadLetterConfig rides along because it is the member that used to
            // answer 501 here, which failed every `cdk deploy` of a function
            // with a DLQ. Both the response and a later read are checked: an
            // update that answers 200 and drops the property is the same bug
            // wearing a better status code.
            var updated = lambda().updateFunctionConfiguration(r -> r.functionName(name)
                    .timeout(30)
                    .memorySize(256)
                    .deadLetterConfig(d -> d.targetArn(dlqArn)));
            Assertions.assertNotNull(updated.deadLetterConfig(),
                    "UpdateFunctionConfiguration: response carries no DeadLetterConfig");
            Assertions.assertEquals(dlqArn, updated.deadLetterConfig().targetArn(),
                    "UpdateFunctionConfiguration: DeadLetterConfig.TargetArn mismatch");

            var resp = lambda().getFunction(r -> r.functionName(name));
            Assertions.assertEquals(30, resp.configuration().timeout(), "UpdateFunctionConfiguration: timeout mismatch");
            Assertions.assertNotNull(resp.configuration().deadLetterConfig(),
                    "UpdateFunctionConfiguration: the stored configuration carries no DeadLetterConfig");
            Assertions.assertEquals(dlqArn, resp.configuration().deadLetterConfig().targetArn(),
                    "UpdateFunctionConfiguration: stored DeadLetterConfig.TargetArn mismatch");
        } finally {
            try {
                sqs().deleteQueue(r -> r.queueUrl(queueUrl));
            } catch (RuntimeException ignored) {
                // Teardown is best effort.
            }
        }
    }

    private void deleteFunction(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-lamdel";
        lambda().createFunction(r -> r
                .functionName(name)
                .runtime(Runtime.NODEJS20_X)
                .role("arn:aws:iam::000000000000:role/lambda-role")
                .handler("index.handler")
                .code(fc -> fc.zipFile(minimalNodeZip())));
        lambda().deleteFunction(r -> r.functionName(name));
    }

    // ── lambda-policy ──────────────────────────────────────────────────────

    private void setupPolicy(TestContext ctx) {
        String name = ctx.runId() + "-fn-policy";
        lambda().createFunction(r -> r
                .functionName(name)
                .runtime(Runtime.NODEJS20_X)
                .role("arn:aws:iam::000000000000:role/lambda-role")
                .handler("index.handler")
                .code(fc -> fc.zipFile(minimalNodeZip())));
        ctx.set("lambdaPolicyName", name);
    }

    private void addPermission(TestContext ctx) {
        String name = ctx.getString("lambdaPolicyName");
        var response = lambda().addPermission(r -> r
                .functionName(name)
                .statementId("allow-s3")
                .action("lambda:InvokeFunction")
                .principal("s3.amazonaws.com")
                .sourceAccount("000000000000"));
        Assertions.assertTrue(response.statement().contains("\"Sid\":\"allow-s3\""),
                "AddPermission: statement missing allow-s3 SID");
        var policy = lambda().getPolicy(r -> r.functionName(name));
        Assertions.assertTrue(policy.policy().contains("\"Sid\":\"allow-s3\""),
                "AddPermission: statement missing from GetPolicy");
    }

    private void getPolicy(TestContext ctx) {
        var response = lambda().getPolicy(r -> r.functionName(ctx.getString("lambdaPolicyName")));
        Assertions.assertTrue(response.policy().contains("\"Sid\":\"allow-s3\""),
                "GetPolicy: allow-s3 statement missing");
        Assertions.assertNotBlank(response.revisionId(), "GetPolicy: RevisionId");
    }

    private void removePermission(TestContext ctx) {
        String name = ctx.getString("lambdaPolicyName");
        lambda().removePermission(r -> r.functionName(name).statementId("allow-s3"));
        try {
            lambda().getPolicy(r -> r.functionName(name));
            throw new AssertionError("RemovePermission: policy still exists");
        } catch (ResourceNotFoundException expected) {
            // Expected after the final statement is removed.
        }
    }

    // ── lambda-invoke ─────────────────────────────────────────────────────────

    private void setupInvoke(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-laminv";
        lambda().createFunction(r -> r
                .functionName(name)
                .runtime(Runtime.NODEJS20_X)
                .role("arn:aws:iam::000000000000:role/lambda-role")
                .handler("index.handler")
                .code(fc -> fc.zipFile(minimalNodeZip())));
        ctx.set("lambdaInvokeName", name);
    }

    private void invokeDryRun(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaInvokeName");
        // Runs first in the group, so nothing has waited for the function yet.
        // A create is not instantly invocable — the emulator answers a Pending
        // invoke with AWS's 409 ResourceConflictException — and a cold runtime
        // image pull widens that window to seconds. Same fix as cli in #442.
        awaitFunctionActive(name);
        var resp = lambda().invoke(r -> r.functionName(name).invocationType(InvocationType.DRY_RUN));
        Assertions.assertEquals(204, resp.statusCode(), "InvokeDryRun: expected 204 status");
    }

    /**
     * Waits for a function to leave Pending. AWS's own function-active-v2
     * waiter allows five minutes; 60 x 500ms is enough to sit out a cold
     * runtime-image pull without hanging a suite if the function never starts.
     */
    private void awaitFunctionActive(String name) throws Exception {
        for (int i = 0; i < 60; i++) {
            var cfg = lambda().getFunction(r -> r.functionName(name)).configuration();
            String state = cfg.stateAsString();
            if (state == null || "Active".equals(state)) {
                return;
            }
            if ("Failed".equals(state)) {
                throw new AssertionError("function " + name + " failed to start: " + cfg.stateReason());
            }
            Thread.sleep(500);
        }
        throw new AssertionError("function " + name + " did not become Active in time");
    }

    private void invokeSync(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaInvokeName");
        var resp = lambda().invoke(r -> r.functionName(name)
                .invocationType(InvocationType.REQUEST_RESPONSE)
                .payload(SdkBytes.fromUtf8String("{}")));
        Assertions.assertGreaterThanOrEqual(200, resp.statusCode(), "InvokeSync: statusCode");
    }

    private void invokeAsync(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaInvokeName");
        var resp = lambda().invoke(r -> r.functionName(name)
                .invocationType(InvocationType.EVENT)
                .payload(SdkBytes.fromUtf8String("{}")));
        Assertions.assertEquals(202, resp.statusCode(), "InvokeAsync: expected 202 status");
    }

    // ── lambda-invoke-stream ───────────────────────────────────────────────────

    private void setupInvokeStream(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-lamstream";
        lambda().createFunction(r -> r
                .functionName(name)
                .runtime(Runtime.NODEJS20_X)
                .role("arn:aws:iam::000000000000:role/lambda-role")
                .handler("index.handler")
                .code(fc -> fc.zipFile(minimalNodeZip())));
        ctx.set("lambdaStreamName", name);
    }

    private void invokeWithResponseStream(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaStreamName");
        InvokeWithResponseStreamResponseHandler handler = InvokeWithResponseStreamResponseHandler.builder()
                .subscriber(event -> {})
                .build();
        lambdaAsync().invokeWithResponseStream(
                InvokeWithResponseStreamRequest.builder()
                        .functionName(name)
                        .payload(SdkBytes.fromUtf8String("{}"))
                        .build(),
                handler).join();
    }

    // ── lambda-aliases ────────────────────────────────────────────────────────

    private void setupAliases(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-lamalias";
        lambda().createFunction(r -> r
                .functionName(name)
                .runtime(Runtime.NODEJS20_X)
                .role("arn:aws:iam::000000000000:role/lambda-role")
                .handler("index.handler")
                .code(fc -> fc.zipFile(minimalNodeZip())));
        ctx.set("lambdaAliasFuncName", name);
    }

    private void publishVersion(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaAliasFuncName");
        var resp = lambda().publishVersion(r -> r.functionName(name));
        Assertions.assertNotBlank(resp.version(), "PublishVersion: version");
        ctx.set("lambdaVersion", resp.version());
    }

    private void listVersionsByFunction(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaAliasFuncName");
        var resp = lambda().listVersionsByFunction(r -> r.functionName(name));
        Assertions.assertNotEmpty(resp.versions(), "ListVersionsByFunction: no versions");
    }

    private void createAlias(TestContext ctx) throws Exception {
        String name    = ctx.getString("lambdaAliasFuncName");
        String version = ctx.getString("lambdaVersion");
        lambda().createAlias(r -> r.functionName(name).name("live").functionVersion(version));
    }

    private void getAlias(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaAliasFuncName");
        var resp = lambda().getAlias(r -> r.functionName(name).name("live"));
        Assertions.assertEquals("live", resp.name(), "GetAlias: name mismatch");
    }

    private void listAliases(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaAliasFuncName");
        var resp = lambda().listAliases(r -> r.functionName(name));
        boolean found = resp.aliases().stream().anyMatch(a -> a.name().equals("live"));
        Assertions.assertTrue(found, "ListAliases: 'live' alias not found");
    }

    private void updateAlias(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaAliasFuncName");
        lambda().updateAlias(r -> r.functionName(name).name("live").description("updated"));
        var resp = lambda().getAlias(r -> r.functionName(name).name("live"));
        Assertions.assertEquals("updated", resp.description(), "UpdateAlias: description mismatch");
    }

    private void deleteAlias(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaAliasFuncName");
        lambda().deleteAlias(r -> r.functionName(name).name("live"));
    }

    // ── lambda-layers ─────────────────────────────────────────────────────────

    private void setupLayers(TestContext ctx) {
        ctx.set("lambdaLayerName", ctx.runId() + "-lamlayer");
    }

    private void publishLayerVersion(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaLayerName");
        var resp = lambda().publishLayerVersion(r -> r
                .layerName(name)
                .description("test layer")
                .compatibleRuntimes(Runtime.NODEJS20_X)
                .content(lc -> lc.zipFile(minimalNodeZip())));
        ctx.set("lambdaLayerVersion", resp.version());
    }

    private void listLayers(TestContext ctx) throws Exception {
        String name = ctx.getString("lambdaLayerName");
        var resp = lambda().listLayers(r -> r.maxItems(50));
        boolean found = resp.layers().stream().anyMatch(l -> name.equals(l.layerName()));
        Assertions.assertTrue(found, "ListLayers: published layer " + name + " not found in list");
    }

    private void deleteLayerVersion(TestContext ctx) throws Exception {
        String name    = ctx.getString("lambdaLayerName");
        Long   version = ctx.get("lambdaLayerVersion");
        if (name == null || version == null) return;
        lambda().deleteLayerVersion(r -> r.layerName(name).versionNumber(version));
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    /**
     * Returns a minimal Node.js Lambda ZIP that exports a handler returning
     * an empty object. Under the local emulator this is sufficient for
     * dry-run invocations; live invocations require Docker to be available.
     */
    static SdkBytes minimalNodeZip() {
        try {
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            try (ZipOutputStream zip = new ZipOutputStream(buf)) {
                ZipEntry entry = new ZipEntry("index.js");
                zip.putNextEntry(entry);
                byte[] js = "exports.handler = async () => ({});".getBytes(StandardCharsets.UTF_8);
                zip.write(js);
                zip.closeEntry();
            }
            return SdkBytes.fromByteArray(buf.toByteArray());
        } catch (Exception e) {
            throw new RuntimeException("failed to build minimal Lambda ZIP", e);
        }
    }

    private void deleteFunctionSilently(String name) {
        if (name == null) return;
        try { lambda().deleteFunction(r -> r.functionName(name)); } catch (Exception ignored) {}
    }

    private void deleteLayerVersionSilently(String name, Long version) {
        if (name == null || version == null) return;
        try { lambda().deleteLayerVersion(r -> r.layerName(name).versionNumber(version)); }
        catch (Exception ignored) {}
    }
}
