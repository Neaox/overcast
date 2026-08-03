"""
groups/secretsmanager.py — SecretsManager compatibility test implementations.
"""

from __future__ import annotations

import json

from botocore.exceptions import ClientError

from lib.harness import TestContext
from lib.clients import make_clients

# AWS wants a ClientRequestToken of 32-64 characters, and the token becomes the
# version ID, so a fixed UUID keeps the staging-label assertions readable.
PENDING_TOKEN = "0f9c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f"

# A minimal, well-formed secret resource policy.
COMPAT_RESOURCE_POLICY = json.dumps(
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Sid": "OvercastCompatRead",
                "Effect": "Allow",
                "Principal": {"AWS": "arn:aws:iam::000000000000:root"},
                "Action": "secretsmanager:GetSecretValue",
                "Resource": "*",
            }
        ],
    }
)


def _sm(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).secretsmanager


def _rotation_lambda_arn(ctx: TestContext) -> str:
    """A placeholder rotation function.

    The rotate group always passes RotateImmediately=False, so it is never
    invoked: driving the four-step protocol needs a real rotation Lambda and
    belongs with the Docker-dependent Lambda groups.
    """
    return f"arn:aws:lambda:{ctx.region}:000000000000:function:oc-rotate-fn"


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


def RotateSecretWithoutLambda(ctx: TestContext) -> None:
    # AWS refuses to enable rotation on a secret with no rotation function,
    # neither on the call nor already configured.
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    try:
        sm.rotate_secret(SecretId=name, RotationRules={"AutomaticallyAfterDays": 30})
    except ClientError as err:
        code = err.response.get("Error", {}).get("Code")
        if code != "InvalidRequestException":
            raise AssertionError(
                f"RotateSecretWithoutLambda: expected InvalidRequestException, got {code!r}"
            ) from err
        return
    raise AssertionError(
        "RotateSecretWithoutLambda: expected InvalidRequestException, call succeeded"
    )


def RotateSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    arn = _rotation_lambda_arn(ctx)
    sm.rotate_secret(
        SecretId=name,
        RotationLambdaARN=arn,
        RotationRules={"AutomaticallyAfterDays": 30},
        RotateImmediately=False,
    )
    resp = sm.describe_secret(SecretId=name)
    if resp.get("RotationEnabled") is not True:
        raise AssertionError("RotateSecret: RotationEnabled not True")
    if resp.get("RotationLambdaARN") != arn:
        raise AssertionError(
            f"RotateSecret: RotationLambdaARN {resp.get('RotationLambdaARN')!r}, want {arn!r}"
        )
    if resp.get("RotationRules", {}).get("AutomaticallyAfterDays") != 30:
        raise AssertionError(
            f"RotateSecret: AutomaticallyAfterDays {resp.get('RotationRules')!r}, want 30"
        )
    if not resp.get("NextRotationDate"):
        raise AssertionError("RotateSecret: NextRotationDate not set")


def PutSecretValuePending(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    resp = sm.put_secret_value(
        SecretId=name,
        SecretString="pending-value",
        ClientRequestToken=PENDING_TOKEN,
        VersionStages=["AWSPENDING"],
    )
    if resp.get("VersionId") != PENDING_TOKEN:
        raise AssertionError(
            f"PutSecretValuePending: VersionId {resp.get('VersionId')!r}, "
            f"want the ClientRequestToken {PENDING_TOKEN!r}"
        )
    if "AWSPENDING" not in resp.get("VersionStages", []):
        raise AssertionError(
            f"PutSecretValuePending: VersionStages {resp.get('VersionStages')!r} missing AWSPENDING"
        )
    # Staging a pending version must not change AWSCURRENT.
    current = sm.get_secret_value(SecretId=name)
    if current.get("SecretString") != "rotate-me":
        raise AssertionError(
            f"PutSecretValuePending: AWSCURRENT {current.get('SecretString')!r}, "
            "want the untouched 'rotate-me'"
        )


def GetSecretValueByStage(ctx: TestContext) -> None:
    sm = _sm(ctx)
    resp = sm.get_secret_value(SecretId=ctx["sm_rotate_name"], VersionStage="AWSPENDING")
    if resp.get("SecretString") != "pending-value":
        raise AssertionError(
            f"GetSecretValueByStage: SecretString {resp.get('SecretString')!r}, want 'pending-value'"
        )


def UpdateSecretVersionStage(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    desc = sm.describe_secret(SecretId=name)
    current = next(
        (vid for vid, stages in desc.get("VersionIdsToStages", {}).items() if "AWSCURRENT" in stages),
        None,
    )
    if not current:
        raise AssertionError("UpdateSecretVersionStage: no AWSCURRENT version")

    # This is what a rotation function's finishSecret step does.
    sm.update_secret_version_stage(
        SecretId=name,
        VersionStage="AWSCURRENT",
        MoveToVersionId=PENDING_TOKEN,
        RemoveFromVersionId=current,
    )

    after = sm.get_secret_value(SecretId=name)
    if after.get("SecretString") != "pending-value":
        raise AssertionError(
            f"UpdateSecretVersionStage: AWSCURRENT {after.get('SecretString')!r}, want 'pending-value'"
        )
    if after.get("VersionId") != PENDING_TOKEN:
        raise AssertionError(
            f"UpdateSecretVersionStage: AWSCURRENT version {after.get('VersionId')!r}, want {PENDING_TOKEN!r}"
        )

    post = sm.describe_secret(SecretId=name)
    if "AWSPREVIOUS" not in post.get("VersionIdsToStages", {}).get(current, []):
        raise AssertionError(
            "UpdateSecretVersionStage: displaced version stages "
            f"{post.get('VersionIdsToStages', {}).get(current)!r}, want AWSPREVIOUS"
        )


def CancelRotateSecret(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_rotate_name"]
    sm.cancel_rotate_secret(SecretId=name)
    resp = sm.describe_secret(SecretId=name)
    if resp.get("RotationEnabled") is True:
        raise AssertionError("CancelRotateSecret: RotationEnabled still True after cancel")
    # AWS keeps the function configured so rotation can be turned back on.
    arn = _rotation_lambda_arn(ctx)
    if resp.get("RotationLambdaARN") != arn:
        raise AssertionError(
            f"CancelRotateSecret: RotationLambdaARN {resp.get('RotationLambdaARN')!r}, "
            f"want it retained as {arn!r}"
        )


# ── secretsmanager-policy ─────────────────────────────────────────────────────
#
# Overcast stores and validates a secret's resource policy but does not evaluate
# it, so these assert the round-trip and the validation verdict, never an access
# decision.

def setup_sm_policy(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = f"{ctx.run_id}-s-pol"
    sm.create_secret(Name=name, SecretString="policy-value")
    ctx["sm_policy_name"] = name


def teardown_sm_policy(ctx: TestContext) -> None:
    name = ctx.get("sm_policy_name")
    if name:
        try:
            _sm(ctx).delete_secret(SecretId=name, ForceDeleteWithoutRecovery=True)
        except Exception:
            pass


def ValidateResourcePolicy(ctx: TestContext) -> None:
    sm = _sm(ctx)
    ok = sm.validate_resource_policy(ResourcePolicy=COMPAT_RESOURCE_POLICY)
    if ok.get("PolicyValidationPassed") is not True:
        raise AssertionError(
            f"ValidateResourcePolicy: valid policy rejected: {ok.get('ValidationErrors')!r}"
        )

    bad = sm.validate_resource_policy(ResourcePolicy=json.dumps({"Version": "2012-10-17"}))
    if bad.get("PolicyValidationPassed") is True:
        raise AssertionError("ValidateResourcePolicy: policy with no Statement should not pass")
    if not bad.get("ValidationErrors"):
        raise AssertionError("ValidateResourcePolicy: no ValidationErrors for a failing policy")


def PutResourcePolicy(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_policy_name"]
    resp = sm.put_resource_policy(SecretId=name, ResourcePolicy=COMPAT_RESOURCE_POLICY)
    if not resp.get("ARN"):
        raise AssertionError("PutResourcePolicy: missing ARN")
    if resp.get("Name") != name:
        raise AssertionError(f"PutResourcePolicy: Name {resp.get('Name')!r}, want {name!r}")


def GetResourcePolicy(ctx: TestContext) -> None:
    sm = _sm(ctx)
    resp = sm.get_resource_policy(SecretId=ctx["sm_policy_name"])
    policy = resp.get("ResourcePolicy", "")
    if "OvercastCompatRead" not in policy:
        raise AssertionError(
            f"GetResourcePolicy: policy {policy!r} does not carry the Sid that was stored"
        )


def DeleteResourcePolicy(ctx: TestContext) -> None:
    sm = _sm(ctx)
    name = ctx["sm_policy_name"]
    sm.delete_resource_policy(SecretId=name)
    resp = sm.get_resource_policy(SecretId=name)
    if resp.get("ResourcePolicy"):
        raise AssertionError(
            f"DeleteResourcePolicy: policy still present: {resp.get('ResourcePolicy')!r}"
        )


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateSecret": CreateSecret,
    "GetSecretValue": GetSecretValue,
    "DescribeSecret": DescribeSecret,
    "PutSecretValue": PutSecretValue,
    "ListSecretVersionIds": ListSecretVersionIds,
    "UpdateSecret": UpdateSecret,
    "secretsmanager-crud:TagResource": TagResource,
    "secretsmanager-crud:UntagResource": UntagResource,
    "GetRandomPassword": GetRandomPassword,
    "BatchGetSecretValue": BatchGetSecretValue,
    "ListSecrets": ListSecrets,
    "DeleteSecret": DeleteSecret,
    "RotateSecretWithoutLambda": RotateSecretWithoutLambda,
    "RotateSecret": RotateSecret,
    "PutSecretValuePending": PutSecretValuePending,
    "GetSecretValueByStage": GetSecretValueByStage,
    "UpdateSecretVersionStage": UpdateSecretVersionStage,
    "CancelRotateSecret": CancelRotateSecret,
    "ValidateResourcePolicy": ValidateResourcePolicy,
    "PutResourcePolicy": PutResourcePolicy,
    "GetResourcePolicy": GetResourcePolicy,
    "DeleteResourcePolicy": DeleteResourcePolicy,
}

SETUP = {
    "secretsmanager-crud": setup_sm_crud,
    "secretsmanager-rotate": setup_sm_rotate,
    "secretsmanager-policy": setup_sm_policy,
}

TEARDOWN = {
    "secretsmanager-crud": teardown_sm_crud,
    "secretsmanager-rotate": teardown_sm_rotate,
    "secretsmanager-policy": teardown_sm_policy,
}
