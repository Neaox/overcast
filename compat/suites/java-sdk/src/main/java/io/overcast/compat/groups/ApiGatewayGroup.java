package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.apigateway.ApiGatewayClient;
import software.amazon.awssdk.services.apigateway.model.*;
import software.amazon.awssdk.services.apigatewayv2.ApiGatewayV2Client;
import software.amazon.awssdk.services.apigatewayv2.model.*;

import java.util.Map;

/**
 * API Gateway (REST v1 + HTTP v2) compatibility test group.
 *
 * <p>Groups: apigateway-rest (v1), apigateway-http (v2).
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
                Map.entry("DeleteApi",        this::deleteApi)
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
                Map.entry("apigateway-http", ctx -> deleteHttpApiSilently(ctx.getString("httpApiId")))
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
