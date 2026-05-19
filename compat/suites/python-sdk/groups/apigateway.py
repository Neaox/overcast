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


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateRestApi": CreateRestApi,
    "GetRestApis": GetRestApis,
    "DeleteRestApi": DeleteRestApi,
    "CreateApi": CreateApi,
    "GetApis": GetApis,
    "DeleteApi": DeleteApi,
}

SETUP = {}
TEARDOWN = {
    "apigateway-rest": lambda ctx: _teardown_rest_api(ctx),
    "apigateway-http": lambda ctx: _teardown_http_api(ctx),
}


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
