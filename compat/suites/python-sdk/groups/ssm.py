"""
groups/ssm.py — SSM Parameter Store compatibility test implementations.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _ssm(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).ssm


# ── ssm-parameters ────────────────────────────────────────────────────────────

def setup_ssm_parameters(ctx: TestContext) -> None:
    ctx["ssm_prefix"] = f"/{ctx.run_id}"


def teardown_ssm_parameters(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx.get("ssm_prefix")
    if not prefix:
        return
    try:
        resp = ssm.get_parameters_by_path(Path=prefix, Recursive=True)
        names = [p["Name"] for p in resp.get("Parameters", [])]
        if names:
            ssm.delete_parameters(Names=names)
    except Exception:
        pass


def PutParameter(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    ssm.put_parameter(Name=name, Value="value1", Type="String")
    resp = ssm.get_parameter(Name=name)
    assert resp["Parameter"]["Value"] == "value1", "PutParameter: value mismatch"


def GetParameter(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    resp = ssm.get_parameter(Name=name)
    if resp["Parameter"]["Value"] != "value1":
        raise AssertionError(f"GetParameter: expected 'value1', got {resp['Parameter']['Value']!r}")


def PutParameterOverwrite(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    ssm.put_parameter(Name=name, Value="value-updated", Type="String", Overwrite=True)
    resp = ssm.get_parameter(Name=name)
    if resp["Parameter"]["Value"] != "value-updated":
        raise AssertionError(f"PutParameterOverwrite: expected 'value-updated', got {resp['Parameter']['Value']!r}")


def GetParameterHistory(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    resp = ssm.get_parameter_history(Name=name)
    history = resp.get("Parameters", [])
    if not history:
        raise AssertionError("GetParameterHistory: no history returned")


def PutMultipleParameters(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx["ssm_prefix"]
    for i in range(3):
        ssm.put_parameter(Name=f"{prefix}/multi/{i}", Value=f"val{i}", Type="String")
    resp = ssm.get_parameters(Names=[f"{prefix}/multi/{i}" for i in range(3)])
    assert len(resp.get("Parameters", [])) == 3, f"PutMultipleParameters: expected 3, got {len(resp.get('Parameters', []))}"


def GetParameters(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx["ssm_prefix"]
    names = [f"{prefix}/multi/{i}" for i in range(3)]
    resp = ssm.get_parameters(Names=names)
    if len(resp.get("Parameters", [])) < 3:
        raise AssertionError(f"GetParameters: expected ≥3 parameters, got {len(resp.get('Parameters', []))}")


def DescribeParameters(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    resp = ssm.describe_parameters(
        ParameterFilters=[{"Key": "Name", "Option": "Equals", "Values": [name]}],
    )
    params = resp.get("Parameters", [])
    if not any(p["Name"] == name for p in params):
        raise AssertionError(f"DescribeParameters: {name!r} not found")


def TagParameter(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    ssm.add_tags_to_resource(
        ResourceType="Parameter",
        ResourceId=name,
        Tags=[{"Key": "env", "Value": "compat"}],
    )
    resp = ssm.list_tags_for_resource(ResourceType="Parameter", ResourceId=name)
    tags = {t["Key"]: t["Value"] for t in resp.get("TagList", [])}
    assert tags.get("env") == "compat", f"TagParameter: env tag not found, got {tags}"


def ListTagsForResource(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_prefix']}/key1"
    resp = ssm.list_tags_for_resource(ResourceType="Parameter", ResourceId=name)
    tags = {t["Key"]: t["Value"] for t in resp.get("TagList", [])}
    if tags.get("env") != "compat":
        raise AssertionError(f"ListTagsForResource: expected env=compat, got {tags}")


def DeleteParameters(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx["ssm_prefix"]
    names = [f"{prefix}/multi/{i}" for i in range(3)]
    resp = ssm.delete_parameters(Names=names)
    if resp.get("InvalidParameters"):
        raise AssertionError(f"DeleteParameters: invalid parameters {resp['InvalidParameters']}")


# ── ssm-secure ────────────────────────────────────────────────────────────────

def setup_ssm_secure(ctx: TestContext) -> None:
    ctx["ssm_secure_prefix"] = f"/{ctx.run_id}-sec"


def teardown_ssm_secure(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx.get("ssm_secure_prefix")
    if not prefix:
        return
    try:
        resp = ssm.get_parameters_by_path(Path=prefix, Recursive=True, WithDecryption=True)
        names = [p["Name"] for p in resp.get("Parameters", [])]
        if names:
            ssm.delete_parameters(Names=names)
    except Exception:
        pass


def PutSecureStringParameter(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_secure_prefix']}/secret"
    ssm.put_parameter(Name=name, Value="my-secret-value", Type="SecureString")


def GetSecureStringParameter(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_secure_prefix']}/secret"
    resp = ssm.get_parameter(Name=name, WithDecryption=True)
    if resp["Parameter"]["Value"] != "my-secret-value":
        raise AssertionError(f"GetSecureStringParameter: wrong value {resp['Parameter']['Value']!r}")
    if resp["Parameter"]["Type"] != "SecureString":
        raise AssertionError(f"GetSecureStringParameter: wrong type {resp['Parameter']['Type']!r}")


def GetSecureStringWithoutDecryption(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    name = f"{ctx['ssm_secure_prefix']}/secret"
    resp = ssm.get_parameter(Name=name, WithDecryption=False)
    # Value should be the encrypted blob (not plaintext)
    val = resp["Parameter"]["Value"]
    if val == "my-secret-value":
        raise AssertionError("GetSecureStringWithoutDecryption: returned plaintext unexpectedly")


# ── ssm-path ──────────────────────────────────────────────────────────────────

def setup_ssm_path(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = f"/{ctx.run_id}-path"
    for path in [f"{prefix}/a/x", f"{prefix}/a/y", f"{prefix}/b/z"]:
        ssm.put_parameter(Name=path, Value=f"v:{path}", Type="String")
    ctx["ssm_path_prefix"] = prefix


def teardown_ssm_path(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx.get("ssm_path_prefix")
    if not prefix:
        return
    try:
        resp = ssm.get_parameters_by_path(Path=prefix, Recursive=True)
        names = [p["Name"] for p in resp.get("Parameters", [])]
        if names:
            ssm.delete_parameters(Names=names)
    except Exception:
        pass


def GetParametersByPath(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx["ssm_path_prefix"]
    resp = ssm.get_parameters_by_path(Path=f"{prefix}/a")
    params = resp.get("Parameters", [])
    if len(params) < 2:
        raise AssertionError(f"GetParametersByPath: expected ≥2 parameters under /a, got {len(params)}")


def GetParametersByPathRecursive(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx["ssm_path_prefix"]
    resp = ssm.get_parameters_by_path(Path=prefix, Recursive=True)
    params = resp.get("Parameters", [])
    if len(params) < 3:
        raise AssertionError(f"GetParametersByPathRecursive: expected ≥3 parameters, got {len(params)}")


def GetParametersByPathPaginated(ctx: TestContext) -> None:
    ssm = _ssm(ctx)
    prefix = ctx["ssm_path_prefix"]
    resp1 = ssm.get_parameters_by_path(Path=prefix, Recursive=True, MaxResults=2)
    all_params = list(resp1.get("Parameters", []))
    token = resp1.get("NextToken")
    while token:
        resp = ssm.get_parameters_by_path(Path=prefix, Recursive=True, MaxResults=2, NextToken=token)
        all_params.extend(resp.get("Parameters", []))
        token = resp.get("NextToken")
    if len(all_params) < 3:
        raise AssertionError(f"GetParametersByPathPaginated: expected ≥3 parameters, got {len(all_params)}")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "PutParameter": PutParameter,
    "GetParameter": GetParameter,
    "PutParameterOverwrite": PutParameterOverwrite,
    "GetParameterHistory": GetParameterHistory,
    "PutMultipleParameters": PutMultipleParameters,
    "GetParameters": GetParameters,
    "DescribeParameters": DescribeParameters,
    "TagParameter": TagParameter,
    "ListSSMTagsForResource": ListTagsForResource,
    "DeleteParameters": DeleteParameters,
    "PutSecureStringParameter": PutSecureStringParameter,
    "GetSecureStringParameter": GetSecureStringParameter,
    "GetSecureStringWithoutDecryption": GetSecureStringWithoutDecryption,
    "GetParametersByPath": GetParametersByPath,
    "GetParametersByPathRecursive": GetParametersByPathRecursive,
    "GetParametersByPathPaginated": GetParametersByPathPaginated,
}

SETUP = {
    "ssm-parameters": setup_ssm_parameters,
    "ssm-secure": setup_ssm_secure,
    "ssm-path": setup_ssm_path,
}

TEARDOWN = {
    "ssm-parameters": teardown_ssm_parameters,
    "ssm-secure": teardown_ssm_secure,
    "ssm-path": teardown_ssm_path,
}
