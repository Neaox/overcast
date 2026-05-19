"""
groups/waf.py — WAF v2 compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _wafv2(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("wafv2")


# ── waf-webacls ───────────────────────────────────────────────────────────────

def CreateWebACL(ctx: TestContext) -> None:
    waf = _wafv2(ctx)
    name = f"compat-{ctx.run_id}"
    resp = waf.create_web_acl(
        Name=name,
        Scope="REGIONAL",
        DefaultAction={"Allow": {}},
        VisibilityConfig={
            "SampledRequestsEnabled": False,
            "CloudWatchMetricsEnabled": False,
            "MetricName": f"compat-{ctx.run_id}",
        },
        Rules=[],
    )
    summary = resp.get("Summary", {})
    if not summary.get("Id"):
        raise AssertionError("CreateWebACL: missing Id")
    ctx["waf_acl_id"] = summary["Id"]
    ctx["waf_acl_name"] = name
    ctx["waf_acl_lock_token"] = summary.get("LockToken", "")


def GetWebACL(ctx: TestContext) -> None:
    waf = _wafv2(ctx)
    acl_id = ctx.get("waf_acl_id")
    name = ctx.get("waf_acl_name")
    if not acl_id or not name:
        raise AssertionError("GetWebACL: no ACL from CreateWebACL")
    resp = waf.get_web_acl(Id=acl_id, Name=name, Scope="REGIONAL")
    if not resp.get("WebACL", {}).get("Id"):
        raise AssertionError("GetWebACL: missing Id")


def ListWebACLs(ctx: TestContext) -> None:
    _wafv2(ctx).list_web_acls(Scope="REGIONAL")


def DeleteWebACL(ctx: TestContext) -> None:
    waf = _wafv2(ctx)
    acl_id = ctx.get("waf_acl_id")
    name = ctx.get("waf_acl_name")
    lock_token = ctx.get("waf_acl_lock_token")
    if not acl_id or not name or not lock_token:
        return
    waf.delete_web_acl(
        Id=acl_id,
        Name=name,
        Scope="REGIONAL",
        LockToken=lock_token,
    )


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateWebACL": CreateWebACL,
    "GetWebACL": GetWebACL,
    "ListWebACLs": ListWebACLs,
    "DeleteWebACL": DeleteWebACL,
}

SETUP = {}
TEARDOWN = {
    "waf-webacls": lambda ctx: _teardown_web_acl(ctx),
}


def _teardown_web_acl(ctx: TestContext) -> None:
    acl_id = ctx.get("waf_acl_id")
    name = ctx.get("waf_acl_name")
    lock_token = ctx.get("waf_acl_lock_token")
    if acl_id and name and lock_token:
        try:
            _wafv2(ctx).delete_web_acl(
                Id=acl_id,
                Name=name,
                Scope="REGIONAL",
                LockToken=lock_token,
            )
        except Exception:
            pass
