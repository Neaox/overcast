"""
groups/efs.py — Elastic File System compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _efs(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("efs")


def _subnet_id(ctx: TestContext) -> str:
    """Build an AWS-shaped subnet ID that is still unique per run.

    A file system accepts one mount target per availability zone, and the
    emulator derives the zone from the subnet ID — so it must vary per run.
    The shape matters too: the SDKs validate SubnetId's length before the
    request leaves the client, and "subnet-<run_id>" is short enough to be
    rejected. Real long-form IDs are "subnet-" plus 17 hex characters, so keep
    the run ID's own hex digits and pad the rest.
    """
    hex_digits = "".join(c for c in ctx.run_id if c in "0123456789abcdef")
    return "subnet-" + (hex_digits + "0" * 17)[:17]


# ── efs-mount-targets ─────────────────────────────────────────────────────────

def CreateMountTarget(ctx: TestContext) -> None:
    efs = _efs(ctx)
    fs_id = ctx.get("efs_file_system_id")
    if not fs_id:
        raise AssertionError("CreateMountTarget: no file system from setup")
    resp = efs.create_mount_target(
        FileSystemId=fs_id,
        SubnetId=_subnet_id(ctx),
        SecurityGroups=["sg-compat-a"],
    )
    if not resp.get("MountTargetId"):
        raise AssertionError("CreateMountTarget: missing MountTargetId")
    if resp.get("FileSystemId") != fs_id:
        raise AssertionError(
            f"CreateMountTarget: expected FileSystemId {fs_id}, got {resp.get('FileSystemId')}"
        )
    if not resp.get("LifeCycleState"):
        raise AssertionError("CreateMountTarget: missing LifeCycleState")
    ctx["efs_mount_target_id"] = resp["MountTargetId"]


def DescribeMountTargets(ctx: TestContext) -> None:
    efs = _efs(ctx)
    fs_id = ctx.get("efs_file_system_id")
    mt_id = ctx.get("efs_mount_target_id")
    if not mt_id:
        raise AssertionError("DescribeMountTargets: no mount target from CreateMountTarget")
    resp = efs.describe_mount_targets(FileSystemId=fs_id)
    targets = resp.get("MountTargets", [])
    if not targets:
        raise AssertionError("DescribeMountTargets: expected at least 1 mount target, got 0")
    match = next((t for t in targets if t.get("MountTargetId") == mt_id), None)
    if match is None:
        raise AssertionError(f"DescribeMountTargets: mount target {mt_id} not in the response")
    if not match.get("IpAddress"):
        raise AssertionError(f"DescribeMountTargets: mount target {mt_id} has no IpAddress")


def DescribeMountTargetSecurityGroups(ctx: TestContext) -> None:
    efs = _efs(ctx)
    mt_id = ctx.get("efs_mount_target_id")
    if not mt_id:
        raise AssertionError(
            "DescribeMountTargetSecurityGroups: no mount target from CreateMountTarget"
        )
    resp = efs.describe_mount_target_security_groups(MountTargetId=mt_id)
    groups = resp.get("SecurityGroups", [])
    if groups != ["sg-compat-a"]:
        raise AssertionError(
            f"DescribeMountTargetSecurityGroups: expected ['sg-compat-a'], got {groups}"
        )


def ModifyMountTargetSecurityGroups(ctx: TestContext) -> None:
    efs = _efs(ctx)
    mt_id = ctx.get("efs_mount_target_id")
    if not mt_id:
        raise AssertionError(
            "ModifyMountTargetSecurityGroups: no mount target from CreateMountTarget"
        )
    efs.modify_mount_target_security_groups(
        MountTargetId=mt_id, SecurityGroups=["sg-compat-b", "sg-compat-c"]
    )
    # The mutation is what this test is about, so assert it here rather than
    # leaving it for another test to notice.
    resp = efs.describe_mount_target_security_groups(MountTargetId=mt_id)
    groups = resp.get("SecurityGroups", [])
    if groups != ["sg-compat-b", "sg-compat-c"]:
        raise AssertionError(
            f"ModifyMountTargetSecurityGroups: expected ['sg-compat-b', 'sg-compat-c'], got {groups}"
        )


def DeleteMountTarget(ctx: TestContext) -> None:
    efs = _efs(ctx)
    fs_id = ctx.get("efs_file_system_id")
    mt_id = ctx.get("efs_mount_target_id")
    if not mt_id:
        raise AssertionError("DeleteMountTarget: no mount target from CreateMountTarget")
    efs.delete_mount_target(MountTargetId=mt_id)
    ctx["efs_mount_target_id"] = ""

    resp = efs.describe_mount_targets(FileSystemId=fs_id)
    remaining = [t.get("MountTargetId") for t in resp.get("MountTargets", [])]
    if mt_id in remaining:
        raise AssertionError(f"DeleteMountTarget: mount target {mt_id} still listed after delete")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateMountTarget": CreateMountTarget,
    "DescribeMountTargets": DescribeMountTargets,
    "DescribeMountTargetSecurityGroups": DescribeMountTargetSecurityGroups,
    "ModifyMountTargetSecurityGroups": ModifyMountTargetSecurityGroups,
    "DeleteMountTarget": DeleteMountTarget,
}


def _setup_mount_targets(ctx: TestContext) -> None:
    """Create the file system the group's mount targets attach to.

    Mount targets have no meaning without one, so this is setup rather than a
    test.
    """
    resp = _efs(ctx).create_file_system(
        CreationToken=f"oc-{ctx.run_id}-efs-mount-targets"
    )
    if not resp.get("FileSystemId"):
        raise AssertionError("setup: CreateFileSystem returned no FileSystemId")
    ctx["efs_file_system_id"] = resp["FileSystemId"]


def _teardown_mount_targets(ctx: TestContext) -> None:
    """Remove the mount target before the file system: DeleteFileSystem
    answers FileSystemInUse while any mount target remains."""
    mt_id = ctx.get("efs_mount_target_id")
    if mt_id:
        try:
            _efs(ctx).delete_mount_target(MountTargetId=mt_id)
        except Exception:
            pass
    fs_id = ctx.get("efs_file_system_id")
    if fs_id:
        try:
            _efs(ctx).delete_file_system(FileSystemId=fs_id)
        except Exception:
            pass


SETUP = {
    "efs-mount-targets": _setup_mount_targets,
}
TEARDOWN = {
    "efs-mount-targets": _teardown_mount_targets,
}
