package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.apigateway.ApiGatewayClient;
import software.amazon.awssdk.services.apigateway.model.*;
import software.amazon.awssdk.services.apigatewayv2.ApiGatewayV2Client;
import software.amazon.awssdk.services.apigatewayv2.model.*;

import java.time.LocalDate;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.Map;

/**
 * API Gateway (REST v1 + HTTP v2) compatibility test group.
 *
 * <p>Groups: apigateway-rest (v1), apigateway-http (v2),
 * apigateway-usage-plans (throttle/quota config + GetUsage).
 */
public final class ApiGatewayGroup implements ServiceGroup {

    private final AwsClients clients;

    public ApiGatewayGroup(AwsClients clients) {
        this.clients = clients;
    }

    private ApiGatewayClient  apigw()   { return clients.apiGateway(); }
    private ApiGatewayV2Client apigwV2() { return clients.apiGatewayV2(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                // REST API (v1)
                Map.entry("CreateRestApi",    this::createRestApi),
                Map.entry("GetRestApis",      this::getRestApis),
                Map.entry("DeleteRestApi",    this::deleteRestApi),
                // HTTP API (v2)
                Map.entry("CreateApi",        this::createApi),
                Map.entry("GetApis",          this::getApis),
                Map.entry("DeleteApi",        this::deleteApi),
                // apigateway-usage-plans — group-qualified because CreateApiKey
                // and GetUsagePlan are generic enough to collide with another
                // service's group.
                Map.entry("apigateway-usage-plans/CreateUsagePlanWithLimits", this::createUsagePlanWithLimits),
                Map.entry("apigateway-usage-plans/GetUsagePlan",              this::getUsagePlan),
                Map.entry("apigateway-usage-plans/CreateApiKey",              this::createApiKey),
                Map.entry("apigateway-usage-plans/CreateUsagePlanKey",        this::createUsagePlanKey),
                Map.entry("apigateway-usage-plans/GetUsage",                  this::getUsage),
                Map.entry("apigateway-usage-plans/DeleteUsagePlan",           this::deleteUsagePlan)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("apigateway-rest", this::setupRest),
                Map.entry("apigateway-http", this::setupHttp)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("apigateway-rest", ctx -> deleteRestApiSilently(ctx.getString("restApiId"))),
                Map.entry("apigateway-http", ctx -> deleteHttpApiSilently(ctx.getString("httpApiId"))),
                Map.entry("apigateway-usage-plans", this::teardownUsagePlan)
        );
    }

    // ── apigateway-rest ───────────────────────────────────────────────────────

    private void setupRest(TestContext ctx) {
        ctx.set("restApiName", "compat-" + ctx.runId());
    }

    private void createRestApi(TestContext ctx) throws Exception {
        String name = ctx.getString("restApiName");
        var resp = apigw().createRestApi(r -> r.name(name));
        Assertions.assertNotBlank(resp.id(), "CreateRestApi: id is blank");
        ctx.set("restApiId", resp.id());
    }

    private void getRestApis(TestContext ctx) throws Exception {
        var resp = apigw().getRestApis(r -> r.limit(100));
        String name = ctx.getString("restApiName");
        boolean found = resp.items().stream().anyMatch(a -> a.name().equals(name));
        Assertions.assertTrue(found, "GetRestApis: created REST API not found");
    }

    private void deleteRestApi(TestContext ctx) throws Exception {
        String id = ctx.getString("restApiId");
        if (id == null) throw new AssertionError("DeleteRestApi: prerequisite CreateRestApi is not implemented");
        apigw().deleteRestApi(r -> r.restApiId(id));
        ctx.set("restApiId", null);
    }

    // ── apigateway-http ───────────────────────────────────────────────────────

    private void setupHttp(TestContext ctx) {
        ctx.set("httpApiName", "compat-http-" + ctx.runId());
    }

    private void createApi(TestContext ctx) throws Exception {
        String name = ctx.getString("httpApiName");
        var resp = apigwV2().createApi(r -> r
                .name(name)
                .protocolType(ProtocolType.HTTP));
        Assertions.assertNotBlank(resp.apiId(), "CreateApi: apiId is blank");
        ctx.set("httpApiId", resp.apiId());
    }

    private void getApis(TestContext ctx) throws Exception {
        var resp = apigwV2().getApis(r -> r.maxResults("100"));
        String name = ctx.getString("httpApiName");
        boolean found = resp.items().stream().anyMatch(a -> a.name().equals(name));
        Assertions.assertTrue(found, "GetApis: created HTTP API not found");
    }

    private void deleteApi(TestContext ctx) throws Exception {
        String id = ctx.getString("httpApiId");
        if (id == null) throw new AssertionError("DeleteApi: prerequisite CreateApi is not implemented");
        apigwV2().deleteApi(r -> r.apiId(id));
        ctx.set("httpApiId", null);
    }

    // ── apigateway-usage-plans ────────────────────────────────────────────────

    private void createUsagePlanWithLimits(TestContext ctx) throws Exception {
        String name = "oc-" + ctx.runId() + "-usage-plan";
        var resp = apigw().createUsagePlan(r -> r
                .name(name)
                .throttle(ThrottleSettings.builder().rateLimit(10.0).burstLimit(20).build())
                .quota(QuotaSettings.builder().limit(1000).period(QuotaPeriodType.DAY).build()));
        Assertions.assertNotBlank(resp.id(), "CreateUsagePlan: id is blank");
        Assertions.assertEquals(name, resp.name(), "CreateUsagePlan: name mismatch");
        ctx.set("usagePlanId", resp.id());
    }

    private void getUsagePlan(TestContext ctx) throws Exception {
        String planId = ctx.getString("usagePlanId");
        if (planId == null) throw new AssertionError("GetUsagePlan: prerequisite CreateUsagePlanWithLimits is not implemented");
        var resp = apigw().getUsagePlan(r -> r.usagePlanId(planId));
        Assertions.assertEquals(planId, resp.id(), "GetUsagePlan: wrong id");
        Assertions.assertTrue(resp.throttle() != null, "GetUsagePlan: throttle not round-tripped");
        Assertions.assertEquals(10.0, resp.throttle().rateLimit(), "GetUsagePlan: rateLimit mismatch");
        Assertions.assertEquals(20, resp.throttle().burstLimit(), "GetUsagePlan: burstLimit mismatch");
        Assertions.assertTrue(resp.quota() != null, "GetUsagePlan: quota not round-tripped");
        Assertions.assertEquals(1000, resp.quota().limit(), "GetUsagePlan: quota limit mismatch");
        Assertions.assertEquals(QuotaPeriodType.DAY, resp.quota().period(), "GetUsagePlan: quota period mismatch");
    }

    private void createApiKey(TestContext ctx) throws Exception {
        String name = "oc-" + ctx.runId() + "-usage-key";
        var resp = apigw().createApiKey(r -> r.name(name).enabled(true));
        Assertions.assertNotBlank(resp.id(), "CreateApiKey: id is blank");
        Assertions.assertEquals(name, resp.name(), "CreateApiKey: name mismatch");
        ctx.set("usageKeyId", resp.id());
    }

    private void createUsagePlanKey(TestContext ctx) throws Exception {
        String planId = ctx.getString("usagePlanId");
        String keyId = ctx.getString("usageKeyId");
        if (planId == null || keyId == null) {
            throw new AssertionError("CreateUsagePlanKey: prerequisites are not implemented");
        }
        apigw().createUsagePlanKey(r -> r.usagePlanId(planId).keyId(keyId).keyType("API_KEY"));
        var listed = apigw().getUsagePlanKeys(r -> r.usagePlanId(planId));
        boolean found = listed.items().stream().anyMatch(k -> keyId.equals(k.id()));
        Assertions.assertTrue(found, "GetUsagePlanKeys: attached key not listed");
    }

    private void getUsage(TestContext ctx) throws Exception {
        String planId = ctx.getString("usagePlanId");
        String keyId = ctx.getString("usageKeyId");
        if (planId == null || keyId == null) {
            throw new AssertionError("GetUsage: prerequisites are not implemented");
        }
        String today = LocalDate.now(ZoneOffset.UTC).format(DateTimeFormatter.ISO_LOCAL_DATE);
        var resp = apigw().getUsage(r -> r.usagePlanId(planId).startDate(today).endDate(today));
        Assertions.assertEquals(planId, resp.usagePlanId(), "GetUsage: wrong usagePlanId");
        Assertions.assertEquals(today, resp.startDate(), "GetUsage: startDate not echoed");
        var days = resp.items().get(keyId);
        Assertions.assertTrue(days != null, "GetUsage: no daily log for the attached key");
        Assertions.assertEquals(1, days.size(), "GetUsage: expected one day of data");
        Assertions.assertEquals(2, days.get(0).size(), "GetUsage: expected a [used, remaining] pair");
    }

    private void deleteUsagePlan(TestContext ctx) throws Exception {
        String planId = ctx.getString("usagePlanId");
        String keyId = ctx.getString("usageKeyId");
        if (planId == null) throw new AssertionError("DeleteUsagePlan: prerequisite CreateUsagePlanWithLimits is not implemented");
        // AWS refuses to delete a plan that still has keys attached.
        if (keyId != null) {
            apigw().deleteUsagePlanKey(r -> r.usagePlanId(planId).keyId(keyId));
        }
        apigw().deleteUsagePlan(r -> r.usagePlanId(planId));
        boolean stillReadable = true;
        try {
            apigw().getUsagePlan(r -> r.usagePlanId(planId));
        } catch (Exception expected) {
            stillReadable = false;
        }
        Assertions.assertTrue(!stillReadable, "GetUsagePlan: deleted plan still readable");
        ctx.set("usagePlanId", null);
    }

    private void teardownUsagePlan(TestContext ctx) {
        String planId = ctx.getString("usagePlanId");
        if (planId != null) {
            try { apigw().deleteUsagePlan(r -> r.usagePlanId(planId)); } catch (Exception ignored) {}
        }
        String keyId = ctx.getString("usageKeyId");
        if (keyId != null) {
            try { apigw().deleteApiKey(r -> r.apiKey(keyId)); } catch (Exception ignored) {}
        }
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void deleteRestApiSilently(String id) {
        if (id == null) return;
        try { apigw().deleteRestApi(r -> r.restApiId(id)); } catch (Exception ignored) {}
    }

    private void deleteHttpApiSilently(String id) {
        if (id == null) return;
        try { apigwV2().deleteApi(r -> r.apiId(id)); } catch (Exception ignored) {}
    }
}
