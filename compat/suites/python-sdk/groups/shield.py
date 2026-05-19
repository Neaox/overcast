"""
groups/shield.py — Shield (DDoS protection) compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _shield(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("shield")


# ── shield-protections ────────────────────────────────────────────────────────

def DescribeSubscription(ctx: TestContext) -> None:
    _shield(ctx).describe_subscription()


def CreateProtection(ctx: TestContext) -> None:
    sh = _shield(ctx)
    name = f"compat-{ctx.run_id}"
    resource_arn = f"arn:aws:ec2:us-east-1:000000000000:eip/eipalloc-{ctx.run_id}"
    resp = sh.create_protection(Name=name, ResourceArn=resource_arn)
    if not resp.get("ProtectionId"):
        raise AssertionError("CreateProtection: missing ProtectionId")
    ctx["shield_protection_id"] = resp["ProtectionId"]


def ListProtections(ctx: TestContext) -> None:
    _shield(ctx).list_protections()


def DescribeProtection(ctx: TestContext) -> None:
    sh = _shield(ctx)
    protection_id = ctx.get("shield_protection_id")
    if not protection_id:
        raise AssertionError("DescribeProtection: no protection_id")
    resp = sh.describe_protection(ProtectionId=protection_id)
    prot = resp.get("Protection", {})
    if prot.get("Id") != protection_id:
        raise AssertionError(f"DescribeProtection: id mismatch")


def DeleteProtection(ctx: TestContext) -> None:
    sh = _shield(ctx)
    protection_id = ctx.get("shield_protection_id")
    if not protection_id:
        return
    sh.delete_protection(ProtectionId=protection_id)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "DescribeSubscription": DescribeSubscription,
    "CreateProtection": CreateProtection,
    "ListProtections": ListProtections,
    "DescribeProtection": DescribeProtection,
    "DeleteProtection": DeleteProtection,
}

SETUP = {}
TEARDOWN = {
    "shield-protections": lambda ctx: _teardown_protection(ctx),
}


def _teardown_protection(ctx: TestContext) -> None:
    protection_id = ctx.get("shield_protection_id")
    if protection_id:
        try:
            _shield(ctx).delete_protection(ProtectionId=protection_id)
        except Exception:
            pass
