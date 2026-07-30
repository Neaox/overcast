package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.elasticache.ElastiCacheClient;
import software.amazon.awssdk.services.elasticache.model.*;

import java.util.Map;

/**
 * ElastiCache compatibility test group.
 *
 * <p>Groups: elasticache-clusters, elasticache-modify,
 * elasticache-replication-groups, elasticache-subnet-groups,
 * elasticache-parameter-groups.
 */
public final class ElastiCacheGroup implements ServiceGroup {

    private final AwsClients clients;

    public ElastiCacheGroup(AwsClients clients) {
        this.clients = clients;
    }

    private ElastiCacheClient elasticache() { return clients.elasticache(); }

    private static int uniqCounter;
    private static String uniq(String desc) {
        return "compat-" + desc + "-" + (++uniqCounter);
    }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateCacheCluster",        this::createCacheCluster),
                Map.entry("DescribeCacheClusters",     this::describeCacheClusters),
                Map.entry("DeleteCacheCluster",        this::deleteCacheCluster),
                Map.entry("ModifyCacheCluster",        this::modifyCacheCluster),
                Map.entry("ModifyReplicationGroup",    this::modifyReplicationGroup),
                Map.entry("CreateReplicationGroup",    this::createReplicationGroup),
                Map.entry("DescribeReplicationGroups", this::describeReplicationGroups),
                Map.entry("CreateCacheSubnetGroup",    this::createCacheSubnetGroup),
                Map.entry("DescribeCacheSubnetGroups", this::describeCacheSubnetGroups),
                Map.entry("CreateCacheParameterGroup",    this::createCacheParameterGroup),
                Map.entry("DescribeCacheParameterGroups", this::describeCacheParameterGroups),
                Map.entry("DeleteCacheParameterGroup",    this::deleteCacheParameterGroup)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "elasticache-clusters",           ctx -> setupNoop(),
                "elasticache-modify",             ctx -> setupNoop(),
                "elasticache-replication-groups", ctx -> setupNoop(),
                "elasticache-subnet-groups",      ctx -> setupNoop(),
                "elasticache-parameter-groups",   ctx -> setupNoop()
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "elasticache-clusters",           this::teardownClusters,
                "elasticache-modify",             this::teardownModify,
                "elasticache-replication-groups", this::teardownReplicationGroups,
                "elasticache-subnet-groups",      this::teardownSubnetGroups,
                "elasticache-parameter-groups",   this::teardownParameterGroups
        );
    }

    private void setupNoop() {}

    // ─── elasticache-clusters ───────────────────────────────────────────────

    private void createCacheCluster(TestContext ctx) throws Exception {
        String id = uniq("cluster");
        ctx.set("_clusterId", id);
        var resp = elasticache().createCacheCluster(r -> r
                .cacheClusterId(id)
                .engine("redis")
                .cacheNodeType("cache.t3.micro")
                .numCacheNodes(1));
        Assertions.assertEquals(resp.cacheCluster().cacheClusterId(), id,
                "CreateCacheCluster: CacheClusterId mismatch");
    }

    private void describeCacheClusters(TestContext ctx) throws Exception {
        String id = uniq("describe");
        ctx.set("_describeClusterId", id);
        elasticache().createCacheCluster(r -> r
                .cacheClusterId(id)
                .engine("redis")
                .cacheNodeType("cache.t3.micro")
                .numCacheNodes(1));
        var resp = elasticache().describeCacheClusters(r -> r.cacheClusterId(id));
        Assertions.assertEquals(resp.cacheClusters().size(), 1,
                "DescribeCacheClusters: expected 1 cluster");
        Assertions.assertEquals(resp.cacheClusters().get(0).cacheClusterId(), id,
                "DescribeCacheClusters: CacheClusterId mismatch");
    }

    private void deleteCacheCluster(TestContext ctx) throws Exception {
        String id = uniq("delete");
        ctx.set("_deleteClusterId", id);
        elasticache().createCacheCluster(r -> r
                .cacheClusterId(id)
                .engine("redis")
                .cacheNodeType("cache.t3.micro")
                .numCacheNodes(1));
        var resp = elasticache().deleteCacheCluster(r -> r.cacheClusterId(id));
        Assertions.assertNotNull(resp.cacheCluster(), "DeleteCacheCluster: no CacheCluster in response");
        Assertions.assertEquals(resp.cacheCluster().cacheClusterStatus(), "deleting",
                "DeleteCacheCluster: expected status 'deleting'");
    }

    private void teardownClusters(TestContext ctx) {
        for (String key : new String[]{"_clusterId", "_describeClusterId", "_deleteClusterId"}) {
            String id = ctx.getString(key);
            if (id != null) {
                try { elasticache().deleteCacheCluster(r -> r.cacheClusterId(id)); } catch (Exception ignored) {}
            }
        }
    }

    // ─── elasticache-modify ─────────────────────────────────────────────────

    private void modifyCacheCluster(TestContext ctx) throws Exception {
        String id = uniq("modify-cluster");
        ctx.set("_modifyClusterId", id);
        elasticache().createCacheCluster(r -> r
                .cacheClusterId(id)
                .engine("redis")
                .cacheNodeType("cache.t3.micro")
                .numCacheNodes(1));
        var resp = elasticache().modifyCacheCluster(r -> r
                .cacheClusterId(id)
                .cacheNodeType("cache.t3.small")
                .applyImmediately(true));
        Assertions.assertEquals(resp.cacheCluster().cacheClusterId(), id,
                "ModifyCacheCluster: CacheClusterId mismatch");
        Assertions.assertEquals(resp.cacheCluster().cacheClusterStatus(), "modifying",
                "ModifyCacheCluster: expected status 'modifying'");
        Assertions.assertEquals(resp.cacheCluster().cacheNodeType(), "cache.t3.small",
                "ModifyCacheCluster: expected CacheNodeType 'cache.t3.small'");
    }

    private void modifyReplicationGroup(TestContext ctx) throws Exception {
        String id = uniq("modify-rg");
        ctx.set("_modifyRgId", id);
        elasticache().createReplicationGroup(r -> r
                .replicationGroupId(id)
                .replicationGroupDescription("original")
                .cacheNodeType("cache.t3.micro"));
        var resp = elasticache().modifyReplicationGroup(r -> r
                .replicationGroupId(id)
                .replicationGroupDescription("updated")
                .applyImmediately(true));
        Assertions.assertEquals(resp.replicationGroup().replicationGroupId(), id,
                "ModifyReplicationGroup: ReplicationGroupId mismatch");
        Assertions.assertEquals(resp.replicationGroup().status(), "modifying",
                "ModifyReplicationGroup: expected status 'modifying'");
        Assertions.assertEquals(resp.replicationGroup().description(), "updated",
                "ModifyReplicationGroup: expected Description 'updated'");
    }

    private void teardownModify(TestContext ctx) {
        String clusterId = ctx.getString("_modifyClusterId");
        if (clusterId != null) {
            try { elasticache().deleteCacheCluster(r -> r.cacheClusterId(clusterId)); } catch (Exception ignored) {}
        }
        String rgId = ctx.getString("_modifyRgId");
        if (rgId != null) {
            try { elasticache().deleteReplicationGroup(r -> r.replicationGroupId(rgId)); } catch (Exception ignored) {}
        }
    }

    // ─── elasticache-replication-groups ─────────────────────────────────────

    private void createReplicationGroup(TestContext ctx) throws Exception {
        String id = uniq("rg");
        ctx.set("_rgId", id);
        var resp = elasticache().createReplicationGroup(r -> r
                .replicationGroupId(id)
                .replicationGroupDescription("compat test group")
                .cacheNodeType("cache.t3.micro"));
        Assertions.assertEquals(resp.replicationGroup().replicationGroupId(), id,
                "CreateReplicationGroup: ReplicationGroupId mismatch");
    }

    private void describeReplicationGroups(TestContext ctx) throws Exception {
        String id = uniq("rg-desc");
        ctx.set("_rgDescId", id);
        elasticache().createReplicationGroup(r -> r
                .replicationGroupId(id)
                .replicationGroupDescription("compat describe")
                .cacheNodeType("cache.t3.micro"));
        var resp = elasticache().describeReplicationGroups(r -> r.replicationGroupId(id));
        Assertions.assertEquals(resp.replicationGroups().size(), 1,
                "DescribeReplicationGroups: expected 1 group");
        Assertions.assertEquals(resp.replicationGroups().get(0).replicationGroupId(), id,
                "DescribeReplicationGroups: ReplicationGroupId mismatch");
    }

    private void teardownReplicationGroups(TestContext ctx) {
        for (String key : new String[]{"_rgId", "_rgDescId"}) {
            String id = ctx.getString(key);
            if (id != null) {
                try { elasticache().deleteReplicationGroup(r -> r.replicationGroupId(id)); } catch (Exception ignored) {}
            }
        }
    }

    // ─── elasticache-subnet-groups ──────────────────────────────────────────

    private void createCacheSubnetGroup(TestContext ctx) throws Exception {
        String name = uniq("sngrp");
        ctx.set("_sngrpName", name);
        var resp = elasticache().createCacheSubnetGroup(r -> r
                .cacheSubnetGroupName(name)
                .cacheSubnetGroupDescription("compat test")
                .subnetIds("subnet-aabbccdd"));
        Assertions.assertEquals(resp.cacheSubnetGroup().cacheSubnetGroupName(), name,
                "CreateCacheSubnetGroup: CacheSubnetGroupName mismatch");
    }

    private void describeCacheSubnetGroups(TestContext ctx) throws Exception {
        String name = uniq("sngrp-desc");
        ctx.set("_sngrpDescName", name);
        elasticache().createCacheSubnetGroup(r -> r
                .cacheSubnetGroupName(name)
                .cacheSubnetGroupDescription("describe test")
                .subnetIds("subnet-11223344"));
        var resp = elasticache().describeCacheSubnetGroups(r -> r.cacheSubnetGroupName(name));
        Assertions.assertEquals(resp.cacheSubnetGroups().size(), 1,
                "DescribeCacheSubnetGroups: expected 1 group");
        Assertions.assertEquals(resp.cacheSubnetGroups().get(0).cacheSubnetGroupName(), name,
                "DescribeCacheSubnetGroups: CacheSubnetGroupName mismatch");
    }

    private void teardownSubnetGroups(TestContext ctx) {
        for (String key : new String[]{"_sngrpName", "_sngrpDescName"}) {
            String name = ctx.getString(key);
            if (name != null) {
                try { elasticache().deleteCacheSubnetGroup(r -> r.cacheSubnetGroupName(name)); } catch (Exception ignored) {}
            }
        }
    }

    // ─── elasticache-parameter-groups ───────────────────────────────────────

    private void createCacheParameterGroup(TestContext ctx) throws Exception {
        String name = uniq("pg");
        ctx.set("_pgName", name);
        var resp = elasticache().createCacheParameterGroup(r -> r
                .cacheParameterGroupName(name)
                .cacheParameterGroupFamily("redis7")
                .description("compat test group"));
        Assertions.assertEquals(resp.cacheParameterGroup().cacheParameterGroupName(), name,
                "CreateCacheParameterGroup: CacheParameterGroupName mismatch");
        Assertions.assertEquals(resp.cacheParameterGroup().cacheParameterGroupFamily(), "redis7",
                "CreateCacheParameterGroup: CacheParameterGroupFamily mismatch");
        Assertions.assertTrue(resp.cacheParameterGroup().arn().contains(name),
                "CreateCacheParameterGroup: ARN does not contain name");
    }

    private void describeCacheParameterGroups(TestContext ctx) throws Exception {
        String name = uniq("pg-desc");
        ctx.set("_pgDescName", name);
        elasticache().createCacheParameterGroup(r -> r
                .cacheParameterGroupName(name)
                .cacheParameterGroupFamily("redis7")
                .description("describe test"));
        var resp = elasticache().describeCacheParameterGroups(r -> r.cacheParameterGroupName(name));
        Assertions.assertEquals(resp.cacheParameterGroups().size(), 1,
                "DescribeCacheParameterGroups: expected 1 group");
        Assertions.assertEquals(resp.cacheParameterGroups().get(0).cacheParameterGroupName(), name,
                "DescribeCacheParameterGroups: CacheParameterGroupName mismatch");
        Assertions.assertEquals(resp.cacheParameterGroups().get(0).cacheParameterGroupFamily(), "redis7",
                "DescribeCacheParameterGroups: CacheParameterGroupFamily mismatch");
    }

    private void deleteCacheParameterGroup(TestContext ctx) throws Exception {
        String name = uniq("pg-del");
        ctx.set("_pgDelName", name);
        elasticache().createCacheParameterGroup(r -> r
                .cacheParameterGroupName(name)
                .cacheParameterGroupFamily("redis7")
                .description("delete test"));
        elasticache().deleteCacheParameterGroup(r -> r.cacheParameterGroupName(name));
        try {
            elasticache().describeCacheParameterGroups(r -> r.cacheParameterGroupName(name));
            throw new AssertionError("DeleteCacheParameterGroup: parameter group still exists after delete");
        } catch (Exception expected) {
            // Expected — group should be gone
        }
    }

    private void teardownParameterGroups(TestContext ctx) {
        for (String key : new String[]{"_pgName", "_pgDescName", "_pgDelName"}) {
            String name = ctx.getString(key);
            if (name != null) {
                try { elasticache().deleteCacheParameterGroup(r -> r.cacheParameterGroupName(name)); } catch (Exception ignored) {}
            }
        }
    }
}
