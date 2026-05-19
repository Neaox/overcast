"""
groups/elasticache.py — ElastiCache compatibility test implementations for the Python suite.

Groups:
  elasticache-clusters           — Cache cluster lifecycle (create, describe, delete)
  elasticache-modify             — Modify cache clusters and replication groups
  elasticache-replication-groups — Replication group lifecycle
  elasticache-subnet-groups      — Cache subnet group lifecycle
  elasticache-parameter-groups   — Cache parameter group lifecycle
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _elasticache(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).elasticache


# ── elasticache-clusters ──────────────────────────────────────────────────────

def teardown_elasticache_clusters(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    for key in ("_clusterId", "_describeClusterId", "_deleteClusterId"):
        cid = ctx.get(key)
        if cid:
            try:
                ec.delete_cache_cluster(CacheClusterId=cid)
            except Exception:
                pass


def CreateCacheCluster(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    cid = f"compat-cluster-{ctx.run_id}"
    ctx["_clusterId"] = cid
    resp = ec.create_cache_cluster(
        CacheClusterId=cid,
        Engine="redis",
        CacheNodeType="cache.t3.micro",
        NumCacheNodes=1,
    )
    cluster = resp.get("CacheCluster", {})
    if cluster.get("CacheClusterId") != cid:
        raise AssertionError(f"CreateCacheCluster: expected id {cid}, got {cluster.get('CacheClusterId')}")
    status = cluster.get("CacheClusterStatus")
    if status not in ("creating", "available"):
        raise AssertionError(f"CreateCacheCluster: unexpected status {status}")


def DescribeCacheClusters(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    cid = f"compat-describe-{ctx.run_id}"
    ctx["_describeClusterId"] = cid
    ec.create_cache_cluster(
        CacheClusterId=cid,
        Engine="redis",
        CacheNodeType="cache.t3.micro",
        NumCacheNodes=1,
    )
    resp = ec.describe_cache_clusters(CacheClusterId=cid)
    clusters = resp.get("CacheClusters", [])
    if len(clusters) != 1:
        raise AssertionError(f"DescribeCacheClusters: expected 1 cluster, got {len(clusters)}")
    if clusters[0].get("CacheClusterId") != cid:
        raise AssertionError(f"DescribeCacheClusters: expected id {cid}, got {clusters[0].get('CacheClusterId')}")


def DeleteCacheCluster(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    cid = f"compat-delete-{ctx.run_id}"
    ctx["_deleteClusterId"] = cid
    ec.create_cache_cluster(
        CacheClusterId=cid,
        Engine="redis",
        CacheNodeType="cache.t3.micro",
        NumCacheNodes=1,
    )
    resp = ec.delete_cache_cluster(CacheClusterId=cid)
    cluster = resp.get("CacheCluster", {})
    if cluster.get("CacheClusterStatus") != "deleting":
        raise AssertionError(f"DeleteCacheCluster: expected status deleting, got {cluster.get('CacheClusterStatus')}")


# ── elasticache-modify ────────────────────────────────────────────────────────

def teardown_elasticache_modify(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    for key in ("_modifyClusterId", "_modifyRgId"):
        rid = ctx.get(key)
        if not rid:
            continue
        try:
            if key == "_modifyClusterId":
                ec.delete_cache_cluster(CacheClusterId=rid)
            else:
                ec.delete_replication_group(ReplicationGroupId=rid)
        except Exception:
            pass


def ModifyCacheCluster(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    cid = f"compat-modify-cluster-{ctx.run_id}"
    ctx["_modifyClusterId"] = cid
    ec.create_cache_cluster(
        CacheClusterId=cid,
        Engine="redis",
        CacheNodeType="cache.t3.micro",
        NumCacheNodes=1,
    )
    resp = ec.modify_cache_cluster(
        CacheClusterId=cid,
        CacheNodeType="cache.t3.small",
        ApplyImmediately=True,
    )
    cluster = resp.get("CacheCluster", {})
    if cluster.get("CacheClusterId") != cid:
        raise AssertionError(f"ModifyCacheCluster: expected id {cid}, got {cluster.get('CacheClusterId')}")
    if cluster.get("CacheClusterStatus") != "modifying":
        raise AssertionError(f"ModifyCacheCluster: expected status modifying, got {cluster.get('CacheClusterStatus')}")
    if cluster.get("CacheNodeType") != "cache.t3.small":
        raise AssertionError(f"ModifyCacheCluster: expected type cache.t3.small, got {cluster.get('CacheNodeType')}")


def ModifyReplicationGroup(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    rgid = f"compat-modify-rg-{ctx.run_id}"
    ctx["_modifyRgId"] = rgid
    ec.create_replication_group(
        ReplicationGroupId=rgid,
        ReplicationGroupDescription="original",
        CacheNodeType="cache.t3.micro",
    )
    resp = ec.modify_replication_group(
        ReplicationGroupId=rgid,
        ReplicationGroupDescription="updated",
        ApplyImmediately=True,
    )
    rg = resp.get("ReplicationGroup", {})
    if rg.get("ReplicationGroupId") != rgid:
        raise AssertionError(f"ModifyReplicationGroup: expected id {rgid}, got {rg.get('ReplicationGroupId')}")
    if rg.get("Status") != "modifying":
        raise AssertionError(f"ModifyReplicationGroup: expected status modifying, got {rg.get('Status')}")
    if rg.get("Description") != "updated":
        raise AssertionError(f"ModifyReplicationGroup: expected desc updated, got {rg.get('Description')}")


# ── elasticache-replication-groups ────────────────────────────────────────────

def teardown_elasticache_replication_groups(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    for key in ("_rgId", "_rgDescId"):
        rgid = ctx.get(key)
        if rgid:
            try:
                ec.delete_replication_group(ReplicationGroupId=rgid)
            except Exception:
                pass


def CreateReplicationGroup(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    rgid = f"compat-rg-{ctx.run_id}"
    ctx["_rgId"] = rgid
    resp = ec.create_replication_group(
        ReplicationGroupId=rgid,
        ReplicationGroupDescription="compat test group",
        CacheNodeType="cache.t3.micro",
    )
    rg = resp.get("ReplicationGroup", {})
    if rg.get("ReplicationGroupId") != rgid:
        raise AssertionError(f"CreateReplicationGroup: expected id {rgid}, got {rg.get('ReplicationGroupId')}")
    status = rg.get("Status")
    if status not in ("creating", "available"):
        raise AssertionError(f"CreateReplicationGroup: unexpected status {status}")


def DescribeReplicationGroups(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    rgid = f"compat-rg-desc-{ctx.run_id}"
    ctx["_rgDescId"] = rgid
    ec.create_replication_group(
        ReplicationGroupId=rgid,
        ReplicationGroupDescription="compat describe",
        CacheNodeType="cache.t3.micro",
    )
    resp = ec.describe_replication_groups(ReplicationGroupId=rgid)
    groups = resp.get("ReplicationGroups", [])
    if len(groups) != 1:
        raise AssertionError(f"DescribeReplicationGroups: expected 1 group, got {len(groups)}")
    if groups[0].get("ReplicationGroupId") != rgid:
        raise AssertionError(f"DescribeReplicationGroups: expected id {rgid}, got {groups[0].get('ReplicationGroupId')}")


# ── elasticache-subnet-groups ─────────────────────────────────────────────────

def teardown_elasticache_subnet_groups(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    for key in ("_sngrpName", "_sngrpDescName"):
        name = ctx.get(key)
        if name:
            try:
                ec.delete_cache_subnet_group(CacheSubnetGroupName=name)
            except Exception:
                pass


def CreateCacheSubnetGroup(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    name = f"compat-sngrp-{ctx.run_id}"
    ctx["_sngrpName"] = name
    resp = ec.create_cache_subnet_group(
        CacheSubnetGroupName=name,
        CacheSubnetGroupDescription="compat test",
        SubnetIds=["subnet-aabbccdd"],
    )
    sngrp = resp.get("CacheSubnetGroup", {})
    if sngrp.get("CacheSubnetGroupName") != name:
        raise AssertionError(f"CreateCacheSubnetGroup: expected name {name}, got {sngrp.get('CacheSubnetGroupName')}")


def DescribeCacheSubnetGroups(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    name = f"compat-sngrp-desc-{ctx.run_id}"
    ctx["_sngrpDescName"] = name
    ec.create_cache_subnet_group(
        CacheSubnetGroupName=name,
        CacheSubnetGroupDescription="describe test",
        SubnetIds=["subnet-11223344"],
    )
    resp = ec.describe_cache_subnet_groups(CacheSubnetGroupName=name)
    groups = resp.get("CacheSubnetGroups", [])
    if len(groups) != 1:
        raise AssertionError(f"DescribeCacheSubnetGroups: expected 1 group, got {len(groups)}")
    if groups[0].get("CacheSubnetGroupName") != name:
        raise AssertionError(f"DescribeCacheSubnetGroups: expected name {name}, got {groups[0].get('CacheSubnetGroupName')}")


# ── elasticache-parameter-groups ──────────────────────────────────────────────

def teardown_elasticache_parameter_groups(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    for key in ("_pgName", "_pgDescName", "_pgDelName"):
        name = ctx.get(key)
        if name:
            try:
                ec.delete_cache_parameter_group(CacheParameterGroupName=name)
            except Exception:
                pass


def CreateCacheParameterGroup(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    name = f"compat-pg-{ctx.run_id}"
    ctx["_pgName"] = name
    resp = ec.create_cache_parameter_group(
        CacheParameterGroupName=name,
        CacheParameterGroupFamily="redis7",
        Description="compat test group",
    )
    pg = resp.get("CacheParameterGroup", {})
    if pg.get("CacheParameterGroupName") != name:
        raise AssertionError(f"CreateCacheParameterGroup: expected name {name}, got {pg.get('CacheParameterGroupName')}")
    if pg.get("CacheParameterGroupFamily") != "redis7":
        raise AssertionError(f"CreateCacheParameterGroup: expected family redis7, got {pg.get('CacheParameterGroupFamily')}")
    arn = pg.get("ARN", "")
    if name not in arn:
        raise AssertionError(f"CreateCacheParameterGroup: ARN {arn!r} does not contain {name}")


def DescribeCacheParameterGroups(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    name = f"compat-pg-desc-{ctx.run_id}"
    ctx["_pgDescName"] = name
    ec.create_cache_parameter_group(
        CacheParameterGroupName=name,
        CacheParameterGroupFamily="redis7",
        Description="describe test",
    )
    resp = ec.describe_cache_parameter_groups(CacheParameterGroupName=name)
    groups = resp.get("CacheParameterGroups", [])
    if len(groups) != 1:
        raise AssertionError(f"DescribeCacheParameterGroups: expected 1 group, got {len(groups)}")
    if groups[0].get("CacheParameterGroupName") != name:
        raise AssertionError(f"DescribeCacheParameterGroups: expected name {name}, got {groups[0].get('CacheParameterGroupName')}")
    if groups[0].get("CacheParameterGroupFamily") != "redis7":
        raise AssertionError(f"DescribeCacheParameterGroups: expected family redis7, got {groups[0].get('CacheParameterGroupFamily')}")


def DeleteCacheParameterGroup(ctx: TestContext) -> None:
    ec = _elasticache(ctx)
    name = f"compat-pg-del-{ctx.run_id}"
    ctx["_pgDelName"] = name
    ec.create_cache_parameter_group(
        CacheParameterGroupName=name,
        CacheParameterGroupFamily="redis7",
        Description="delete test",
    )
    ec.delete_cache_parameter_group(CacheParameterGroupName=name)
    try:
        ec.describe_cache_parameter_groups(CacheParameterGroupName=name)
        raise AssertionError("DeleteCacheParameterGroup: group still exists after deletion")
    except Exception as e:
        if "CacheParameterGroupNotFound" not in str(e):
            raise AssertionError(f"DeleteCacheParameterGroup: unexpected error: {e}")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateCacheCluster": CreateCacheCluster,
    "DescribeCacheClusters": DescribeCacheClusters,
    "DeleteCacheCluster": DeleteCacheCluster,
    "ModifyCacheCluster": ModifyCacheCluster,
    "ModifyReplicationGroup": ModifyReplicationGroup,
    "CreateReplicationGroup": CreateReplicationGroup,
    "DescribeReplicationGroups": DescribeReplicationGroups,
    "CreateCacheSubnetGroup": CreateCacheSubnetGroup,
    "DescribeCacheSubnetGroups": DescribeCacheSubnetGroups,
    "CreateCacheParameterGroup": CreateCacheParameterGroup,
    "DescribeCacheParameterGroups": DescribeCacheParameterGroups,
    "DeleteCacheParameterGroup": DeleteCacheParameterGroup,
}

SETUP = {}

TEARDOWN = {
    "elasticache-clusters": teardown_elasticache_clusters,
    "elasticache-modify": teardown_elasticache_modify,
    "elasticache-replication-groups": teardown_elasticache_replication_groups,
    "elasticache-subnet-groups": teardown_elasticache_subnet_groups,
    "elasticache-parameter-groups": teardown_elasticache_parameter_groups,
}
