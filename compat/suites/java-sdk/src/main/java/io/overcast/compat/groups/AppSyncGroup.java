package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.core.SdkBytes;
import software.amazon.awssdk.services.appsync.AppSyncClient;
import software.amazon.awssdk.services.appsync.model.*;

import java.util.Map;

/**
 * AppSync compatibility test group.
 *
 * <p>Groups: appsync-apis, appsync-schemas, appsync-api-keys, appsync-datasources,
 * appsync-functions, appsync-resolvers, appsync-types, appsync-tags, appsync-env-vars,
 * appsync-domains, appsync-cache.
 */
public final class AppSyncGroup implements ServiceGroup {

    private final AwsClients clients;

    public AppSyncGroup(AwsClients clients) {
        this.clients = clients;
    }

    private AppSyncClient appSync() { return clients.appSync(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                // appsync-apis
                Map.entry("appsync-apis:CreateGraphqlApi", this::createGraphqlApi),
                Map.entry("appsync-apis:GetGraphqlApi",    this::getGraphqlApi),
                Map.entry("appsync-apis:UpdateGraphqlApi", this::updateGraphqlApi),
                Map.entry("appsync-apis:ListGraphqlApis",  this::listGraphqlApis),
                Map.entry("appsync-apis:DeleteGraphqlApi", this::deleteGraphqlApi),
                // appsync-schemas
                Map.entry("appsync-schemas:StartSchemaCreation",     this::startSchemaCreation),
                Map.entry("appsync-schemas:GetSchemaCreationStatus", this::getSchemaCreationStatus),
                // appsync-api-keys
                Map.entry("appsync-api-keys:CreateApiKey", this::createApiKey),
                Map.entry("appsync-api-keys:ListApiKeys",  this::listApiKeys),
                Map.entry("appsync-api-keys:UpdateApiKey", this::updateApiKey),
                Map.entry("appsync-api-keys:DeleteApiKey", this::deleteApiKey),
                // appsync-datasources
                Map.entry("appsync-datasources:CreateDataSource", this::createDataSource),
                Map.entry("appsync-datasources:GetDataSource",    this::getDataSource),
                Map.entry("appsync-datasources:UpdateDataSource", this::updateDataSource),
                Map.entry("appsync-datasources:ListDataSources",  this::listDataSources),
                Map.entry("appsync-datasources:DeleteDataSource", this::deleteDataSource),
                // appsync-functions — group-qualified to avoid colliding with Lambda
                Map.entry("appsync-functions:CreateFunction", this::createFunction),
                Map.entry("appsync-functions:GetFunction",    this::getFunction),
                Map.entry("appsync-functions:UpdateFunction", this::updateFunction),
                Map.entry("appsync-functions:ListFunctions",  this::listFunctions),
                Map.entry("appsync-functions:DeleteFunction", this::deleteFunction),
                // appsync-resolvers
                Map.entry("appsync-resolvers:CreateResolver",          this::createResolver),
                Map.entry("appsync-resolvers:GetResolver",             this::getResolver),
                Map.entry("appsync-resolvers:UpdateResolver",          this::updateResolver),
                Map.entry("appsync-resolvers:ListResolvers",           this::listResolvers),
                Map.entry("appsync-resolvers:ListResolversByFunction", this::listResolversByFunction),
                Map.entry("appsync-resolvers:DeleteResolver",          this::deleteResolver),
                // appsync-types
                Map.entry("appsync-types:CreateType", this::createType),
                Map.entry("appsync-types:GetType",    this::getType),
                Map.entry("appsync-types:UpdateType", this::updateType),
                Map.entry("appsync-types:ListTypes",  this::listTypes),
                Map.entry("appsync-types:DeleteType", this::deleteType),
                // appsync-tags — group-qualified to avoid colliding with SecretsManager
                Map.entry("appsync-tags:TagResource",         this::tagResource),
                Map.entry("appsync-tags:ListTagsForResource", this::listTagsForResource),
                Map.entry("appsync-tags:UntagResource",       this::untagResource),
                // appsync-env-vars
                Map.entry("appsync-env-vars:PutGraphqlApiEnvironmentVariables", this::putEnvVars),
                Map.entry("appsync-env-vars:GetGraphqlApiEnvironmentVariables", this::getEnvVars),
                // appsync-domains
                Map.entry("appsync-domains:CreateDomainName", this::createDomainName),
                Map.entry("appsync-domains:GetDomainName",    this::getDomainName),
                Map.entry("appsync-domains:UpdateDomainName", this::updateDomainName),
                // Group-qualified: opensearch-domains declares a ListDomainNames
                // too, and a bare key cannot say which group it implements.
                Map.entry("appsync-domains:ListDomainNames",   this::listDomainNames),
                Map.entry("appsync-domains:AssociateApi",      this::associateApi),
                Map.entry("appsync-domains:GetApiAssociation", this::getApiAssociation),
                Map.entry("appsync-domains:DisassociateApi",   this::disassociateApi),
                Map.entry("appsync-domains:DeleteDomainName",  this::deleteDomainName),
                // appsync-cache
                Map.entry("appsync-cache:CreateApiCache", this::createApiCache),
                Map.entry("appsync-cache:GetApiCache",    this::getApiCache),
                Map.entry("appsync-cache:UpdateApiCache", this::updateApiCache),
                Map.entry("appsync-cache:FlushApiCache",  this::flushApiCache),
                Map.entry("appsync-cache:DeleteApiCache", this::deleteApiCache)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("appsync-apis",        this::setupApis),
                Map.entry("appsync-schemas",     this::setupGeneric),
                Map.entry("appsync-api-keys",    this::setupGeneric),
                Map.entry("appsync-datasources", this::setupGeneric),
                Map.entry("appsync-functions",   this::setupFunctions),
                Map.entry("appsync-resolvers",   this::setupResolvers),
                Map.entry("appsync-types",       this::setupTypes),
                Map.entry("appsync-tags",        this::setupTags),
                Map.entry("appsync-env-vars",    this::setupGeneric),
                Map.entry("appsync-domains",     this::setupGeneric),
                Map.entry("appsync-cache",       this::setupGeneric)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("appsync-apis",        ctx -> deleteApiSilently(ctx.getString("appSyncApiId"))),
                Map.entry("appsync-schemas",     ctx -> deleteApiSilently(ctx.getString("apiId"))),
                Map.entry("appsync-api-keys",    ctx -> deleteApiSilently(ctx.getString("apiId"))),
                Map.entry("appsync-datasources", ctx -> deleteApiSilently(ctx.getString("apiId"))),
                Map.entry("appsync-functions",   ctx -> deleteApiSilently(ctx.getString("fnApiId"))),
                Map.entry("appsync-resolvers",   ctx -> deleteApiSilently(ctx.getString("apiId"))),
                Map.entry("appsync-types",       ctx -> deleteApiSilently(ctx.getString("apiId"))),
                Map.entry("appsync-tags",        ctx -> deleteApiSilently(ctx.getString("tagApiId"))),
                Map.entry("appsync-env-vars",    ctx -> deleteApiSilently(ctx.getString("apiId"))),
                Map.entry("appsync-domains",     this::teardownDomains),
                Map.entry("appsync-cache",       this::teardownCache)
        );
    }

    // ── setup helpers ─────────────────────────────────────────────────────────

    private void setupApis(TestContext ctx) {
        ctx.set("appSyncApiName", "compat-" + ctx.runId());
    }

    private void setupGeneric(TestContext ctx) throws Exception {
        var resp = appSync().createGraphqlApi(r -> r
                .name("compat-grp-" + ctx.runId())
                .authenticationType(AuthenticationType.API_KEY));
        ctx.set("apiId", resp.graphqlApi().apiId());
        ctx.set("apiArn", resp.graphqlApi().arn());
    }

    private void setupFunctions(TestContext ctx) throws Exception {
        var resp = appSync().createGraphqlApi(r -> r
                .name("compat-fn-" + ctx.runId())
                .authenticationType(AuthenticationType.API_KEY));
        String apiId = resp.graphqlApi().apiId();
        ctx.set("fnApiId", apiId);
        appSync().createDataSource(r -> r.apiId(apiId).name("FnNoneDS").type(DataSourceType.NONE));
    }

    private void setupResolvers(TestContext ctx) throws Exception {
        var resp = appSync().createGraphqlApi(r -> r
                .name("compat-res-" + ctx.runId())
                .authenticationType(AuthenticationType.API_KEY));
        String apiId = resp.graphqlApi().apiId();
        ctx.set("apiId", apiId);
        appSync().startSchemaCreation(r -> r.apiId(apiId).definition(SdkBytes.fromUtf8String("type Query { hello: String, goodbye: String, extra: String }")));
        appSync().createDataSource(r -> r.apiId(apiId).name("ResNoneDS").type(DataSourceType.NONE));
        var fnResp = appSync().createFunction(r -> r
                .apiId(apiId).name("ResFn").dataSourceName("ResNoneDS")
                .requestMappingTemplate("{}").responseMappingTemplate("{}"));
        ctx.set("fnId", fnResp.functionConfiguration().functionId());
    }

    private void setupTypes(TestContext ctx) throws Exception {
        var resp = appSync().createGraphqlApi(r -> r
                .name("compat-types-" + ctx.runId())
                .authenticationType(AuthenticationType.API_KEY));
        String apiId = resp.graphqlApi().apiId();
        ctx.set("apiId", apiId);
        appSync().startSchemaCreation(r -> r.apiId(apiId).definition(SdkBytes.fromUtf8String("type Query { hello: String }")));
    }

    private void setupTags(TestContext ctx) throws Exception {
        var resp = appSync().createGraphqlApi(r -> r
                .name("compat-tag-" + ctx.runId())
                .authenticationType(AuthenticationType.API_KEY));
        ctx.set("tagApiId", resp.graphqlApi().apiId());
        ctx.set("tagApiArn", resp.graphqlApi().arn());
    }

    // ── teardown helpers ──────────────────────────────────────────────────────

    private void teardownDomains(TestContext ctx) {
        String dn = ctx.getString("domainName");
        if (dn != null) {
            try { appSync().disassociateApi(r -> r.domainName(dn)); } catch (Exception ignored) {}
            try { appSync().deleteDomainName(r -> r.domainName(dn)); } catch (Exception ignored) {}
        }
        deleteApiSilently(ctx.getString("apiId"));
    }

    private void teardownCache(TestContext ctx) {
        String apiId = ctx.getString("apiId");
        if (apiId != null) {
            try { appSync().deleteApiCache(r -> r.apiId(apiId)); } catch (Exception ignored) {}
        }
        deleteApiSilently(apiId);
    }

    private void deleteApiSilently(String apiId) {
        if (apiId == null) return;
        try { appSync().deleteGraphqlApi(r -> r.apiId(apiId)); } catch (Exception ignored) {}
    }

    // ── appsync-apis ──────────────────────────────────────────────────────────

    private void createGraphqlApi(TestContext ctx) throws Exception {
        String name = ctx.getString("appSyncApiName");
        var resp = appSync().createGraphqlApi(r -> r
                .name(name)
                .authenticationType(AuthenticationType.API_KEY));
        Assertions.assertNotBlank(resp.graphqlApi().apiId(), "CreateGraphqlApi: apiId is blank");
        ctx.set("appSyncApiId", resp.graphqlApi().apiId());
    }

    private void getGraphqlApi(TestContext ctx) throws Exception {
        String apiId = ctx.getString("appSyncApiId");
        var resp = appSync().getGraphqlApi(r -> r.apiId(apiId));
        Assertions.assertEquals(apiId, resp.graphqlApi().apiId(), "GetGraphqlApi: apiId mismatch");
    }

    private void updateGraphqlApi(TestContext ctx) throws Exception {
        String apiId = ctx.getString("appSyncApiId");
        var resp = appSync().updateGraphqlApi(r -> r
                .apiId(apiId)
                .name("compat-updated-" + ctx.runId())
                .authenticationType(AuthenticationType.API_KEY));
        Assertions.assertNotNull(resp.graphqlApi(), "UpdateGraphqlApi: missing graphqlApi");
    }

    private void listGraphqlApis(TestContext ctx) throws Exception {
        String apiId = ctx.getString("appSyncApiId");
        var resp = appSync().listGraphqlApis(r -> r.maxResults(25));
        boolean found = resp.graphqlApis().stream().anyMatch(a -> apiId.equals(a.apiId()));
        Assertions.assertTrue(found, "ListGraphqlApis: created api " + apiId + " not found in list");
    }

    private void deleteGraphqlApi(TestContext ctx) throws Exception {
        String apiId = ctx.getString("appSyncApiId");
        appSync().deleteGraphqlApi(r -> r.apiId(apiId));
        ctx.set("appSyncApiId", null);
    }

    // ── appsync-schemas ───────────────────────────────────────────────────────

    private void startSchemaCreation(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().startSchemaCreation(r -> r
                .apiId(apiId)
                .definition(SdkBytes.fromUtf8String("type Query { hello: String }")));
        Assertions.assertNotNull(resp.statusAsString(), "StartSchemaCreation: missing status");
    }

    private void getSchemaCreationStatus(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().getSchemaCreationStatus(r -> r.apiId(apiId));
        Assertions.assertNotNull(resp.statusAsString(), "GetSchemaCreationStatus: missing status");
    }

    // ── appsync-api-keys ──────────────────────────────────────────────────────

    private void createApiKey(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().createApiKey(r -> r.apiId(apiId).description("compat test key"));
        Assertions.assertNotBlank(resp.apiKey().id(), "CreateApiKey: missing id");
        ctx.set("keyId", resp.apiKey().id());
    }

    private void listApiKeys(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        String keyId = ctx.getString("keyId");
        var resp = appSync().listApiKeys(r -> r.apiId(apiId));
        boolean found = resp.apiKeys().stream().anyMatch(k -> k.id().equals(keyId));
        Assertions.assertTrue(found, "ListApiKeys: created key " + keyId + " not found in list");
    }

    private void updateApiKey(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        String keyId = ctx.getString("keyId");
        appSync().updateApiKey(r -> r.apiId(apiId).id(keyId).description("updated compat key"));
    }

    private void deleteApiKey(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var cr = appSync().createApiKey(r -> r.apiId(apiId));
        appSync().deleteApiKey(r -> r.apiId(apiId).id(cr.apiKey().id()));
    }

    // ── appsync-datasources ───────────────────────────────────────────────────

    private void createDataSource(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().createDataSource(r -> r
                .apiId(apiId).name("CompatNoneDS").type(DataSourceType.NONE));
        Assertions.assertEquals("CompatNoneDS", resp.dataSource().name(), "CreateDataSource: name mismatch");
    }

    private void getDataSource(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().getDataSource(r -> r.apiId(apiId).name("CompatNoneDS"));
        Assertions.assertNotNull(resp.dataSource().dataSourceArn(), "GetDataSource: missing ARN");
    }

    private void updateDataSource(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().updateDataSource(r -> r
                .apiId(apiId).name("CompatNoneDS").type(DataSourceType.NONE).description("updated"));
    }

    private void listDataSources(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().listDataSources(r -> r.apiId(apiId));
        boolean found = resp.dataSources().stream().anyMatch(d -> "CompatNoneDS".equals(d.name()));
        Assertions.assertTrue(found, "ListDataSources: CompatNoneDS not found in list");
    }

    private void deleteDataSource(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().createDataSource(r -> r.apiId(apiId).name("CompatDelDS").type(DataSourceType.NONE));
        appSync().deleteDataSource(r -> r.apiId(apiId).name("CompatDelDS"));
    }

    // ── appsync-functions ─────────────────────────────────────────────────────

    private void createFunction(TestContext ctx) throws Exception {
        String apiId = ctx.getString("fnApiId");
        var resp = appSync().createFunction(r -> r
                .apiId(apiId).name("CompatFn").dataSourceName("FnNoneDS")
                .requestMappingTemplate("{\"version\":\"2018-05-29\",\"payload\":{}}")
                .responseMappingTemplate("$util.toJson($context.result)"));
        Assertions.assertNotBlank(resp.functionConfiguration().functionId(), "CreateFunction: missing functionId");
        ctx.set("fnFuncId", resp.functionConfiguration().functionId());
    }

    private void getFunction(TestContext ctx) throws Exception {
        String apiId = ctx.getString("fnApiId");
        String fnId = ctx.getString("fnFuncId");
        var resp = appSync().getFunction(r -> r.apiId(apiId).functionId(fnId));
        Assertions.assertNotNull(resp.functionConfiguration().name(), "GetFunction: missing name");
    }

    private void updateFunction(TestContext ctx) throws Exception {
        String apiId = ctx.getString("fnApiId");
        String fnId = ctx.getString("fnFuncId");
        appSync().updateFunction(r -> r
                .apiId(apiId).functionId(fnId).name("CompatFnUpdated").dataSourceName("FnNoneDS")
                .requestMappingTemplate("{\"version\":\"2018-05-29\",\"payload\":{}}")
                .responseMappingTemplate("$util.toJson($context.result)"));
    }

    private void listFunctions(TestContext ctx) throws Exception {
        String apiId = ctx.getString("fnApiId");
        String fnId = ctx.getString("fnFuncId");
        var resp = appSync().listFunctions(r -> r.apiId(apiId));
        boolean found = resp.functions().stream().anyMatch(f -> f.functionId().equals(fnId));
        Assertions.assertTrue(found, "ListFunctions: created function " + fnId + " not found in list");
    }

    private void deleteFunction(TestContext ctx) throws Exception {
        String apiId = ctx.getString("fnApiId");
        var cr = appSync().createFunction(r -> r
                .apiId(apiId).name("CompatFnDel").dataSourceName("FnNoneDS")
                .requestMappingTemplate("{}").responseMappingTemplate("{}"));
        appSync().deleteFunction(r -> r.apiId(apiId).functionId(cr.functionConfiguration().functionId()));
    }

    // ── appsync-resolvers ─────────────────────────────────────────────────────

    private void createResolver(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().createResolver(r -> r
                .apiId(apiId).typeName("Query").fieldName("hello")
                .dataSourceName("ResNoneDS").kind(ResolverKind.UNIT)
                .requestMappingTemplate("{\"version\":\"2018-05-29\",\"payload\":\"world\"}")
                .responseMappingTemplate("$util.toJson($context.result)"));
        Assertions.assertNotNull(resp.resolver().fieldName(), "CreateResolver: missing fieldName");
    }

    private void getResolver(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().getResolver(r -> r.apiId(apiId).typeName("Query").fieldName("hello"));
        Assertions.assertNotNull(resp.resolver().resolverArn(), "GetResolver: missing resolverArn");
    }

    private void updateResolver(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().updateResolver(r -> r
                .apiId(apiId).typeName("Query").fieldName("hello")
                .dataSourceName("ResNoneDS").kind(ResolverKind.UNIT)
                .requestMappingTemplate("{\"version\":\"2018-05-29\",\"payload\":\"updated\"}")
                .responseMappingTemplate("$util.toJson($context.result)"));
    }

    private void listResolvers(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().listResolvers(r -> r.apiId(apiId).typeName("Query"));
        boolean found = resp.resolvers().stream().anyMatch(res -> "hello".equals(res.fieldName()));
        Assertions.assertTrue(found, "ListResolvers: Query.hello resolver not found in list");
    }

    private void listResolversByFunction(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        String fnId = ctx.getString("fnId");
        appSync().createResolver(r -> r
                .apiId(apiId).typeName("Query").fieldName("goodbye")
                .kind(ResolverKind.PIPELINE)
                .pipelineConfig(p -> p.functions(fnId))
                .requestMappingTemplate("{}").responseMappingTemplate("$util.toJson($context.result)"));
        var resp = appSync().listResolversByFunction(r -> r.apiId(apiId).functionId(fnId));
        boolean found = resp.resolvers().stream().anyMatch(res -> "goodbye".equals(res.fieldName()));
        Assertions.assertTrue(found, "ListResolversByFunction: Query.goodbye pipeline resolver not found in list");
    }

    private void deleteResolver(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().createResolver(r -> r
                .apiId(apiId).typeName("Query").fieldName("extra")
                .dataSourceName("ResNoneDS").kind(ResolverKind.UNIT));
        appSync().deleteResolver(r -> r.apiId(apiId).typeName("Query").fieldName("extra"));
    }

    // ── appsync-types ─────────────────────────────────────────────────────────

    private void createType(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().createType(r -> r
                .apiId(apiId)
                .definition("type CompatType { id: ID, name: String }")
                .format(TypeDefinitionFormat.SDL));
        Assertions.assertNotNull(resp.type().name(), "CreateType: missing name");
    }

    private void getType(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().getType(r -> r.apiId(apiId).typeName("CompatType").format(TypeDefinitionFormat.SDL));
        Assertions.assertEquals("CompatType", resp.type().name(), "GetType: name mismatch");
        Assertions.assertNotBlank(resp.type().definition(), "GetType: definition is blank");
    }

    private void updateType(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().updateType(r -> r
                .apiId(apiId).typeName("CompatType")
                .definition("type CompatType { id: ID, name: String, age: Int }")
                .format(TypeDefinitionFormat.SDL));
    }

    private void listTypes(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().listTypes(r -> r.apiId(apiId).format(TypeDefinitionFormat.SDL));
        boolean found = resp.types().stream().anyMatch(t -> "CompatType".equals(t.name()));
        Assertions.assertTrue(found, "ListTypes: CompatType not found in list");
    }

    private void deleteType(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().createType(r -> r
                .apiId(apiId)
                .definition("type CompatDelType { x: String }")
                .format(TypeDefinitionFormat.SDL));
        appSync().deleteType(r -> r.apiId(apiId).typeName("CompatDelType"));
    }

    // ── appsync-tags ──────────────────────────────────────────────────────────

    private void tagResource(TestContext ctx) throws Exception {
        String arn = ctx.getString("tagApiArn");
        appSync().tagResource(r -> r.resourceArn(arn).tags(Map.of("env", "compat")));
    }

    private void listTagsForResource(TestContext ctx) throws Exception {
        String arn = ctx.getString("tagApiArn");
        var resp = appSync().listTagsForResource(r -> r.resourceArn(arn));
        Assertions.assertEquals("compat", resp.tags().get("env"), "ListTagsForResource: tag value mismatch");
    }

    private void untagResource(TestContext ctx) throws Exception {
        String arn = ctx.getString("tagApiArn");
        appSync().untagResource(r -> r.resourceArn(arn).tagKeys("env"));
    }

    // ── appsync-env-vars ──────────────────────────────────────────────────────

    private void putEnvVars(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().putGraphqlApiEnvironmentVariables(r -> r
                .apiId(apiId)
                .environmentVariables(Map.of("DB_HOST", "localhost", "DB_PORT", "5432")));
    }

    private void getEnvVars(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().getGraphqlApiEnvironmentVariables(r -> r.apiId(apiId));
        Assertions.assertEquals("localhost", resp.environmentVariables().get("DB_HOST"),
                "GetGraphqlApiEnvironmentVariables: value mismatch");
    }

    // ── appsync-domains ───────────────────────────────────────────────────────

    private void createDomainName(TestContext ctx) throws Exception {
        String dn = ctx.runId() + ".example.com";
        var resp = appSync().createDomainName(r -> r
                .domainName(dn)
                .certificateArn("arn:aws:acm:us-east-1:000000000000:certificate/test"));
        Assertions.assertNotNull(resp.domainNameConfig().domainName(), "CreateDomainName: missing domainName");
        ctx.set("domainName", dn);
    }

    private void getDomainName(TestContext ctx) throws Exception {
        String dn = ctx.getString("domainName");
        var resp = appSync().getDomainName(r -> r.domainName(dn));
        Assertions.assertEquals(dn, resp.domainNameConfig().domainName(), "GetDomainName: domainName mismatch");
    }

    private void updateDomainName(TestContext ctx) throws Exception {
        String dn = ctx.getString("domainName");
        appSync().updateDomainName(r -> r.domainName(dn).description("updated-domain"));
    }

    private void listDomainNames(TestContext ctx) throws Exception {
        String dn = ctx.getString("domainName");
        var resp = appSync().listDomainNames(r -> {});
        boolean found = resp.domainNameConfigs().stream().anyMatch(d -> dn.equals(d.domainName()));
        Assertions.assertTrue(found, "ListDomainNames: created domain " + dn + " not found in list");
    }

    private void associateApi(TestContext ctx) throws Exception {
        String dn = ctx.getString("domainName");
        String apiId = ctx.getString("apiId");
        var resp = appSync().associateApi(r -> r.domainName(dn).apiId(apiId));
        Assertions.assertNotNull(resp.apiAssociation(), "AssociateApi: missing apiAssociation");
    }

    private void getApiAssociation(TestContext ctx) throws Exception {
        String dn = ctx.getString("domainName");
        var resp = appSync().getApiAssociation(r -> r.domainName(dn));
        Assertions.assertNotNull(resp.apiAssociation(), "GetApiAssociation: missing apiAssociation");
    }

    private void disassociateApi(TestContext ctx) throws Exception {
        String dn = ctx.getString("domainName");
        appSync().disassociateApi(r -> r.domainName(dn));
    }

    private void deleteDomainName(TestContext ctx) throws Exception {
        String dn = "del-" + ctx.runId() + ".example.com";
        appSync().createDomainName(r -> r
                .domainName(dn)
                .certificateArn("arn:aws:acm:us-east-1:000000000000:certificate/test"));
        appSync().deleteDomainName(r -> r.domainName(dn));
    }

    // ── appsync-cache ─────────────────────────────────────────────────────────

    private void createApiCache(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().createApiCache(r -> r
                .apiId(apiId)
                .type(ApiCacheType.T2_SMALL)
                .apiCachingBehavior(ApiCachingBehavior.FULL_REQUEST_CACHING)
                .ttl(300L));
        Assertions.assertNotNull(resp.apiCache(), "CreateApiCache: missing apiCache");
    }

    private void getApiCache(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        var resp = appSync().getApiCache(r -> r.apiId(apiId));
        Assertions.assertNotNull(resp.apiCache(), "GetApiCache: missing apiCache");
        Assertions.assertEquals(ApiCacheType.T2_SMALL, resp.apiCache().type(), "GetApiCache: type mismatch");
    }

    private void updateApiCache(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().updateApiCache(r -> r
                .apiId(apiId)
                .type(ApiCacheType.T2_MEDIUM)
                .apiCachingBehavior(ApiCachingBehavior.FULL_REQUEST_CACHING)
                .ttl(600L));
    }

    private void flushApiCache(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().flushApiCache(r -> r.apiId(apiId));
    }

    private void deleteApiCache(TestContext ctx) throws Exception {
        String apiId = ctx.getString("apiId");
        appSync().deleteApiCache(r -> r.apiId(apiId));
    }
}
