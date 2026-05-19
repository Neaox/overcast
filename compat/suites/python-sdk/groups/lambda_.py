"""
groups/lambda_.py — Lambda compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import base64
import json
import os
import time
import zipfile
import io
from lib.harness import TestContext
from lib.clients import make_clients

# Minimal Node.js Lambda handler — returns a simple JSON payload.
_HANDLER_JS = b"""
exports.handler = async (event) => ({
  statusCode: 200,
  body: JSON.stringify({ hello: "from lambda", event })
});
"""

# A Python Lambda handler for InvokeAsync tests.
_HANDLER_PY = b"""
def handler(event, context):
    return {"statusCode": 200, "body": "ok"}
"""

_ROLE_ARN = "arn:aws:iam::000000000000:role/lambda-role"
_LAYER_PY = b"""
# placeholder layer content
def layer_util():
    return "layer"
"""


def _make_zip(filename: str, content: bytes) -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr(filename, content)
    return buf.getvalue()


def _lambda(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).lambda_


def _logs(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).logs


def _wait_active(ctx: TestContext, fn_name: str, max_attempts: int = 30) -> None:
    lmb = _lambda(ctx)
    for _ in range(max_attempts):
        resp = lmb.get_function(FunctionName=fn_name)
        state = resp.get("Configuration", {}).get("State")
        if state == "Active":
            return
        if state != "Pending":
            return
        time.sleep(0.2)
    raise AssertionError(f"Function {fn_name} did not become Active after {max_attempts} attempts")


def _delete_fn_and_logs(ctx: TestContext, fn_name: str) -> None:
    lmb = _lambda(ctx)
    logs = _logs(ctx)
    try:
        lmb.delete_function(FunctionName=fn_name)
    except Exception:
        pass
    try:
        logs.delete_log_group(logGroupName=f"/aws/lambda/{fn_name}")
    except Exception:
        pass


# ── lambda-crud ───────────────────────────────────────────────────────────────

def setup_lambda_crud(ctx: TestContext) -> None:
    fn_name = f"{ctx.run_id}-fn"
    _lambda(ctx).create_function(
        FunctionName=fn_name,
        Runtime="nodejs20.x",
        Role=_ROLE_ARN,
        Handler="index.handler",
        Code={"ZipFile": _make_zip("index.js", _HANDLER_JS)},
    )
    ctx["lambda_fn_name"] = fn_name


def teardown_lambda_crud(ctx: TestContext) -> None:
    fn_name = ctx.get("lambda_fn_name")
    if fn_name:
        _delete_fn_and_logs(ctx, fn_name)


def CreateFunction(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = f"{ctx.run_id}-fn-create"
    lmb.create_function(
        FunctionName=name,
        Runtime="nodejs20.x",
        Role=_ROLE_ARN,
        Handler="index.handler",
        Code={"ZipFile": _make_zip("index.js", _HANDLER_JS)},
    )
    try:
        resp = lmb.get_function(FunctionName=name)
        if resp["Configuration"]["FunctionName"] != name:
            raise AssertionError(f"CreateFunction: wrong name {resp['Configuration']['FunctionName']!r}")
    finally:
        lmb.delete_function(FunctionName=name)


def GetFunction(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_fn_name"]
    resp = lmb.get_function(FunctionName=name)
    cfg = resp.get("Configuration", {})
    if cfg.get("FunctionName") != name:
        raise AssertionError(f"GetFunction: wrong name {cfg.get('FunctionName')!r}")
    if not cfg.get("FunctionArn"):
        raise AssertionError("GetFunction: missing FunctionArn")


def ListFunctions(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_fn_name"]
    resp = lmb.list_functions()
    fns = [f["FunctionName"] for f in resp.get("Functions", [])]
    if name not in fns:
        raise AssertionError(f"ListFunctions: {name!r} not found in function list")


def UpdateFunctionCode(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_fn_name"]
    updated_js = b"""
exports.handler = async (event) => ({ statusCode: 200, body: 'updated' });
"""
    lmb.update_function_code(
        FunctionName=name,
        ZipFile=_make_zip("index.js", updated_js),
    )
    cfg = lmb.get_function_configuration(FunctionName=name)
    assert cfg.get("CodeSha256"), "UpdateFunctionCode: missing CodeSha256 after update"


def UpdateFunctionConfiguration(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_fn_name"]
    lmb.update_function_configuration(
        FunctionName=name,
        Description="updated by compat test",
        MemorySize=256,
    )
    resp = lmb.get_function_configuration(FunctionName=name)
    if resp.get("MemorySize") != 256:
        raise AssertionError(f"UpdateFunctionConfiguration: expected MemorySize=256, got {resp.get('MemorySize')}")


def DeleteFunction(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = f"{ctx.run_id}-fn-del"
    lmb.create_function(
        FunctionName=name,
        Runtime="nodejs20.x",
        Role=_ROLE_ARN,
        Handler="index.handler",
        Code={"ZipFile": _make_zip("index.js", _HANDLER_JS)},
    )
    lmb.delete_function(FunctionName=name)
    import botocore.exceptions
    try:
        lmb.get_function(FunctionName=name)
        raise AssertionError(f"DeleteFunction: function {name!r} still exists")
    except botocore.exceptions.ClientError as exc:
        if exc.response["Error"]["Code"] != "ResourceNotFoundException":
            raise


# ── lambda-invoke ─────────────────────────────────────────────────────────────

def setup_lambda_invoke(ctx: TestContext) -> None:
    fn_name = f"{ctx.run_id}-fn-invoke"
    _lambda(ctx).create_function(
        FunctionName=fn_name,
        Runtime="nodejs20.x",
        Role=_ROLE_ARN,
        Handler="index.handler",
        Code={"ZipFile": _make_zip("index.js", _HANDLER_JS)},
        Timeout=30,
    )
    ctx["lambda_invoke_fn"] = fn_name
    _wait_active(ctx, fn_name)


def teardown_lambda_invoke(ctx: TestContext) -> None:
    fn_name = ctx.get("lambda_invoke_fn")
    if fn_name:
        _delete_fn_and_logs(ctx, fn_name)


def InvokeDryRun(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_invoke_fn"]
    resp = lmb.invoke(FunctionName=name, InvocationType="DryRun")
    if resp["ResponseMetadata"]["HTTPStatusCode"] != 204:
        raise AssertionError(f"InvokeDryRun: expected 204, got {resp['ResponseMetadata']['HTTPStatusCode']}")


def InvokeSync(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_invoke_fn"]
    resp = lmb.invoke(
        FunctionName=name,
        InvocationType="RequestResponse",
        Payload=json.dumps({"test": True}).encode(),
    )
    if resp.get("FunctionError"):
        raise AssertionError(f"InvokeSync: function error {resp['FunctionError']!r}")
    payload = json.loads(resp["Payload"].read())
    if payload.get("statusCode") != 200:
        raise AssertionError(f"InvokeSync: unexpected payload {payload}")


def InvokeAsync(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_invoke_fn"]
    resp = lmb.invoke(
        FunctionName=name,
        InvocationType="Event",
        Payload=json.dumps({"async": True}).encode(),
    )
    if resp["ResponseMetadata"]["HTTPStatusCode"] != 202:
        raise AssertionError(f"InvokeAsync: expected 202, got {resp['ResponseMetadata']['HTTPStatusCode']}")


# ── lambda-invoke-stream ──────────────────────────────────────────────────────

def setup_lambda_invoke_stream(ctx: TestContext) -> None:
    fn_name = f"{ctx.run_id}-fn-stream"
    _lambda(ctx).create_function(
        FunctionName=fn_name,
        Runtime="nodejs20.x",
        Role=_ROLE_ARN,
        Handler="index.handler",
        Code={"ZipFile": _make_zip("index.js", _HANDLER_JS)},
        Timeout=30,
    )
    ctx["lambda_stream_fn"] = fn_name
    _wait_active(ctx, fn_name)


def teardown_lambda_invoke_stream(ctx: TestContext) -> None:
    fn_name = ctx.get("lambda_stream_fn")
    if fn_name:
        _delete_fn_and_logs(ctx, fn_name)


def InvokeWithResponseStream(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_stream_fn"]
    resp = lmb.invoke_with_response_stream(
        FunctionName=name,
        Payload=b'{}',
    )
    if resp["ResponseMetadata"]["HTTPStatusCode"] != 200:
        raise AssertionError(
            f"InvokeWithResponseStream: expected 200, got {resp['ResponseMetadata']['HTTPStatusCode']}"
        )
    # Consume the event stream.
    for event in resp.get("EventStream", []):
        pass


# ── lambda-aliases ────────────────────────────────────────────────────────────

def setup_lambda_aliases(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    fn_name = f"{ctx.run_id}-fn-alias"
    lmb.create_function(
        FunctionName=fn_name,
        Runtime="nodejs20.x",
        Role=_ROLE_ARN,
        Handler="index.handler",
        Code={"ZipFile": _make_zip("index.js", _HANDLER_JS)},
    )
    ctx["lambda_alias_fn"] = fn_name


def teardown_lambda_aliases(ctx: TestContext) -> None:
    fn_name = ctx.get("lambda_alias_fn")
    if fn_name:
        _delete_fn_and_logs(ctx, fn_name)


def PublishVersion(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    resp = lmb.publish_version(FunctionName=name)
    if not resp.get("Version"):
        raise AssertionError("PublishVersion: missing Version in response")
    ctx["lambda_version"] = resp["Version"]


def ListVersionsByFunction(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    resp = lmb.list_versions_by_function(FunctionName=name)
    versions = resp.get("Versions", [])
    if not versions:
        raise AssertionError("ListVersionsByFunction: no versions returned")


def CreateAlias(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    version = ctx.get("lambda_version", "$LATEST")
    resp = lmb.create_alias(
        FunctionName=name,
        Name="live",
        FunctionVersion=version,
    )
    if not resp.get("AliasArn"):
        raise AssertionError("CreateAlias: missing AliasArn")
    ctx["lambda_alias_arn"] = resp["AliasArn"]


def GetAlias(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    resp = lmb.get_alias(FunctionName=name, Name="live")
    if resp.get("Name") != "live":
        raise AssertionError(f"GetAlias: wrong name {resp.get('Name')!r}")


def ListAliases(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    resp = lmb.list_aliases(FunctionName=name)
    aliases = [a["Name"] for a in resp.get("Aliases", [])]
    if "live" not in aliases:
        raise AssertionError(f"ListAliases: 'live' not found in {aliases}")


def UpdateAlias(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    lmb.update_alias(FunctionName=name, Name="live", Description="updated by compat")
    resp = lmb.get_alias(FunctionName=name, Name="live")
    if resp.get("Description") != "updated by compat":
        raise AssertionError(f"UpdateAlias: description not updated; got {resp.get('Description')!r}")


def DeleteAlias(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_alias_fn"]
    lmb.delete_alias(FunctionName=name, Name="live")
    import botocore.exceptions
    try:
        lmb.get_alias(FunctionName=name, Name="live")
        raise AssertionError("DeleteAlias: alias still exists")
    except botocore.exceptions.ClientError as exc:
        if exc.response["Error"]["Code"] != "ResourceNotFoundException":
            raise


# ── lambda-layers ─────────────────────────────────────────────────────────────

def setup_lambda_layers(ctx: TestContext) -> None:
    ctx["lambda_layer_name"] = f"{ctx.run_id}-layer"


def teardown_lambda_layers(ctx: TestContext) -> None:
    pass  # layers deleted by tests


def PublishLayerVersion(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_layer_name"]
    resp = lmb.publish_layer_version(
        LayerName=name,
        Content={"ZipFile": _make_zip("lib.py", _LAYER_PY)},
        CompatibleRuntimes=["python3.12"],
    )
    if not resp.get("LayerArn"):
        raise AssertionError("PublishLayerVersion: missing LayerArn")
    ctx["lambda_layer_version"] = resp.get("Version", 1)


def ListLayers(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_layer_name"]
    resp = lmb.list_layers()
    layer_names = [l["LayerName"] for l in resp.get("Layers", [])]
    if name not in layer_names:
        raise AssertionError(f"ListLayers: {name!r} not found in {layer_names}")


def DeleteLayerVersion(ctx: TestContext) -> None:
    lmb = _lambda(ctx)
    name = ctx["lambda_layer_name"]
    version = ctx.get("lambda_layer_version", 1)
    lmb.delete_layer_version(LayerName=name, VersionNumber=version)
    resp = lmb.list_layer_versions(LayerName=name)
    versions = [v["Version"] for v in resp.get("LayerVersions", [])]
    assert version not in versions, f"DeleteLayerVersion: version {version} still present"


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateFunction": CreateFunction,
    "GetFunction": GetFunction,
    "ListFunctions": ListFunctions,
    "UpdateFunctionCode": UpdateFunctionCode,
    "UpdateFunctionConfiguration": UpdateFunctionConfiguration,
    "DeleteFunction": DeleteFunction,
    "InvokeDryRun": InvokeDryRun,
    "InvokeSync": InvokeSync,
    "InvokeAsync": InvokeAsync,
    "InvokeWithResponseStream": InvokeWithResponseStream,
    "PublishVersion": PublishVersion,
    "ListVersionsByFunction": ListVersionsByFunction,
    "CreateAlias": CreateAlias,
    "GetAlias": GetAlias,
    "ListAliases": ListAliases,
    "UpdateAlias": UpdateAlias,
    "DeleteAlias": DeleteAlias,
    "PublishLayerVersion": PublishLayerVersion,
    "ListLayers": ListLayers,
    "DeleteLayerVersion": DeleteLayerVersion,
}

SETUP = {
    "lambda-crud": setup_lambda_crud,
    "lambda-invoke": setup_lambda_invoke,
    "lambda-invoke-stream": setup_lambda_invoke_stream,
    "lambda-aliases": setup_lambda_aliases,
    "lambda-layers": setup_lambda_layers,
}

TEARDOWN = {
    "lambda-crud": teardown_lambda_crud,
    "lambda-invoke": teardown_lambda_invoke,
    "lambda-invoke-stream": teardown_lambda_invoke_stream,
    "lambda-aliases": teardown_lambda_aliases,
    "lambda-layers": teardown_lambda_layers,
}
