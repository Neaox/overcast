"""
groups/rds.py — RDS compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import time
from lib.harness import TestContext
from lib.clients import make_clients


def _rds(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("rds")


# How long a DB instance is given to reach a target status, and how often it is
# asked. Shared with the go, node, java and cli suites, which wait for the same
# thing against the same emulator.
#
# A wall-clock budget rather than a count of attempts, because those two stop
# meaning the same thing the moment the machine is busy: "60 attempts, 0.5s
# apart" is 30 seconds only while every describe_db_instances returns instantly.
# What is being waited for is a real database container's first boot, data
# directory initialisation included, and 30 seconds does not cover that on a
# loaded machine — the go, node and java suites all failed here during a release
# run with five suites in flight, and passed run alone. The CLI suite already
# carried 120 seconds for exactly this reason; this is that number, made common.
RDS_WAIT_BUDGET_SECONDS = 120.0
RDS_POLL_INTERVAL_SECONDS = 1.0


def _wait_for_status(
    rds, db_id: str, target: str, budget: float = RDS_WAIT_BUDGET_SECONDS
) -> None:
    """Poll until the DB instance reaches *target* status, or raise once the budget is spent."""
    deadline = time.monotonic() + budget
    last = "(none reported)"
    while True:
        resp = rds.describe_db_instances(DBInstanceIdentifier=db_id)
        status = resp.get("DBInstances", [{}])[0].get("DBInstanceStatus", "")
        if status:
            last = status
            if status == target:
                return
            # A failed instance will never reach any target, so spending the
            # rest of the budget on it only delays the report.
            if status == "failed":
                raise AssertionError(
                    f"DB instance {db_id} went to 'failed' while waiting for '{target}'"
                )
        if time.monotonic() >= deadline:
            raise AssertionError(
                f"DB instance {db_id} did not reach '{target}' within {budget:.0f}s "
                f"(last status '{last}')"
            )
        time.sleep(RDS_POLL_INTERVAL_SECONDS)


# ── rds-instances ─────────────────────────────────────────────────────────────

def DescribeDBEngineVersions(ctx: TestContext) -> None:
    _rds(ctx).describe_db_engine_versions()


def CreateDBInstance(ctx: TestContext) -> None:
    rds = _rds(ctx)
    db_id = f"compat-{ctx.run_id}"
    resp = rds.create_db_instance(
        DBInstanceIdentifier=db_id,
        DBInstanceClass="db.t3.micro",
        Engine="mysql",
        MasterUsername="admin",
        MasterUserPassword="Password1!",
        AllocatedStorage=20,
    )
    db = resp.get("DBInstance", {})
    if not db.get("DBInstanceIdentifier"):
        raise AssertionError("CreateDBInstance: missing DBInstanceIdentifier")
    ctx["rds_db_id"] = db_id


def DescribeDBInstances(ctx: TestContext) -> None:
    resp = _rds(ctx).describe_db_instances()
    if resp.get("DBInstances") is None:
        raise AssertionError("DescribeDBInstances: missing DBInstances")


def DeleteDBInstance(ctx: TestContext) -> None:
    rds = _rds(ctx)
    db_id = ctx.get("rds_db_id")
    if not db_id:
        return
    rds.delete_db_instance(
        DBInstanceIdentifier=db_id,
        SkipFinalSnapshot=True,
    )


def StopDBInstance(ctx: TestContext) -> None:
    rds = _rds(ctx)
    db_id = ctx.get("rds_db_id")
    if not db_id:
        raise AssertionError("StopDBInstance: no db_id from CreateDBInstance")
    _wait_for_status(rds, db_id, "available")
    resp = rds.stop_db_instance(DBInstanceIdentifier=db_id)
    db = resp.get("DBInstance", {})
    if not db.get("DBInstanceStatus"):
        raise AssertionError("StopDBInstance: missing DBInstanceStatus")


def StartDBInstance(ctx: TestContext) -> None:
    rds = _rds(ctx)
    db_id = ctx.get("rds_db_id")
    if not db_id:
        raise AssertionError("StartDBInstance: no db_id from CreateDBInstance")
    _wait_for_status(rds, db_id, "stopped")
    resp = rds.start_db_instance(DBInstanceIdentifier=db_id)
    db = resp.get("DBInstance", {})
    if not db.get("DBInstanceStatus"):
        raise AssertionError("StartDBInstance: missing DBInstanceStatus")


def ModifyDBInstance(ctx: TestContext) -> None:
    rds = _rds(ctx)
    db_id = ctx.get("rds_db_id")
    if not db_id:
        raise AssertionError("ModifyDBInstance: no db_id from CreateDBInstance")
    resp = rds.modify_db_instance(
        DBInstanceIdentifier=db_id,
        AllocatedStorage=30,
    )
    db = resp.get("DBInstance", {})
    if not db.get("DBInstanceIdentifier"):
        raise AssertionError("ModifyDBInstance: missing DBInstanceIdentifier")


# ── rds-subnet-groups ────────────────────────────────────────────────────────

def CreateDBSubnetGroup(ctx: TestContext) -> None:
    rds = _rds(ctx)
    name = f"compat-{ctx.run_id}"
    resp = rds.create_db_subnet_group(
        DBSubnetGroupName=name,
        DBSubnetGroupDescription="compat test subnet group",
        SubnetIds=["subnet-00000000", "subnet-00000001"],
    )
    sg = resp.get("DBSubnetGroup", {})
    if not sg.get("DBSubnetGroupName"):
        raise AssertionError("CreateDBSubnetGroup: missing DBSubnetGroupName")
    ctx["rds_subnet_group"] = name


def DescribeDBSubnetGroups(ctx: TestContext) -> None:
    rds = _rds(ctx)
    name = ctx.get("rds_subnet_group")
    if not name:
        raise AssertionError("DescribeDBSubnetGroups: no group from CreateDBSubnetGroup")
    resp = rds.describe_db_subnet_groups(DBSubnetGroupName=name)
    if not resp.get("DBSubnetGroups"):
        raise AssertionError("DescribeDBSubnetGroups: no groups returned")


def DeleteDBSubnetGroup(ctx: TestContext) -> None:
    rds = _rds(ctx)
    name = ctx.get("rds_subnet_group")
    if not name:
        return
    rds.delete_db_subnet_group(DBSubnetGroupName=name)


# ── rds-parameter-groups ─────────────────────────────────────────────────────

def CreateDBParameterGroup(ctx: TestContext) -> None:
    rds = _rds(ctx)
    name = f"compat-pg-{ctx.run_id}"
    resp = rds.create_db_parameter_group(
        DBParameterGroupName=name,
        DBParameterGroupFamily="aurora-mysql8.0",
        Description="compat test parameter group",
    )
    pg = resp.get("DBParameterGroup", {})
    if not pg.get("DBParameterGroupName"):
        raise AssertionError("CreateDBParameterGroup: missing DBParameterGroupName")
    if pg.get("DBParameterGroupFamily") != "aurora-mysql8.0":
        raise AssertionError(
            "CreateDBParameterGroup: family mismatch: "
            f"{pg.get('DBParameterGroupFamily')!r}"
        )
    ctx["rds_param_group"] = name


def DescribeDBParameterGroups(ctx: TestContext) -> None:
    rds = _rds(ctx)
    name = ctx.get("rds_param_group")
    if not name:
        raise AssertionError("DescribeDBParameterGroups: no group from CreateDBParameterGroup")
    resp = rds.describe_db_parameter_groups(DBParameterGroupName=name)
    if not resp.get("DBParameterGroups"):
        raise AssertionError("DescribeDBParameterGroups: no groups returned")


def DeleteDBParameterGroup(ctx: TestContext) -> None:
    rds = _rds(ctx)
    name = ctx.get("rds_param_group")
    if not name:
        return
    rds.delete_db_parameter_group(DBParameterGroupName=name)


# ── rds-clusters ─────────────────────────────────────────────────────────────
#
# Aurora DB clusters. A cluster is a logical record in Overcast — member
# instances are what start engine containers — so the waits here are for the
# emulator's own status transitions rather than for a database to boot. They
# still have to be waited for: StopDBCluster refuses a cluster that is not
# "available" and StartDBCluster refuses one that is not "stopped", here and on
# AWS, so going straight from create to stop asks for InvalidDBClusterStateFault.
#
# The poll interval is tighter than the instance one for the same reason: there
# is no container boot to wait on, only a scheduled transition, so a coarser
# interval would spend seconds observing something that had already happened.
RDS_CLUSTER_POLL_INTERVAL_SECONDS = 0.5

RDS_CLUSTER_ENGINE = "aurora-mysql"
RDS_CLUSTER_MASTER_USERNAME = "admin"
RDS_CLUSTER_MASTER_PASSWORD = "Password1!"
# The retention period ModifyDBCluster sets. Non-zero so it is distinguishable
# from the create-time default, and echoed back by DescribeDBClusters so the
# change can be observed rather than assumed.
RDS_CLUSTER_BACKUP_RETENTION = 7


def _describe_cluster(rds, cluster_id: str) -> dict:
    """Return this group's cluster, or raise naming what came back instead.

    Every assertion below reads through it, so a describe that comes back empty
    reports as a missing cluster rather than an IndexError.
    """
    resp = rds.describe_db_clusters(DBClusterIdentifier=cluster_id)
    clusters = resp.get("DBClusters") or []
    if not clusters:
        raise AssertionError(
            f"DescribeDBClusters: no cluster returned for {cluster_id}"
        )
    return clusters[0]


def _wait_for_cluster_status(
    rds, cluster_id: str, target: str, budget: float = RDS_WAIT_BUDGET_SECONDS
) -> None:
    """Poll until the DB cluster reaches *target* status, or raise once the budget is spent."""
    deadline = time.monotonic() + budget
    last = "(none reported)"
    while True:
        status = _describe_cluster(rds, cluster_id).get("Status", "")
        if status:
            last = status
            if status == target:
                return
        if time.monotonic() >= deadline:
            raise AssertionError(
                f"DB cluster {cluster_id} did not reach '{target}' within {budget:.0f}s "
                f"(last status '{last}')"
            )
        time.sleep(RDS_CLUSTER_POLL_INTERVAL_SECONDS)


def _wait_for_cluster_gone(
    rds, cluster_id: str, budget: float = RDS_WAIT_BUDGET_SECONDS
) -> None:
    """Poll the unfiltered DescribeDBClusters until this run's cluster is gone.

    Deletion is asynchronous — the call marks the cluster "deleting" and the
    record goes shortly after — so absence is what has to be waited for. The
    unfiltered form rather than a lookup by identifier, so the check does not
    depend on which not-found shape botocore raises.
    """
    deadline = time.monotonic() + budget
    while True:
        resp = rds.describe_db_clusters()
        found = any(
            c.get("DBClusterIdentifier") == cluster_id
            for c in resp.get("DBClusters") or []
        )
        if not found:
            return
        if time.monotonic() >= deadline:
            raise AssertionError(
                f"DB cluster {cluster_id} still listed {budget:.0f}s after DeleteDBCluster"
            )
        time.sleep(RDS_CLUSTER_POLL_INTERVAL_SECONDS)


def CreateDBCluster(ctx: TestContext) -> None:
    rds = _rds(ctx)
    # Named apart from the instance group's resource so the two never collide
    # when both run against the same emulator.
    cluster_id = f"compat-cluster-{ctx.run_id}"
    # Recorded before the call, not after it: a create that fails partway still
    # leaves something for teardown to remove.
    ctx["rds_cluster_id"] = cluster_id
    resp = rds.create_db_cluster(
        DBClusterIdentifier=cluster_id,
        Engine=RDS_CLUSTER_ENGINE,
        MasterUsername=RDS_CLUSTER_MASTER_USERNAME,
        MasterUserPassword=RDS_CLUSTER_MASTER_PASSWORD,
    )
    cluster = resp.get("DBCluster", {})
    if cluster.get("DBClusterIdentifier") != cluster_id:
        raise AssertionError(
            "CreateDBCluster: DBClusterIdentifier = "
            f"{cluster.get('DBClusterIdentifier')!r}, want {cluster_id!r}"
        )
    if not cluster.get("DBClusterArn"):
        raise AssertionError("CreateDBCluster: missing DBClusterArn")


def DescribeDBClusters(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_cluster_id")
    if not cluster_id:
        raise AssertionError("DescribeDBClusters: no cluster from CreateDBCluster")
    cluster = _describe_cluster(rds, cluster_id)
    if cluster.get("DBClusterIdentifier") != cluster_id:
        raise AssertionError(
            "DescribeDBClusters: DBClusterIdentifier = "
            f"{cluster.get('DBClusterIdentifier')!r}, want {cluster_id!r}"
        )
    if cluster.get("Engine") != RDS_CLUSTER_ENGINE:
        raise AssertionError(
            f"DescribeDBClusters: Engine = {cluster.get('Engine')!r}, "
            f"want {RDS_CLUSTER_ENGINE!r}"
        )
    if not cluster.get("Status"):
        raise AssertionError("DescribeDBClusters: missing Status")
    # The writer and reader endpoints are what makes a cluster a cluster, and
    # nothing outside the emulator's own Go tests looked at them until this
    # group existed. Both must be present and they must differ — a reader
    # endpoint collapsed onto the writer reads as success everywhere else.
    # Both stay distinct only while the cluster has no writer member: since
    # #1076 a host caller that cannot resolve the names is given the loopback
    # address for each, deliberately. This group creates no members, so it
    # never reaches that case — one that adds an instance would.
    writer = cluster.get("Endpoint")
    reader = cluster.get("ReaderEndpoint")
    if not writer:
        raise AssertionError("DescribeDBClusters: missing Endpoint")
    if not reader:
        raise AssertionError("DescribeDBClusters: missing ReaderEndpoint")
    if writer == reader:
        raise AssertionError(
            f"DescribeDBClusters: Endpoint and ReaderEndpoint are both {writer!r}"
        )


def ModifyDBCluster(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_cluster_id")
    if not cluster_id:
        raise AssertionError("ModifyDBCluster: no cluster from CreateDBCluster")
    resp = rds.modify_db_cluster(
        DBClusterIdentifier=cluster_id,
        BackupRetentionPeriod=RDS_CLUSTER_BACKUP_RETENTION,
    )
    cluster = resp.get("DBCluster", {})
    if cluster.get("BackupRetentionPeriod") != RDS_CLUSTER_BACKUP_RETENTION:
        raise AssertionError(
            "ModifyDBCluster: BackupRetentionPeriod = "
            f"{cluster.get('BackupRetentionPeriod')!r}, "
            f"want {RDS_CLUSTER_BACKUP_RETENTION}"
        )
    # Read it back rather than trusting the modify response to have echoed a
    # value it never stored.
    after = _describe_cluster(rds, cluster_id)
    if after.get("BackupRetentionPeriod") != RDS_CLUSTER_BACKUP_RETENTION:
        raise AssertionError(
            "ModifyDBCluster: BackupRetentionPeriod after describe = "
            f"{after.get('BackupRetentionPeriod')!r}, "
            f"want {RDS_CLUSTER_BACKUP_RETENTION}"
        )


def StopDBCluster(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_cluster_id")
    if not cluster_id:
        raise AssertionError("StopDBCluster: no cluster from CreateDBCluster")
    # StopDBCluster requires an available cluster, here and on AWS.
    _wait_for_cluster_status(rds, cluster_id, "available")
    resp = rds.stop_db_cluster(DBClusterIdentifier=cluster_id)
    if not resp.get("DBCluster", {}).get("Status"):
        raise AssertionError("StopDBCluster: missing Status")
    # The stop is this test's mutation, so this test is where it is confirmed to
    # have landed — not StartDBCluster's precondition wait.
    _wait_for_cluster_status(rds, cluster_id, "stopped")


def StartDBCluster(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_cluster_id")
    if not cluster_id:
        raise AssertionError("StartDBCluster: no cluster from CreateDBCluster")
    _wait_for_cluster_status(rds, cluster_id, "stopped")
    resp = rds.start_db_cluster(DBClusterIdentifier=cluster_id)
    if not resp.get("DBCluster", {}).get("Status"):
        raise AssertionError("StartDBCluster: missing Status")
    _wait_for_cluster_status(rds, cluster_id, "available")


def DeleteDBCluster(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_cluster_id")
    if not cluster_id:
        raise AssertionError("DeleteDBCluster: no cluster from CreateDBCluster")
    rds.delete_db_cluster(
        DBClusterIdentifier=cluster_id,
        SkipFinalSnapshot=True,
    )
    _wait_for_cluster_gone(rds, cluster_id)
    ctx["rds_cluster_id"] = ""



# ── rds-cluster-members ──────────────────────────────────────────────────────
#
# An Aurora cluster with an instance in it. rds-clusters deliberately has none,
# which leaves two things nothing outside the emulator's own Go tests looks at:
# DBClusterMembers, and the settings a member is supposed to take from its
# cluster rather than from its own request.
#
# Kept apart from rds-clusters rather than bolted onto it: a member changes what
# the cluster endpoints mean — with a writer on record they can collapse onto one
# loopback address for a host caller, which is deliberate and would break that
# group's "the two endpoints differ" assertion — and a second group declaring
# CreateDBCluster would make the bare impl key for it ambiguous in every suite,
# which aborts the run.

RDS_MEMBER_ENGINE = "aurora-mysql"
RDS_MEMBER_USERNAME = "clusteradmin"
RDS_MEMBER_PASSWORD = "Password1!"
RDS_MEMBER_CLASS = "db.t3.micro"


def _cluster_member_ids(cluster: dict) -> list:
    return [
        m.get("DBInstanceIdentifier")
        for m in cluster.get("DBClusterMembers") or []
    ]


def CreateClusterForMembers(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = f"compat-members-{ctx.run_id}"
    ctx["rds_member_cluster"] = cluster_id
    resp = rds.create_db_cluster(
        DBClusterIdentifier=cluster_id,
        Engine=RDS_MEMBER_ENGINE,
        MasterUsername=RDS_MEMBER_USERNAME,
        MasterUserPassword=RDS_MEMBER_PASSWORD,
    )
    cluster = resp.get("DBCluster", {})
    if not cluster.get("DBClusterArn"):
        raise AssertionError("CreateClusterForMembers: missing DBClusterArn")
    # The engine version the cluster settled on is what the member must inherit,
    # so it is recorded here rather than assumed.
    ctx["rds_member_cluster_engine_version"] = cluster.get("EngineVersion", "")


def AddClusterMember(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_member_cluster")
    if not cluster_id:
        raise AssertionError("AddClusterMember: no cluster from CreateClusterForMembers")
    instance_id = f"compat-member-{ctx.run_id}"
    ctx["rds_member_instance"] = instance_id
    # No MasterUsername or MasterUserPassword: an Aurora member takes both from
    # its cluster, and AWS documents them as not applying here.
    resp = rds.create_db_instance(
        DBInstanceIdentifier=instance_id,
        DBClusterIdentifier=cluster_id,
        Engine=RDS_MEMBER_ENGINE,
        DBInstanceClass=RDS_MEMBER_CLASS,
    )
    db = resp.get("DBInstance", {})
    if db.get("DBInstanceIdentifier") != instance_id:
        raise AssertionError(
            f"AddClusterMember: DBInstanceIdentifier = {db.get('DBInstanceIdentifier')!r}, "
            f"want {instance_id!r}"
        )
    if db.get("DBClusterIdentifier") != cluster_id:
        raise AssertionError(
            f"AddClusterMember: DBClusterIdentifier = {db.get('DBClusterIdentifier')!r}, "
            f"want {cluster_id!r}"
        )


def DescribeClusterMembers(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_member_cluster")
    instance_id = ctx.get("rds_member_instance")
    if not cluster_id or not instance_id:
        raise AssertionError("DescribeClusterMembers: no cluster or member from the earlier tests")
    cluster = _describe_cluster(rds, cluster_id)
    members = cluster.get("DBClusterMembers") or []
    if not members:
        raise AssertionError(
            f"DescribeClusterMembers: DBClusterMembers is empty after adding {instance_id}"
        )
    entry = next(
        (m for m in members if m.get("DBInstanceIdentifier") == instance_id), None
    )
    if entry is None:
        raise AssertionError(
            f"DescribeClusterMembers: {instance_id} is not among the listed members "
            f"{_cluster_member_ids(cluster)}"
        )
    # The first member of a cluster is its writer. A membership recorded with no
    # writer is the shape that leaves the cluster endpoints pointing at nothing.
    if not entry.get("IsClusterWriter"):
        raise AssertionError(
            f"DescribeClusterMembers: {instance_id} is listed but IsClusterWriter is false, "
            "and it is the only member"
        )


def DescribeMemberInheritedSettings(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_member_cluster")
    instance_id = ctx.get("rds_member_instance")
    if not instance_id:
        raise AssertionError("DescribeMemberInheritedSettings: no member from AddClusterMember")
    resp = rds.describe_db_instances(DBInstanceIdentifier=instance_id)
    instances = resp.get("DBInstances") or []
    if not instances:
        raise AssertionError(
            f"DescribeMemberInheritedSettings: no instance returned for {instance_id}"
        )
    member = instances[0]

    # The member never sent a master username; it must be the cluster's.
    if member.get("MasterUsername") != RDS_MEMBER_USERNAME:
        raise AssertionError(
            f"DescribeMemberInheritedSettings: MasterUsername = {member.get('MasterUsername')!r}, "
            f"want the cluster's {RDS_MEMBER_USERNAME!r}"
        )
    if member.get("DBClusterIdentifier") != cluster_id:
        raise AssertionError(
            "DescribeMemberInheritedSettings: DBClusterIdentifier = "
            f"{member.get('DBClusterIdentifier')!r}, want {cluster_id!r}"
        )
    want_version = ctx.get("rds_member_cluster_engine_version")
    if want_version and member.get("EngineVersion") != want_version:
        raise AssertionError(
            f"DescribeMemberInheritedSettings: EngineVersion = {member.get('EngineVersion')!r}, "
            f"want the cluster's {want_version!r}"
        )
    # Port is deliberately not asserted: Overcast answers with a port the caller
    # can actually dial, which differs between a host and a sibling container, so
    # any fixed expectation here would be wrong for one of them.


def RemoveClusterMember(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_member_cluster")
    instance_id = ctx.get("rds_member_instance")
    if not cluster_id or not instance_id:
        raise AssertionError("RemoveClusterMember: no cluster or member from the earlier tests")
    rds.delete_db_instance(DBInstanceIdentifier=instance_id, SkipFinalSnapshot=True)

    # Deleting the instance must take it out of the cluster too. It did not until
    # recently: the record went and the membership entry stayed, so
    # DescribeDBClusters kept reporting a member that no longer existed.
    deadline = time.monotonic() + RDS_WAIT_BUDGET_SECONDS
    while True:
        cluster = _describe_cluster(rds, cluster_id)
        if instance_id not in _cluster_member_ids(cluster):
            ctx["rds_member_instance"] = ""
            return
        if time.monotonic() >= deadline:
            raise AssertionError(
                f"DB instance {instance_id} was still listed in {cluster_id}'s DBClusterMembers "
                f"{RDS_WAIT_BUDGET_SECONDS:.0f}s after DeleteDBInstance"
            )
        time.sleep(RDS_CLUSTER_POLL_INTERVAL_SECONDS)


def DeleteClusterAfterMembers(ctx: TestContext) -> None:
    rds = _rds(ctx)
    cluster_id = ctx.get("rds_member_cluster")
    if not cluster_id:
        raise AssertionError("DeleteClusterAfterMembers: no cluster from CreateClusterForMembers")
    rds.delete_db_cluster(DBClusterIdentifier=cluster_id, SkipFinalSnapshot=True)
    _wait_for_cluster_gone(rds, cluster_id)
    ctx["rds_member_cluster"] = ""


def _teardown_cluster_members(ctx: TestContext) -> None:
    # Reverse creation order: the member owns a container, the cluster owns the
    # member.
    instance_id = ctx.get("rds_member_instance")
    if instance_id:
        try:
            _rds(ctx).delete_db_instance(
                DBInstanceIdentifier=instance_id, SkipFinalSnapshot=True
            )
        except Exception:
            pass
    cluster_id = ctx.get("rds_member_cluster")
    if cluster_id:
        try:
            _rds(ctx).delete_db_cluster(
                DBClusterIdentifier=cluster_id, SkipFinalSnapshot=True
            )
        except Exception:
            pass

# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "DescribeDBEngineVersions": DescribeDBEngineVersions,
    "CreateDBInstance": CreateDBInstance,
    "DescribeDBInstances": DescribeDBInstances,
    "StopDBInstance": StopDBInstance,
    "StartDBInstance": StartDBInstance,
    "ModifyDBInstance": ModifyDBInstance,
    "DeleteDBInstance": DeleteDBInstance,
    "CreateDBSubnetGroup": CreateDBSubnetGroup,
    "DescribeDBSubnetGroups": DescribeDBSubnetGroups,
    "DeleteDBSubnetGroup": DeleteDBSubnetGroup,
    "CreateDBParameterGroup": CreateDBParameterGroup,
    "DescribeDBParameterGroups": DescribeDBParameterGroups,
    "DeleteDBParameterGroup": DeleteDBParameterGroup,
    "CreateDBCluster": CreateDBCluster,
    "DescribeDBClusters": DescribeDBClusters,
    "ModifyDBCluster": ModifyDBCluster,
    "StopDBCluster": StopDBCluster,
    "StartDBCluster": StartDBCluster,
    "DeleteDBCluster": DeleteDBCluster,
    "CreateClusterForMembers": CreateClusterForMembers,
    "AddClusterMember": AddClusterMember,
    "DescribeClusterMembers": DescribeClusterMembers,
    "DescribeMemberInheritedSettings": DescribeMemberInheritedSettings,
    "RemoveClusterMember": RemoveClusterMember,
    "DeleteClusterAfterMembers": DeleteClusterAfterMembers,
}

SETUP = {}
TEARDOWN = {
    "rds-instances": lambda ctx: _teardown_db_instance(ctx),
    "rds-subnet-groups": lambda ctx: _teardown_subnet_group(ctx),
    "rds-parameter-groups": lambda ctx: _teardown_param_group(ctx),
    "rds-clusters": lambda ctx: _teardown_cluster(ctx),
    "rds-cluster-members": lambda ctx: _teardown_cluster_members(ctx),
}


def _teardown_db_instance(ctx: TestContext) -> None:
    db_id = ctx.get("rds_db_id")
    if db_id:
        try:
            _rds(ctx).delete_db_instance(
                DBInstanceIdentifier=db_id,
                SkipFinalSnapshot=True,
            )
        except Exception:
            pass


def _teardown_subnet_group(ctx: TestContext) -> None:
    name = ctx.get("rds_subnet_group")
    if name:
        try:
            _rds(ctx).delete_db_subnet_group(DBSubnetGroupName=name)
        except Exception:
            pass


def _teardown_param_group(ctx: TestContext) -> None:
    name = ctx.get("rds_param_group")
    if name:
        try:
            _rds(ctx).delete_db_parameter_group(DBParameterGroupName=name)
        except Exception:
            pass


def _teardown_cluster(ctx: TestContext) -> None:
    cluster_id = ctx.get("rds_cluster_id")
    if cluster_id:
        try:
            _rds(ctx).delete_db_cluster(
                DBClusterIdentifier=cluster_id,
                SkipFinalSnapshot=True,
            )
        except Exception:
            pass
