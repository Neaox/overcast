"""
groups/sts.py — STS compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import base64
import json
import time
from lib.harness import TestContext
from lib.clients import make_clients


def _sts(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).sts


# ── sts-identity ──────────────────────────────────────────────────────────────

def GetCallerIdentity(ctx: TestContext) -> None:
    sts = _sts(ctx)
    resp = sts.get_caller_identity()
    if not resp.get("Account"):
        raise AssertionError("GetCallerIdentity: missing Account")
    if not resp.get("UserId"):
        raise AssertionError("GetCallerIdentity: missing UserId")
    if not resp.get("Arn"):
        raise AssertionError("GetCallerIdentity: missing Arn")


def GetSessionToken(ctx: TestContext) -> None:
    sts = _sts(ctx)
    resp = sts.get_session_token(DurationSeconds=900)
    creds = resp.get("Credentials", {})
    if not creds.get("AccessKeyId"):
        raise AssertionError("GetSessionToken: missing AccessKeyId")
    if not creds.get("SecretAccessKey"):
        raise AssertionError("GetSessionToken: missing SecretAccessKey")
    if not creds.get("SessionToken"):
        raise AssertionError("GetSessionToken: missing SessionToken")


def GetFederationToken(ctx: TestContext) -> None:
    sts = _sts(ctx)
    resp = sts.get_federation_token(
        Name="compat-test",
        DurationSeconds=900,
        Policy=json.dumps({
            "Version": "2012-10-17",
            "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}],
        }),
    )
    creds = resp.get("Credentials", {})
    if not creds.get("AccessKeyId"):
        raise AssertionError("GetFederationToken: missing AccessKeyId")


# ── sts-assume ────────────────────────────────────────────────────────────────

def AssumeRole(ctx: TestContext) -> None:
    sts = _sts(ctx)
    resp = sts.assume_role(
        RoleArn=f"arn:aws:iam::000000000000:role/{ctx.run_id}-role",
        RoleSessionName=f"compat-{ctx.run_id}",
        DurationSeconds=900,
    )
    creds = resp.get("Credentials", {})
    if not creds.get("AccessKeyId"):
        raise AssertionError("AssumeRole: missing AccessKeyId")
    assumed = resp.get("AssumedRoleUser", {})
    if not assumed.get("AssumedRoleId"):
        raise AssertionError("AssumeRole: missing AssumedRoleId")


def AssumeRoleWithWebIdentity(ctx: TestContext) -> None:
    sts = _sts(ctx)
    # A syntactically valid but fake JWT (three base64url segments)
    header = base64.urlsafe_b64encode(json.dumps({"alg": "RS256"}).encode()).rstrip(b"=")
    payload = base64.urlsafe_b64encode(json.dumps({
        "sub": "test-user", "iss": "https://example.com", "exp": 9999999999,
    }).encode()).rstrip(b"=")
    fake_token = f"{header.decode()}.{payload.decode()}.fakesig"
    resp = sts.assume_role_with_web_identity(
        RoleArn=f"arn:aws:iam::000000000000:role/{ctx.run_id}-web-role",
        RoleSessionName=f"compat-web-{ctx.run_id}",
        WebIdentityToken=fake_token,
    )
    if not resp.get("Credentials", {}).get("AccessKeyId"):
        raise AssertionError("AssumeRoleWithWebIdentity: missing AccessKeyId")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "GetCallerIdentity": GetCallerIdentity,
    "GetSessionToken": GetSessionToken,
    "GetFederationToken": GetFederationToken,
    "AssumeRole": AssumeRole,
    "AssumeRoleWithWebIdentity": AssumeRoleWithWebIdentity,
}

SETUP = {}
TEARDOWN = {}
