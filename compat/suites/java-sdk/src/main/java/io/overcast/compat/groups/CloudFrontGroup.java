package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.cloudfront.CloudFrontClient;
import software.amazon.awssdk.services.cloudfront.model.*;

import java.util.List;
import java.util.Map;

/**
 * CloudFront compatibility test group.
 */
public final class CloudFrontGroup implements ServiceGroup {

    private final AwsClients clients;

    public CloudFrontGroup(AwsClients clients) {
        this.clients = clients;
    }

    private CloudFrontClient cf() { return clients.cloudFront(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                // distributions
                Map.entry("CreateDistribution",  this::createDistribution),
                Map.entry("GetDistribution",     this::getDistribution),
                Map.entry("ListDistributions",   this::listDistributions),
                Map.entry("DeleteDistribution",  this::deleteDistribution),
                // oac
                Map.entry("CreateOriginAccessControl",  this::createOriginAccessControl),
                Map.entry("GetOriginAccessControl",     this::getOriginAccessControl),
                Map.entry("UpdateOriginAccessControl",  this::updateOriginAccessControl),
                Map.entry("ListOriginAccessControls",   this::listOriginAccessControls),
                Map.entry("DeleteOriginAccessControl",  this::deleteOriginAccessControl),
                // cache policy
                Map.entry("CreateCachePolicy",      this::createCachePolicy),
                Map.entry("GetCachePolicy",         this::getCachePolicy),
                Map.entry("GetCachePolicyConfig",   this::getCachePolicyConfig),
                Map.entry("UpdateCachePolicy",      this::updateCachePolicy),
                Map.entry("ListCachePolicies",      this::listCachePolicies),
                Map.entry("DeleteCachePolicy",      this::deleteCachePolicy),
                // key group
                Map.entry("CreateKeyGroup",      this::createKeyGroup),
                Map.entry("GetKeyGroup",         this::getKeyGroup),
                Map.entry("GetKeyGroupConfig",   this::getKeyGroupConfig),
                Map.entry("UpdateKeyGroup",      this::updateKeyGroup),
                Map.entry("ListKeyGroups",       this::listKeyGroups),
                Map.entry("DeleteKeyGroup",      this::deleteKeyGroup),
                // realtime log
                Map.entry("CreateRealtimeLogConfig",  this::createRealtimeLogConfig),
                Map.entry("GetRealtimeLogConfig",     this::getRealtimeLogConfig),
                Map.entry("UpdateRealtimeLogConfig",  this::updateRealtimeLogConfig),
                Map.entry("ListRealtimeLogConfigs",   this::listRealtimeLogConfigs),
                Map.entry("DeleteRealtimeLogConfig",  this::deleteRealtimeLogConfig),
                // monitoring
                Map.entry("CreateMonitoringSubscription",  this::createMonitoringSubscription),
                Map.entry("GetMonitoringSubscription",     this::getMonitoringSubscription),
                Map.entry("DeleteMonitoringSubscription",  this::deleteMonitoringSubscription),
                // fle config
                Map.entry("CreateFieldLevelEncryptionConfig",  this::createFieldLevelEncryptionConfig),
                Map.entry("GetFieldLevelEncryption",           this::getFieldLevelEncryption),
                Map.entry("GetFieldLevelEncryptionConfig",     this::getFieldLevelEncryptionConfig),
                Map.entry("UpdateFieldLevelEncryptionConfig",  this::updateFieldLevelEncryptionConfig),
                Map.entry("ListFieldLevelEncryptionConfigs",   this::listFieldLevelEncryptionConfigs),
                Map.entry("DeleteFieldLevelEncryption",        this::deleteFieldLevelEncryption),
                // fle profile
                Map.entry("CreateFieldLevelEncryptionProfile",       this::createFieldLevelEncryptionProfile),
                Map.entry("GetFieldLevelEncryptionProfile",          this::getFieldLevelEncryptionProfile),
                Map.entry("GetFieldLevelEncryptionProfileConfig",    this::getFieldLevelEncryptionProfileConfig),
                Map.entry("UpdateFieldLevelEncryptionProfile",       this::updateFieldLevelEncryptionProfile),
                Map.entry("ListFieldLevelEncryptionProfiles",        this::listFieldLevelEncryptionProfiles),
                Map.entry("DeleteFieldLevelEncryptionProfile",       this::deleteFieldLevelEncryptionProfile),
                // continuous deployment
                Map.entry("CreateContinuousDeploymentPolicy",       this::createContinuousDeploymentPolicy),
                Map.entry("GetContinuousDeploymentPolicy",          this::getContinuousDeploymentPolicy),
                Map.entry("GetContinuousDeploymentPolicyConfig",    this::getContinuousDeploymentPolicyConfig),
                Map.entry("UpdateContinuousDeploymentPolicy",       this::updateContinuousDeploymentPolicy),
                Map.entry("ListContinuousDeploymentPolicies",       this::listContinuousDeploymentPolicies),
                Map.entry("DeleteContinuousDeploymentPolicy",       this::deleteContinuousDeploymentPolicy)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "cloudfront-distributions", this::setupDistributions,
                "cloudfront-monitoring",    this::setupMonitoring
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("cloudfront-distributions",       this::teardownDistributions),
                Map.entry("cloudfront-oac",                 this::teardownOac),
                Map.entry("cloudfront-cache-policy",        this::teardownCachePolicy),
                Map.entry("cloudfront-key-group",           this::teardownKeyGroup),
                Map.entry("cloudfront-realtime-log",        this::teardownRealtimeLog),
                Map.entry("cloudfront-monitoring",          this::teardownMonitoring),
                Map.entry("cloudfront-fle-config",          this::teardownFleConfig),
                Map.entry("cloudfront-fle-profile",         this::teardownFleProfile),
                Map.entry("cloudfront-continuous-deployment", this::teardownCdp)
        );
    }

    // ── helpers ───────────────────────────────────────────────────────────────

    private DistributionConfig distroConfig(String callerRef) {
        return DistributionConfig.builder()
                .callerReference(callerRef)
                .enabled(true)
                .comment("Overcast compat test")
                .origins(Origins.builder()
                        .quantity(1)
                        .items(Origin.builder()
                                .id("origin-1")
                                .domainName("example.com")
                                .s3OriginConfig(S3OriginConfig.builder().originAccessIdentity("").build())
                                .build())
                        .build())
                .defaultCacheBehavior(DefaultCacheBehavior.builder()
                        .targetOriginId("origin-1")
                        .viewerProtocolPolicy(ViewerProtocolPolicy.REDIRECT_TO_HTTPS)
                        .forwardedValues(ForwardedValues.builder()
                                .queryString(false)
                                .cookies(CookiePreference.builder()
                                        .forward(ItemSelection.NONE).build())
                                .build())
                        .minTTL(0L)
                        .build())
                .build();
    }

    private void disableAndDelete(String id) {
        try {
            var resp = cf().getDistribution(r -> r.id(id));
            String etag = resp.eTag();
            var config = resp.distribution().distributionConfig();
            if (config.enabled()) {
                config = config.toBuilder().enabled(false).build();
                final String disableEtag = etag;
                final var disableConfig = config;
                var upd = cf().updateDistribution(r -> r.id(id).distributionConfig(disableConfig).ifMatch(disableEtag));
                etag = upd.eTag();
            }
            final String deleteEtag = etag;
            cf().deleteDistribution(r -> r.id(id).ifMatch(deleteEtag));
        } catch (Exception ignored) {}
    }

    // ── cloudfront-distributions ──────────────────────────────────────────────

    private void setupDistributions(TestContext ctx) {
        ctx.set("cfOriginDomain", "compat-" + ctx.runId() + ".s3.amazonaws.com");
    }

    private void teardownDistributions(TestContext ctx) {
        String id = ctx.getString("cfDistId");
        if (id != null) disableAndDelete(id);
    }

    private void createDistribution(TestContext ctx) throws Exception {
        var config = distroConfig("compat-" + ctx.runId());
        var resp = cf().createDistribution(r -> r.distributionConfig(config));
        Assertions.assertNotBlank(resp.distribution().id(), "CreateDistribution: id is blank");
        ctx.set("cfDistId",   resp.distribution().id());
        ctx.set("cfDistEtag", resp.eTag());
    }

    private void getDistribution(TestContext ctx) throws Exception {
        String id = ctx.getString("cfDistId");
        if (id == null) throw new AssertionError("GetDistribution: prerequisite missing");
        var resp = cf().getDistribution(r -> r.id(id));
        Assertions.assertEquals(id, resp.distribution().id(), "GetDistribution: id mismatch");
    }

    private void listDistributions(TestContext ctx) throws Exception {
        var resp = cf().listDistributions(r -> r.maxItems("100"));
        Assertions.assertNotNull(resp.distributionList(), "ListDistributions: distributionList is null");
    }

    private void deleteDistribution(TestContext ctx) throws Exception {
        String id = ctx.getString("cfDistId");
        if (id == null) return;
        var get    = cf().getDistribution(r -> r.id(id));
        var config = get.distribution().distributionConfig().toBuilder().enabled(false).build();
        var upd    = cf().updateDistribution(r -> r.id(id).distributionConfig(config).ifMatch(get.eTag()));
        cf().deleteDistribution(r -> r.id(id).ifMatch(upd.eTag()));
        ctx.set("cfDistId",   null);
        ctx.set("cfDistEtag", null);
    }

    // ── cloudfront-oac ───────────────────────────────────────────────────────

    private void teardownOac(TestContext ctx) {
        String id = ctx.getString("oacId");
        if (id == null) return;
        try {
            var resp = cf().getOriginAccessControl(r -> r.id(id));
            final String etag = resp.eTag();
            cf().deleteOriginAccessControl(r -> r.id(id).ifMatch(etag));
        } catch (Exception ignored) {}
    }

    private void createOriginAccessControl(TestContext ctx) throws Exception {
        var oacConfig = OriginAccessControlConfig.builder()
                .name("oc-oac-" + ctx.runId())
                .originAccessControlOriginType(OriginAccessControlOriginTypes.S3)
                .signingBehavior(OriginAccessControlSigningBehaviors.ALWAYS)
                .signingProtocol(OriginAccessControlSigningProtocols.SIGV4)
                .build();
        var resp = cf().createOriginAccessControl(r -> r.originAccessControlConfig(oacConfig));
        Assertions.assertNotBlank(resp.originAccessControl().id(), "CreateOriginAccessControl: id is blank");
        ctx.set("oacId",   resp.originAccessControl().id());
        ctx.set("oacEtag", resp.eTag());
    }

    private void getOriginAccessControl(TestContext ctx) throws Exception {
        String id = ctx.getString("oacId");
        if (id == null) throw new AssertionError("GetOriginAccessControl: prerequisite missing");
        var resp = cf().getOriginAccessControl(r -> r.id(id));
        Assertions.assertNotBlank(resp.originAccessControl().id(), "GetOriginAccessControl: id is blank");
    }

    private void updateOriginAccessControl(TestContext ctx) throws Exception {
        String id   = ctx.getString("oacId");
        String etag = ctx.getString("oacEtag");
        if (id == null || etag == null) throw new AssertionError("UpdateOriginAccessControl: prerequisite missing");
        var oacConfig = OriginAccessControlConfig.builder()
                .name("oc-oac-" + ctx.runId())
                .originAccessControlOriginType(OriginAccessControlOriginTypes.S3)
                .signingBehavior(OriginAccessControlSigningBehaviors.NEVER)
                .signingProtocol(OriginAccessControlSigningProtocols.SIGV4)
                .build();
        var resp = cf().updateOriginAccessControl(r -> r.id(id).ifMatch(etag).originAccessControlConfig(oacConfig));
        ctx.set("oacEtag", resp.eTag());
    }

    private void listOriginAccessControls(TestContext ctx) throws Exception {
        var resp = cf().listOriginAccessControls(r -> {});
        Assertions.assertNotNull(resp.originAccessControlList(), "ListOriginAccessControls: list is null");
    }

    private void deleteOriginAccessControl(TestContext ctx) throws Exception {
        String id   = ctx.getString("oacId");
        String etag = ctx.getString("oacEtag");
        if (id == null || etag == null) return;
        cf().deleteOriginAccessControl(r -> r.id(id).ifMatch(etag));
        ctx.set("oacId", null);
    }

    // ── cloudfront-cache-policy ──────────────────────────────────────────────

    private void teardownCachePolicy(TestContext ctx) {
        String id = ctx.getString("cpId");
        if (id == null) return;
        try {
            var resp = cf().getCachePolicy(r -> r.id(id));
            final String etag = resp.eTag();
            cf().deleteCachePolicy(r -> r.id(id).ifMatch(etag));
        } catch (Exception ignored) {}
    }

    private ParametersInCacheKeyAndForwardedToOrigin cacheKeyParams(boolean gzip) {
        return ParametersInCacheKeyAndForwardedToOrigin.builder()
                .cookiesConfig(CachePolicyCookiesConfig.builder().cookieBehavior(CachePolicyCookieBehavior.NONE).build())
                .enableAcceptEncodingGzip(gzip)
                .headersConfig(CachePolicyHeadersConfig.builder().headerBehavior(CachePolicyHeaderBehavior.NONE).build())
                .queryStringsConfig(CachePolicyQueryStringsConfig.builder().queryStringBehavior(CachePolicyQueryStringBehavior.NONE).build())
                .build();
    }

    private void createCachePolicy(TestContext ctx) throws Exception {
        var cpConfig = CachePolicyConfig.builder()
                .name("oc-cp-" + ctx.runId())
                .minTTL(0L).defaultTTL(86400L).maxTTL(31536000L)
                .parametersInCacheKeyAndForwardedToOrigin(cacheKeyParams(false))
                .build();
        var resp = cf().createCachePolicy(r -> r.cachePolicyConfig(cpConfig));
        Assertions.assertNotBlank(resp.cachePolicy().id(), "CreateCachePolicy: id is blank");
        ctx.set("cpId",   resp.cachePolicy().id());
        ctx.set("cpEtag", resp.eTag());
    }

    private void getCachePolicy(TestContext ctx) throws Exception {
        String id = ctx.getString("cpId");
        if (id == null) throw new AssertionError("GetCachePolicy: prerequisite missing");
        var resp = cf().getCachePolicy(r -> r.id(id));
        Assertions.assertNotBlank(resp.cachePolicy().id(), "GetCachePolicy: id is blank");
    }

    private void getCachePolicyConfig(TestContext ctx) throws Exception {
        String id = ctx.getString("cpId");
        if (id == null) throw new AssertionError("GetCachePolicyConfig: prerequisite missing");
        var resp = cf().getCachePolicyConfig(r -> r.id(id));
        Assertions.assertNotNull(resp.cachePolicyConfig(), "GetCachePolicyConfig: config is null");
    }

    private void updateCachePolicy(TestContext ctx) throws Exception {
        String id   = ctx.getString("cpId");
        String etag = ctx.getString("cpEtag");
        if (id == null || etag == null) throw new AssertionError("UpdateCachePolicy: prerequisite missing");
        var cpConfig = CachePolicyConfig.builder()
                .name("oc-cp-" + ctx.runId())
                .minTTL(0L).defaultTTL(3600L).maxTTL(86400L)
                .parametersInCacheKeyAndForwardedToOrigin(cacheKeyParams(true))
                .build();
        var resp = cf().updateCachePolicy(r -> r.id(id).ifMatch(etag).cachePolicyConfig(cpConfig));
        ctx.set("cpEtag", resp.eTag());
    }

    private void listCachePolicies(TestContext ctx) throws Exception {
        var resp = cf().listCachePolicies(r -> {});
        Assertions.assertNotNull(resp.cachePolicyList(), "ListCachePolicies: list is null");
    }

    private void deleteCachePolicy(TestContext ctx) throws Exception {
        String id   = ctx.getString("cpId");
        String etag = ctx.getString("cpEtag");
        if (id == null || etag == null) return;
        cf().deleteCachePolicy(r -> r.id(id).ifMatch(etag));
        ctx.set("cpId", null);
    }

    // ── cloudfront-key-group ─────────────────────────────────────────────────

    private void teardownKeyGroup(TestContext ctx) {
        String id = ctx.getString("kgId");
        if (id == null) return;
        try {
            var resp = cf().getKeyGroup(r -> r.id(id));
            final String etag = resp.eTag();
            cf().deleteKeyGroup(r -> r.id(id).ifMatch(etag));
        } catch (Exception ignored) {}
    }

    private void createKeyGroup(TestContext ctx) throws Exception {
        var kgConfig = KeyGroupConfig.builder()
                .name("oc-kg-" + ctx.runId())
                .items("K1234567890ABCDE")
                .build();
        var resp = cf().createKeyGroup(r -> r.keyGroupConfig(kgConfig));
        Assertions.assertNotBlank(resp.keyGroup().id(), "CreateKeyGroup: id is blank");
        ctx.set("kgId",   resp.keyGroup().id());
        ctx.set("kgEtag", resp.eTag());
    }

    private void getKeyGroup(TestContext ctx) throws Exception {
        String id = ctx.getString("kgId");
        if (id == null) throw new AssertionError("GetKeyGroup: prerequisite missing");
        var resp = cf().getKeyGroup(r -> r.id(id));
        Assertions.assertNotBlank(resp.keyGroup().id(), "GetKeyGroup: id is blank");
    }

    private void getKeyGroupConfig(TestContext ctx) throws Exception {
        String id = ctx.getString("kgId");
        if (id == null) throw new AssertionError("GetKeyGroupConfig: prerequisite missing");
        var resp = cf().getKeyGroupConfig(r -> r.id(id));
        Assertions.assertNotNull(resp.keyGroupConfig(), "GetKeyGroupConfig: config is null");
    }

    private void updateKeyGroup(TestContext ctx) throws Exception {
        String id   = ctx.getString("kgId");
        String etag = ctx.getString("kgEtag");
        if (id == null || etag == null) throw new AssertionError("UpdateKeyGroup: prerequisite missing");
        var kgConfig = KeyGroupConfig.builder()
                .name("oc-kg-" + ctx.runId())
                .comment("updated")
                .items("K1234567890ABCDE")
                .build();
        var resp = cf().updateKeyGroup(r -> r.id(id).ifMatch(etag).keyGroupConfig(kgConfig));
        ctx.set("kgEtag", resp.eTag());
    }

    private void listKeyGroups(TestContext ctx) throws Exception {
        var resp = cf().listKeyGroups(r -> {});
        Assertions.assertNotNull(resp.keyGroupList(), "ListKeyGroups: list is null");
    }

    private void deleteKeyGroup(TestContext ctx) throws Exception {
        String id   = ctx.getString("kgId");
        String etag = ctx.getString("kgEtag");
        if (id == null || etag == null) return;
        cf().deleteKeyGroup(r -> r.id(id).ifMatch(etag));
        ctx.set("kgId", null);
    }

    // ── cloudfront-realtime-log ──────────────────────────────────────────────

    private void teardownRealtimeLog(TestContext ctx) {
        String name = ctx.getString("rlcName");
        if (name == null) return;
        try {
            cf().deleteRealtimeLogConfig(r -> r.name(name));
        } catch (Exception ignored) {}
    }

    private void createRealtimeLogConfig(TestContext ctx) throws Exception {
        String name = "oc-rlc-" + ctx.runId();
        var resp = cf().createRealtimeLogConfig(r -> r
                .name(name)
                .samplingRate(100L)
                .endPoints(EndPoint.builder()
                        .streamType("Kinesis")
                        .kinesisStreamConfig(KinesisStreamConfig.builder()
                                .roleARN("arn:aws:iam::000000000000:role/test")
                                .streamARN("arn:aws:kinesis:us-east-1:000000000000:stream/test")
                                .build())
                        .build())
                .fields("timestamp", "c-ip"));
        Assertions.assertNotBlank(resp.realtimeLogConfig().arn(), "CreateRealtimeLogConfig: arn is blank");
        ctx.set("rlcName", name);
        ctx.set("rlcArn",  resp.realtimeLogConfig().arn());
    }

    private void getRealtimeLogConfig(TestContext ctx) throws Exception {
        String name = ctx.getString("rlcName");
        if (name == null) throw new AssertionError("GetRealtimeLogConfig: prerequisite missing");
        var resp = cf().getRealtimeLogConfig(r -> r.name(name));
        Assertions.assertNotBlank(resp.realtimeLogConfig().arn(), "GetRealtimeLogConfig: arn is blank");
    }

    private void updateRealtimeLogConfig(TestContext ctx) throws Exception {
        String name = ctx.getString("rlcName");
        String arn  = ctx.getString("rlcArn");
        if (name == null || arn == null) throw new AssertionError("UpdateRealtimeLogConfig: prerequisite missing");
        cf().updateRealtimeLogConfig(r -> r
                .name(name)
                .arn(arn)
                .samplingRate(50L)
                .endPoints(EndPoint.builder()
                        .streamType("Kinesis")
                        .kinesisStreamConfig(KinesisStreamConfig.builder()
                                .roleARN("arn:aws:iam::000000000000:role/test")
                                .streamARN("arn:aws:kinesis:us-east-1:000000000000:stream/test")
                                .build())
                        .build())
                .fields("timestamp", "c-ip", "sc-status"));
    }

    private void listRealtimeLogConfigs(TestContext ctx) throws Exception {
        var resp = cf().listRealtimeLogConfigs(r -> {});
        Assertions.assertNotNull(resp.realtimeLogConfigs(), "ListRealtimeLogConfigs: list is null");
    }

    private void deleteRealtimeLogConfig(TestContext ctx) throws Exception {
        String name = ctx.getString("rlcName");
        if (name == null) return;
        cf().deleteRealtimeLogConfig(r -> r.name(name));
        ctx.set("rlcName", null);
    }

    // ── cloudfront-monitoring ────────────────────────────────────────────────

    private void setupMonitoring(TestContext ctx) {
        var config = distroConfig("compat-mon-" + ctx.runId());
        var resp = cf().createDistribution(r -> r.distributionConfig(config));
        ctx.set("monDistroId", resp.distribution().id());
    }

    private void teardownMonitoring(TestContext ctx) {
        String distId = ctx.getString("monDistroId");
        if (distId == null) return;
        try {
            cf().deleteMonitoringSubscription(r -> r.distributionId(distId));
        } catch (Exception ignored) {}
        disableAndDelete(distId);
    }

    private void createMonitoringSubscription(TestContext ctx) throws Exception {
        String distId = ctx.getString("monDistroId");
        if (distId == null) throw new AssertionError("CreateMonitoringSubscription: prerequisite missing");
        cf().createMonitoringSubscription(r -> r
                .distributionId(distId)
                .monitoringSubscription(MonitoringSubscription.builder()
                        .realtimeMetricsSubscriptionConfig(RealtimeMetricsSubscriptionConfig.builder()
                                .realtimeMetricsSubscriptionStatus(RealtimeMetricsSubscriptionStatus.ENABLED)
                                .build())
                        .build()));
    }

    private void getMonitoringSubscription(TestContext ctx) throws Exception {
        String distId = ctx.getString("monDistroId");
        if (distId == null) throw new AssertionError("GetMonitoringSubscription: prerequisite missing");
        var resp = cf().getMonitoringSubscription(r -> r.distributionId(distId));
        Assertions.assertNotNull(resp.monitoringSubscription(), "GetMonitoringSubscription: subscription is null");
    }

    private void deleteMonitoringSubscription(TestContext ctx) throws Exception {
        String distId = ctx.getString("monDistroId");
        if (distId == null) return;
        cf().deleteMonitoringSubscription(r -> r.distributionId(distId));
    }

    // ── cloudfront-fle-config ────────────────────────────────────────────────

    private void teardownFleConfig(TestContext ctx) {
        String id = ctx.getString("fleId");
        if (id == null) return;
        try {
            var resp = cf().getFieldLevelEncryption(r -> r.id(id));
            final String etag = resp.eTag();
            cf().deleteFieldLevelEncryptionConfig(r -> r.id(id).ifMatch(etag));
        } catch (Exception ignored) {}
    }

    private void createFieldLevelEncryptionConfig(TestContext ctx) throws Exception {
        var fleConfig = FieldLevelEncryptionConfig.builder()
                .callerReference("oc-fle-" + ctx.runId())
                .comment("compat test")
                .build();
        var resp = cf().createFieldLevelEncryptionConfig(r -> r.fieldLevelEncryptionConfig(fleConfig));
        Assertions.assertNotBlank(resp.fieldLevelEncryption().id(), "CreateFieldLevelEncryptionConfig: id is blank");
        ctx.set("fleId",   resp.fieldLevelEncryption().id());
        ctx.set("fleEtag", resp.eTag());
    }

    private void getFieldLevelEncryption(TestContext ctx) throws Exception {
        String id = ctx.getString("fleId");
        if (id == null) throw new AssertionError("GetFieldLevelEncryption: prerequisite missing");
        var resp = cf().getFieldLevelEncryption(r -> r.id(id));
        Assertions.assertNotBlank(resp.fieldLevelEncryption().id(), "GetFieldLevelEncryption: id is blank");
    }

    private void getFieldLevelEncryptionConfig(TestContext ctx) throws Exception {
        String id = ctx.getString("fleId");
        if (id == null) throw new AssertionError("GetFieldLevelEncryptionConfig: prerequisite missing");
        var resp = cf().getFieldLevelEncryptionConfig(r -> r.id(id));
        Assertions.assertNotNull(resp.fieldLevelEncryptionConfig(), "GetFieldLevelEncryptionConfig: config is null");
    }

    private void updateFieldLevelEncryptionConfig(TestContext ctx) throws Exception {
        String id   = ctx.getString("fleId");
        String etag = ctx.getString("fleEtag");
        if (id == null || etag == null) throw new AssertionError("UpdateFieldLevelEncryptionConfig: prerequisite missing");
        var fleConfig = FieldLevelEncryptionConfig.builder()
                .callerReference("oc-fle-" + ctx.runId())
                .comment("compat test updated")
                .build();
        var resp = cf().updateFieldLevelEncryptionConfig(r -> r.id(id).ifMatch(etag).fieldLevelEncryptionConfig(fleConfig));
        ctx.set("fleEtag", resp.eTag());
    }

    private void listFieldLevelEncryptionConfigs(TestContext ctx) throws Exception {
        var resp = cf().listFieldLevelEncryptionConfigs(r -> {});
        Assertions.assertNotNull(resp.fieldLevelEncryptionList(), "ListFieldLevelEncryptionConfigs: list is null");
    }

    private void deleteFieldLevelEncryption(TestContext ctx) throws Exception {
        String id   = ctx.getString("fleId");
        String etag = ctx.getString("fleEtag");
        if (id == null || etag == null) return;
        cf().deleteFieldLevelEncryptionConfig(r -> r.id(id).ifMatch(etag));
        ctx.set("fleId", null);
    }

    // ── cloudfront-fle-profile ───────────────────────────────────────────────

    private void teardownFleProfile(TestContext ctx) {
        String id = ctx.getString("flepId");
        if (id == null) return;
        try {
            var resp = cf().getFieldLevelEncryptionProfile(r -> r.id(id));
            final String etag = resp.eTag();
            cf().deleteFieldLevelEncryptionProfile(r -> r.id(id).ifMatch(etag));
        } catch (Exception ignored) {}
    }

    private void createFieldLevelEncryptionProfile(TestContext ctx) throws Exception {
        var profileConfig = FieldLevelEncryptionProfileConfig.builder()
                .callerReference("oc-flep-" + ctx.runId())
                .name("oc-flep-" + ctx.runId())
                .comment("compat test")
                .encryptionEntities(EncryptionEntities.builder().quantity(0).items(List.of()).build())
                .build();
        var resp = cf().createFieldLevelEncryptionProfile(r -> r.fieldLevelEncryptionProfileConfig(profileConfig));
        Assertions.assertNotBlank(resp.fieldLevelEncryptionProfile().id(), "CreateFieldLevelEncryptionProfile: id is blank");
        ctx.set("flepId",   resp.fieldLevelEncryptionProfile().id());
        ctx.set("flepEtag", resp.eTag());
    }

    private void getFieldLevelEncryptionProfile(TestContext ctx) throws Exception {
        String id = ctx.getString("flepId");
        if (id == null) throw new AssertionError("GetFieldLevelEncryptionProfile: prerequisite missing");
        var resp = cf().getFieldLevelEncryptionProfile(r -> r.id(id));
        Assertions.assertNotBlank(resp.fieldLevelEncryptionProfile().id(), "GetFieldLevelEncryptionProfile: id is blank");
    }

    private void getFieldLevelEncryptionProfileConfig(TestContext ctx) throws Exception {
        String id = ctx.getString("flepId");
        if (id == null) throw new AssertionError("GetFieldLevelEncryptionProfileConfig: prerequisite missing");
        var resp = cf().getFieldLevelEncryptionProfileConfig(r -> r.id(id));
        Assertions.assertNotNull(resp.fieldLevelEncryptionProfileConfig(), "GetFieldLevelEncryptionProfileConfig: config is null");
    }

    private void updateFieldLevelEncryptionProfile(TestContext ctx) throws Exception {
        String id   = ctx.getString("flepId");
        String etag = ctx.getString("flepEtag");
        if (id == null || etag == null) throw new AssertionError("UpdateFieldLevelEncryptionProfile: prerequisite missing");
        var profileConfig = FieldLevelEncryptionProfileConfig.builder()
                .callerReference("oc-flep-" + ctx.runId())
                .name("oc-flep-" + ctx.runId())
                .comment("compat test updated")
                .encryptionEntities(EncryptionEntities.builder().quantity(0).items(List.of()).build())
                .build();
        var resp = cf().updateFieldLevelEncryptionProfile(r -> r.id(id).ifMatch(etag).fieldLevelEncryptionProfileConfig(profileConfig));
        ctx.set("flepEtag", resp.eTag());
    }

    private void listFieldLevelEncryptionProfiles(TestContext ctx) throws Exception {
        var resp = cf().listFieldLevelEncryptionProfiles(r -> {});
        Assertions.assertNotNull(resp.fieldLevelEncryptionProfileList(), "ListFieldLevelEncryptionProfiles: list is null");
    }

    private void deleteFieldLevelEncryptionProfile(TestContext ctx) throws Exception {
        String id   = ctx.getString("flepId");
        String etag = ctx.getString("flepEtag");
        if (id == null || etag == null) return;
        cf().deleteFieldLevelEncryptionProfile(r -> r.id(id).ifMatch(etag));
        ctx.set("flepId", null);
    }

    // ── cloudfront-continuous-deployment ─────────────────────────────────────

    private void teardownCdp(TestContext ctx) {
        String id = ctx.getString("cdpId");
        if (id == null) return;
        try {
            var resp = cf().getContinuousDeploymentPolicy(r -> r.id(id));
            final String etag = resp.eTag();
            cf().deleteContinuousDeploymentPolicy(r -> r.id(id).ifMatch(etag));
        } catch (Exception ignored) {}
    }

    private void createContinuousDeploymentPolicy(TestContext ctx) throws Exception {
        var cdpConfig = ContinuousDeploymentPolicyConfig.builder()
                .stagingDistributionDnsNames(StagingDistributionDnsNames.builder()
                        .quantity(1)
                        .items("d1234.cloudfront.net")
                        .build())
                .enabled(true)
                .build();
        var resp = cf().createContinuousDeploymentPolicy(r -> r.continuousDeploymentPolicyConfig(cdpConfig));
        Assertions.assertNotBlank(resp.continuousDeploymentPolicy().id(), "CreateContinuousDeploymentPolicy: id is blank");
        ctx.set("cdpId",   resp.continuousDeploymentPolicy().id());
        ctx.set("cdpEtag", resp.eTag());
    }

    private void getContinuousDeploymentPolicy(TestContext ctx) throws Exception {
        String id = ctx.getString("cdpId");
        if (id == null) throw new AssertionError("GetContinuousDeploymentPolicy: prerequisite missing");
        var resp = cf().getContinuousDeploymentPolicy(r -> r.id(id));
        Assertions.assertNotBlank(resp.continuousDeploymentPolicy().id(), "GetContinuousDeploymentPolicy: id is blank");
    }

    private void getContinuousDeploymentPolicyConfig(TestContext ctx) throws Exception {
        String id = ctx.getString("cdpId");
        if (id == null) throw new AssertionError("GetContinuousDeploymentPolicyConfig: prerequisite missing");
        var resp = cf().getContinuousDeploymentPolicyConfig(r -> r.id(id));
        Assertions.assertNotNull(resp.continuousDeploymentPolicyConfig(), "GetContinuousDeploymentPolicyConfig: config is null");
    }

    private void updateContinuousDeploymentPolicy(TestContext ctx) throws Exception {
        String id   = ctx.getString("cdpId");
        String etag = ctx.getString("cdpEtag");
        if (id == null || etag == null) throw new AssertionError("UpdateContinuousDeploymentPolicy: prerequisite missing");
        var cdpConfig = ContinuousDeploymentPolicyConfig.builder()
                .stagingDistributionDnsNames(StagingDistributionDnsNames.builder()
                        .quantity(1)
                        .items("d5678.cloudfront.net")
                        .build())
                .enabled(false)
                .build();
        var resp = cf().updateContinuousDeploymentPolicy(r -> r.id(id).ifMatch(etag).continuousDeploymentPolicyConfig(cdpConfig));
        ctx.set("cdpEtag", resp.eTag());
    }

    private void listContinuousDeploymentPolicies(TestContext ctx) throws Exception {
        var resp = cf().listContinuousDeploymentPolicies(r -> {});
        Assertions.assertNotNull(resp.continuousDeploymentPolicyList(), "ListContinuousDeploymentPolicies: list is null");
    }

    private void deleteContinuousDeploymentPolicy(TestContext ctx) throws Exception {
        String id   = ctx.getString("cdpId");
        String etag = ctx.getString("cdpEtag");
        if (id == null || etag == null) return;
        cf().deleteContinuousDeploymentPolicy(r -> r.id(id).ifMatch(etag));
        ctx.set("cdpId", null);
    }
}
