"""
groups/rds.py — RDS compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import time
from lib.harness import TestContext
from lib.clients import make_clients


def _rds(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("rds")


def _wait_for_status(rds, db_id: str, target: str, max_attempts: int = 60) -> None:
    """Poll until the DB instance reaches *target* status or raise after max_attempts."""
    for _ in range(max_attempts):
        resp = rds.describe_db_instances(DBInstanceIdentifier=db_id)
        status = resp.get("DBInstances", [{}])[0].get("DBInstanceStatus", "")
        if status == target:
            return
        time.sleep(0.5)
    raise AssertionError(
        f"DB instance {db_id} did not reach '{target}' after {max_attempts} attempts"
    )


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
        DBParameterGroupFamily="mysql8.0",
        Description="compat test parameter group",
    )
    pg = resp.get("DBParameterGroup", {})
    if not pg.get("DBParameterGroupName"):
        raise AssertionError("CreateDBParameterGroup: missing DBParameterGroupName")
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
}

SETUP = {}
TEARDOWN = {
    "rds-instances": lambda ctx: _teardown_db_instance(ctx),
    "rds-subnet-groups": lambda ctx: _teardown_subnet_group(ctx),
    "rds-parameter-groups": lambda ctx: _teardown_param_group(ctx),
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
