"""
groups/cognito.py — Cognito User Pools compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _cognito(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("cognito-idp")


# ── cognito-userpools ─────────────────────────────────────────────────────────

def CreateUserPool(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_name = f"compat-{ctx.run_id}"
    resp = cog.create_user_pool(PoolName=pool_name)
    pool = resp.get("UserPool", {})
    if not pool.get("Id"):
        raise AssertionError("CreateUserPool: missing Id")
    ctx["cognito_pool_id"] = pool["Id"]


def DescribeUserPool(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        raise AssertionError("DescribeUserPool: no pool from CreateUserPool")
    resp = cog.describe_user_pool(UserPoolId=pool_id)
    if not resp.get("UserPool", {}).get("Id"):
        raise AssertionError("DescribeUserPool: missing Id")


def ListUserPools(ctx: TestContext) -> None:
    _cognito(ctx).list_user_pools(MaxResults=10)


def AdminCreateUser(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        raise AssertionError("AdminCreateUser: no pool from CreateUserPool")
    username = f"compat-user-{ctx.run_id}"
    cog.admin_create_user(UserPoolId=pool_id, Username=username)
    ctx["cognito_username"] = username


def ListUsers(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        return
    cog.list_users(UserPoolId=pool_id)


def AdminDeleteUser(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    username = ctx.get("cognito_username")
    if not pool_id or not username:
        return
    cog.admin_delete_user(UserPoolId=pool_id, Username=username)


def DeleteUserPool(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        return
    cog.delete_user_pool(UserPoolId=pool_id)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateUserPool": CreateUserPool,
    "DescribeUserPool": DescribeUserPool,
    "ListUserPools": ListUserPools,
    "CreateUserPoolClient": lambda ctx: CreateUserPoolClientFn(ctx),
    "ListUserPoolClients": lambda ctx: ListUserPoolClientsFn(ctx),
    "AdminCreateUser": AdminCreateUser,
    "ListUsers": ListUsers,
    "AdminDeleteUser": AdminDeleteUser,
    "DeleteUserPool": DeleteUserPool,
    "CreateUserPoolClient with token validity": lambda ctx: CreateClientTokenValidity(ctx),
    "DescribeUserPoolClient returns token validity": lambda ctx: DescribeClientTokenValidity(ctx),
    "UpdateUserPoolClient changes token validity": lambda ctx: UpdateClientTokenValidity(ctx),
    "DeleteUserPoolClient": lambda ctx: DeleteUserPoolClientFn(ctx),
}

SETUP = {
    "cognito-token-validity": lambda ctx: None,
}
TEARDOWN = {
    "cognito-userpools": lambda ctx: _teardown_user_pool(ctx),
    "cognito-token-validity": lambda ctx: _teardown_token_validity(ctx),
}


def _teardown_user_pool(ctx: TestContext) -> None:
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        return
    username = ctx.get("cognito_username")
    if username:
        try:
            _cognito(ctx).admin_delete_user(UserPoolId=pool_id, Username=username)
        except Exception:
            pass
    try:
        _cognito(ctx).delete_user_pool(UserPoolId=pool_id)
    except Exception:
        pass


def CreateUserPoolClientFn(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        raise AssertionError("CreateUserPoolClient: no pool from CreateUserPool")
    resp = cog.create_user_pool_client(
        UserPoolId=pool_id,
        ClientName=f"compat-client-{ctx.run_id}",
    )
    client = resp.get("UserPoolClient", {})
    if not client.get("ClientId"):
        raise AssertionError("CreateUserPoolClient: missing ClientId")
    ctx["cognito_client_id"] = client["ClientId"]


def ListUserPoolClientsFn(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("cognito_pool_id")
    if not pool_id:
        raise AssertionError("ListUserPoolClients: no pool from CreateUserPool")
    cog.list_user_pool_clients(UserPoolId=pool_id, MaxResults=10)


# ── cognito-token-validity ────────────────────────────────────────────────────

def _teardown_token_validity(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("tv_pool_id")
    client_id = ctx.get("tv_client_id")
    if pool_id and client_id:
        try:
            cog.delete_user_pool_client(UserPoolId=pool_id, ClientId=client_id)
        except Exception:
            pass
    if pool_id:
        try:
            cog.delete_user_pool(UserPoolId=pool_id)
        except Exception:
            pass


def CreateClientTokenValidity(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_name = f"compat-tv-{ctx.run_id}"
    resp = cog.create_user_pool(PoolName=pool_name)
    pool_id = resp.get("UserPool", {}).get("Id")
    if not pool_id:
        raise AssertionError("CreateClientTokenValidity: missing pool Id")
    ctx["tv_pool_id"] = pool_id

    resp = cog.create_user_pool_client(
        UserPoolId=pool_id,
        ClientName=f"compat-client-{ctx.run_id}",
        AccessTokenValidity=2,
        IdTokenValidity=3,
        RefreshTokenValidity=7,
        TokenValidityUnits={
            "AccessToken": "hours",
            "IdToken": "hours",
            "RefreshToken": "days",
        },
    )
    client = resp.get("UserPoolClient", {})
    if not client.get("ClientId"):
        raise AssertionError("CreateClientTokenValidity: missing ClientId")
    ctx["tv_client_id"] = client["ClientId"]


def DescribeClientTokenValidity(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("tv_pool_id")
    client_id = ctx.get("tv_client_id")
    if not pool_id or not client_id:
        raise AssertionError("DescribeClientTokenValidity: missing pool/client")
    cog.describe_user_pool_client(UserPoolId=pool_id, ClientId=client_id)


def UpdateClientTokenValidity(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("tv_pool_id")
    client_id = ctx.get("tv_client_id")
    if not pool_id or not client_id:
        raise AssertionError("UpdateClientTokenValidity: missing pool/client")
    cog.update_user_pool_client(
        UserPoolId=pool_id,
        ClientId=client_id,
        AccessTokenValidity=30,
        TokenValidityUnits={
            "AccessToken": "minutes",
            "IdToken": "hours",
            "RefreshToken": "days",
        },
    )


def DeleteUserPoolClientFn(ctx: TestContext) -> None:
    cog = _cognito(ctx)
    pool_id = ctx.get("tv_pool_id")
    client_id = ctx.get("tv_client_id")
    if not pool_id or not client_id:
        return
    cog.delete_user_pool_client(UserPoolId=pool_id, ClientId=client_id)
