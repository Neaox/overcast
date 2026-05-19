"""
groups/secretsmanager.py — SecretsManager compatibility test implementations.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _sm(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).secretsmanager


# ── secretsmanager-crud ───────────────────────────────────────────────────────

def setup_sm_crud(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = f"{ctx.run_id}-secret"
    resp = sm.create_secret(Name=name, SecretString="initial-value")
    ctx["sm_secret_name"] = name
    ctx["sm_secret_arn"] = resp["ARN"]


def teardown_sm_crud(ctx: TestContext) -> None:
    name = ctx.get("sm_secret_name")
    if name:
        try:
            _sm(ctx).delete_secret(SecretId=name, ForceDeleteWithoutRecovery=True)
        except Exception:
            pass


def CreateSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = f"{ctx.run_id}-s-create"
    resp = sm.create_secret(Name=name, SecretString="hello")
    try:
        if not resp.get("ARN"):
            raise AssertionError("CreateSecret: missing ARN")
    finally:
        sm.delete_secret(SecretId=name, ForceDeleteWithoutRecovery=True)


def GetSecretValue(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    resp = sm.get_secret_value(SecretId=name)
    if resp.get("SecretString") != "initial-value":
        raise AssertionError(f"GetSecretValue: expected 'initial-value', got {resp.get('SecretString')!r}")


def DescribeSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    resp = sm.describe_secret(SecretId=name)
    if resp.get("Name") != name:
        raise AssertionError(f"DescribeSecret: wrong name {resp.get('Name')!r}")
    if not resp.get("ARN"):
        raise AssertionError("DescribeSecret: missing ARN")


def PutSecretValue(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    sm.put_secret_value(SecretId=name, SecretString="updated-value")
    resp = sm.get_secret_value(SecretId=name)
    if resp.get("SecretString") != "updated-value":
        raise AssertionError(f"PutSecretValue: expected 'updated-value', got {resp.get('SecretString')!r}")


def ListSecretVersionIds(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    resp = sm.list_secret_version_ids(SecretId=name)
    versions = resp.get("Versions", [])
    if not versions:
        raise AssertionError("ListSecretVersionIds: no versions returned")


def UpdateSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    sm.update_secret(SecretId=name, Description="updated by compat test")
    resp = sm.describe_secret(SecretId=name)
    if resp.get("Description") != "updated by compat test":
        raise AssertionError(f"UpdateSecret: description not updated; got {resp.get('Description')!r}")


def TagResource(ctx: TestContext) -> None:
    sm = _sm(ctx)
    arn = ctx["sm_secret_arn"]
    sm.tag_resource(SecretId=arn, Tags=[{"Key": "env", "Value": "compat"}])
    resp = sm.describe_secret(SecretId=arn)
    tags = {t["Key"]: t["Value"] for t in resp.get("Tags", [])}
    if tags.get("env") != "compat":
        raise AssertionError(f"TagResource: tag env=compat not found after tagging; got {tags!r}")


def UntagResource(ctx: TestContext) -> None:
    sm = _sm(ctx)
    arn = ctx["sm_secret_arn"]
    sm.untag_resource(SecretId=arn, TagKeys=["env"])
    resp = sm.describe_secret(SecretId=arn)
    tags = {t["Key"]: t["Value"] for t in resp.get("Tags", [])}
    if "env" in tags:
        raise AssertionError(f"UntagResource: tag 'env' still present after untagging; got {tags!r}")


def GetRandomPassword(ctx: TestContext) -> None:
    sm = _sm(ctx)
    resp = sm.get_random_password(PasswordLength=16)
    pwd = resp.get("RandomPassword", "")
    if len(pwd) < 16:
        raise AssertionError(f"GetRandomPassword: expected ≥16 chars, got {len(pwd)!r}")


def BatchGetSecretValue(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    resp = sm.batch_get_secret_value(SecretIdList=[name])
    secrets = resp.get("SecretValues", [])
    if not secrets:
        raise AssertionError("BatchGetSecretValue: no secrets returned")


def ListSecrets(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_secret_name"]
    resp = sm.list_secrets()
    names = [s["Name"] for s in resp.get("SecretList", [])]
    if name not in names:
        raise AssertionError(f"ListSecrets: {name!r} not found in secret list")


def DeleteSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    tmp = f"{ctx.run_id}-s-del"
    sm.create_secret(Name=tmp, SecretString="temp")
    sm.delete_secret(SecretId=tmp, ForceDeleteWithoutRecovery=True)
    resp = sm.list_secrets()
    names = [s["Name"] for s in resp.get("SecretList", [])]
    if tmp in names:
        raise AssertionError(f"DeleteSecret: {tmp!r} still listed after deletion")


# ── secretsmanager-rotate ─────────────────────────────────────────────────────

def setup_sm_rotate(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = f"{ctx.run_id}-s-rot"
    sm.create_secret(Name=name, SecretString="rotate-me")
    ctx["sm_rotate_name"] = name


def teardown_sm_rotate(ctx: TestContext) -> None:
    name = ctx.get("sm_rotate_name")
    if name:
        try:
            _sm(ctx).delete_secret(SecretId=name, ForceDeleteWithoutRecovery=True)
        except Exception:
            pass


def RotateSecret(ctx: TestContext) -> None:
    # Overcast likely returns 501; the harness reports it as "unimplemented"
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    sm.rotate_secret(
        SecretId=name,
        RotationRules={"AutomaticallyAfterDays": 30},
    )


def CancelRotateSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    sm.cancel_rotate_secret(SecretId=name)
    resp = sm.describe_secret(SecretId=name)
    if resp.get("RotationEnabled") is True:
        raise AssertionError("CancelRotateSecret: RotationEnabled still True after cancel")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateSecret": CreateSecret,
    "GetSecretValue": GetSecretValue,
    "DescribeSecret": DescribeSecret,
    "PutSecretValue": PutSecretValue,
    "ListSecretVersionIds": ListSecretVersionIds,
    "UpdateSecret": UpdateSecret,
    "TagResource": TagResource,
    "UntagResource": UntagResource,
    "GetRandomPassword": GetRandomPassword,
    "BatchGetSecretValue": BatchGetSecretValue,
    "ListSecrets": ListSecrets,
    "DeleteSecret": DeleteSecret,
    "RotateSecret": RotateSecret,
    "CancelRotateSecret": CancelRotateSecret,
}

SETUP = {
    "secretsmanager-crud": setup_sm_crud,
    "secretsmanager-rotate": setup_sm_rotate,
}

TEARDOWN = {
    "secretsmanager-crud": teardown_sm_crud,
    "secretsmanager-rotate": teardown_sm_rotate,
}
