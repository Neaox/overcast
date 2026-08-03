"""
groups/apigateway.py — API Gateway (REST v1 + HTTP v2) compatibility test implementations
for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _apigw(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("apigateway")


def _apigwv2(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("apigatewayv2")


# ── apigateway-rest ───────────────────────────────────────────────────────────

def CreateRestApi(ctx: TestContext) -> None:
    api_name = f"compat-{ctx.run_id}"
    resp = _apigw(ctx).create_rest_api(name=api_name)
    if not resp.get("id"):
        raise AssertionError("CreateRestApi: missing id")
    ctx["apigw_rest_api_id"] = resp["id"]


def GetRestApis(ctx: TestContext) -> None:
    resp = _apigw(ctx).get_rest_apis()
    if resp.get("items") is None:
        raise AssertionError("GetRestApis: missing items")


def DeleteRestApi(ctx: TestContext) -> None:
    api_id = ctx.get("apigw_rest_api_id")
    if not api_id:
        return
    _apigw(ctx).delete_rest_api(restApiId=api_id)


# ── apigateway-http ───────────────────────────────────────────────────────────

def CreateApi(ctx: TestContext) -> None:
    name = f"compat-{ctx.run_id}"
    resp = _apigwv2(ctx).create_api(Name=name, ProtocolType="HTTP")
    if not resp.get("ApiId"):
        raise AssertionError("CreateApi: missing ApiId")
    ctx["apigw_http_api_id"] = resp["ApiId"]


def GetApis(ctx: TestContext) -> None:
    resp = _apigwv2(ctx).get_apis()
    if resp.get("Items") is None:
        raise AssertionError("GetApis: missing Items")


def DeleteApi(ctx: TestContext) -> None:
    api_id = ctx.get("apigw_http_api_id")
    if not api_id:
        return
    _apigwv2(ctx).delete_api(ApiId=api_id)


# ── apigateway-usage-plans ────────────────────────────────────────────────────

def CreateUsagePlanWithLimits(ctx: TestContext) -> None:
    name = f"oc-{ctx.run_id}-usage-plan"
    resp = _apigw(ctx).create_usage_plan(
        name=name,
        throttle={"rateLimit": 10.0, "burstLimit": 20},
        quota={"limit": 1000, "period": "DAY"},
    )
    if not resp.get("id"):
        raise AssertionError("CreateUsagePlan: missing id")
    if resp.get("name") != name:
        raise AssertionError(f"CreateUsagePlan: name {resp.get('name')!r} != {name!r}")
    ctx["apigw_usage_plan_id"] = resp["id"]


def GetUsagePlan(ctx: TestContext) -> None:
    plan_id = ctx["apigw_usage_plan_id"]
    resp = _apigw(ctx).get_usage_plan(usagePlanId=plan_id)
    if resp.get("id") != plan_id:
        raise AssertionError("GetUsagePlan: wrong id")
    throttle = resp.get("throttle") or {}
    if throttle.get("rateLimit") != 10.0:
        raise AssertionError(f"GetUsagePlan: rateLimit {throttle.get('rateLimit')!r} != 10.0")
    if throttle.get("burstLimit") != 20:
        raise AssertionError(f"GetUsagePlan: burstLimit {throttle.get('burstLimit')!r} != 20")
    quota = resp.get("quota") or {}
    if quota.get("limit") != 1000:
        raise AssertionError(f"GetUsagePlan: quota limit {quota.get('limit')!r} != 1000")
    if quota.get("period") != "DAY":
        raise AssertionError(f"GetUsagePlan: quota period {quota.get('period')!r} != 'DAY'")


def CreateApiKey(ctx: TestContext) -> None:
    name = f"oc-{ctx.run_id}-usage-key"
    resp = _apigw(ctx).create_api_key(name=name, enabled=True)
    if not resp.get("id"):
        raise AssertionError("CreateApiKey: missing id")
    if resp.get("name") != name:
        raise AssertionError(f"CreateApiKey: name {resp.get('name')!r} != {name!r}")
    ctx["apigw_usage_key_id"] = resp["id"]


def CreateUsagePlanKey(ctx: TestContext) -> None:
    plan_id = ctx["apigw_usage_plan_id"]
    key_id = ctx["apigw_usage_key_id"]
    _apigw(ctx).create_usage_plan_key(usagePlanId=plan_id, keyId=key_id, keyType="API_KEY")
    listed = _apigw(ctx).get_usage_plan_keys(usagePlanId=plan_id)
    if not any(k.get("id") == key_id for k in listed.get("items") or []):
        raise AssertionError("GetUsagePlanKeys: attached key not listed")


def GetUsage(ctx: TestContext) -> None:
    plan_id = ctx["apigw_usage_plan_id"]
    key_id = ctx["apigw_usage_key_id"]
    today = _utc_today()
    resp = _apigw(ctx).get_usage(usagePlanId=plan_id, startDate=today, endDate=today)
    if resp.get("usagePlanId") != plan_id:
        raise AssertionError("GetUsage: wrong usagePlanId")
    if resp.get("startDate") != today:
        raise AssertionError("GetUsage: startDate not echoed")
    days = (resp.get("items") or {}).get(key_id)
    if days is None:
        raise AssertionError("GetUsage: no daily log for the attached key")
    if len(days) != 1:
        raise AssertionError(f"GetUsage: expected one day of data, got {len(days)}")
    if len(days[0]) != 2:
        raise AssertionError("GetUsage: expected a [used, remaining] pair")


def DeleteUsagePlan(ctx: TestContext) -> None:
    plan_id = ctx["apigw_usage_plan_id"]
    key_id = ctx["apigw_usage_key_id"]
    # AWS refuses to delete a plan that still has keys attached.
    _apigw(ctx).delete_usage_plan_key(usagePlanId=plan_id, keyId=key_id)
    _apigw(ctx).delete_usage_plan(usagePlanId=plan_id)
    try:
        _apigw(ctx).get_usage_plan(usagePlanId=plan_id)
    except Exception:
        return
    raise AssertionError("GetUsagePlan: deleted plan still readable")


def _utc_today() -> str:
    from datetime import datetime, timezone

    return datetime.now(timezone.utc).strftime("%Y-%m-%d")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateRestApi": CreateRestApi,
    "GetRestApis": GetRestApis,
    "DeleteRestApi": DeleteRestApi,
    "CreateApi": CreateApi,
    "GetApis": GetApis,
    "DeleteApi": DeleteApi,
    # Group-qualified: CreateApiKey/GetUsagePlan are generic enough to collide
    # with another service's group later.
    "apigateway-usage-plans:CreateUsagePlanWithLimits": CreateUsagePlanWithLimits,
    "apigateway-usage-plans:GetUsagePlan": GetUsagePlan,
    "apigateway-usage-plans:CreateApiKey": CreateApiKey,
    "apigateway-usage-plans:CreateUsagePlanKey": CreateUsagePlanKey,
    "apigateway-usage-plans:GetUsage": GetUsage,
    "apigateway-usage-plans:DeleteUsagePlan": DeleteUsagePlan,
}

SETUP = {}
TEARDOWN = {
    "apigateway-rest": lambda ctx: _teardown_rest_api(ctx),
    "apigateway-http": lambda ctx: _teardown_http_api(ctx),
    "apigateway-usage-plans": lambda ctx: _teardown_usage_plan(ctx),
}


def _teardown_usage_plan(ctx: TestContext) -> None:
    plan_id = ctx.get("apigw_usage_plan_id")
    key_id = ctx.get("apigw_usage_key_id")
    if plan_id:
        try:
            _apigw(ctx).delete_usage_plan(usagePlanId=plan_id)
        except Exception:
            pass
    if key_id:
        try:
            _apigw(ctx).delete_api_key(apiKey=key_id)
        except Exception:
            pass


def _teardown_rest_api(ctx: TestContext) -> None:
    api_id = ctx.get("apigw_rest_api_id")
    if api_id:
        try:
            _apigw(ctx).delete_rest_api(restApiId=api_id)
        except Exception:
            pass


def _teardown_http_api(ctx: TestContext) -> None:
    api_id = ctx.get("apigw_http_api_id")
    if api_id:
        try:
            _apigwv2(ctx).delete_api(ApiId=api_id)
        except Exception:
            pass
