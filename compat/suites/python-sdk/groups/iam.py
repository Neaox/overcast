"""
groups/iam.py — IAM compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import json
from lib.harness import TestContext
from lib.clients import make_clients

_ASSUME_POLICY = json.dumps({
    "Version": "2012-10-17",
    "Statement": [{
        "Effect": "Allow",
        "Principal": {"Service": "lambda.amazonaws.com"},
        "Action": "sts:AssumeRole",
    }],
})

_POLICY_DOC = json.dumps({
    "Version": "2012-10-17",
    "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}],
})


def _iam(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).iam


# ── iam-users ─────────────────────────────────────────────────────────────────

def setup_iam_users(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-user"
    iam.create_user(UserName=name)
    ctx["iam_user"] = name


def teardown_iam_users(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx.get("iam_user")
    if not name:
        return
    # Delete access keys first
    try:
        keys = iam.list_access_keys(UserName=name).get("AccessKeyMetadata", [])
        for key in keys:
            iam.delete_access_key(UserName=name, AccessKeyId=key["AccessKeyId"])
    except Exception:
        pass
    # Delete inline policies
    try:
        policies = iam.list_user_policies(UserName=name).get("PolicyNames", [])
        for p in policies:
            iam.delete_user_policy(UserName=name, PolicyName=p)
    except Exception:
        pass
    try:
        iam.delete_user(UserName=name)
    except Exception:
        pass


def CreateUser(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-u-create"
    resp = iam.create_user(UserName=name)
    try:
        if resp.get("User", {}).get("UserName") != name:
            raise AssertionError(f"CreateUser: wrong name {resp.get('User', {}).get('UserName')!r}")
    finally:
        iam.delete_user(UserName=name)


def GetUser(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    resp = iam.get_user(UserName=name)
    if resp.get("User", {}).get("UserName") != name:
        raise AssertionError(f"GetUser: wrong name {resp.get('User', {}).get('UserName')!r}")


def ListUsers(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    resp = iam.list_users()
    names = [u["UserName"] for u in resp.get("Users", [])]
    if name not in names:
        raise AssertionError(f"ListUsers: {name!r} not found in user list")


def CreateAccessKey(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    resp = iam.create_access_key(UserName=name)
    key = resp.get("AccessKey", {})
    if not key.get("AccessKeyId"):
        raise AssertionError("CreateAccessKey: missing AccessKeyId")
    ctx["iam_access_key_id"] = key["AccessKeyId"]


def DeleteAccessKey(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    key_id = ctx.get("iam_access_key_id")
    if not key_id:
        raise AssertionError("DeleteAccessKey: no access key to delete")
    iam.delete_access_key(UserName=name, AccessKeyId=key_id)
    ctx["iam_access_key_id"] = None


def PutUserPolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    iam.put_user_policy(
        UserName=name,
        PolicyName="compat-policy",
        PolicyDocument=_POLICY_DOC,
    )


def GetUserPolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    resp = iam.get_user_policy(UserName=name, PolicyName="compat-policy")
    if resp.get("PolicyName") != "compat-policy":
        raise AssertionError(f"GetUserPolicy: wrong name {resp.get('PolicyName')!r}")


def DeleteUserPolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    iam.delete_user_policy(UserName=name, PolicyName="compat-policy")


def DeleteUser(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-u-del"
    iam.create_user(UserName=name)
    iam.delete_user(UserName=name)
    resp = iam.list_users()
    names = [u["UserName"] for u in resp.get("Users", [])]
    if name in names:
        raise AssertionError(f"DeleteUser: {name!r} still listed after deletion")


def UpdateUser(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    new_name = name + "-upd"
    iam.update_user(UserName=name, NewUserName=new_name)
    # Rename back so teardown works.
    iam.update_user(UserName=new_name, NewUserName=name)


def ListAccessKeys(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_user"]
    resp = iam.list_access_keys(UserName=name)
    assert "AccessKeyMetadata" in resp, "ListAccessKeys: missing AccessKeyMetadata"


# ── iam-roles ─────────────────────────────────────────────────────────────────

def setup_iam_roles(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-role"
    resp = iam.create_role(RoleName=name, AssumeRolePolicyDocument=_ASSUME_POLICY)
    ctx["iam_role_name"] = name
    ctx["iam_role_arn"] = resp["Role"]["Arn"]


def teardown_iam_roles(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx.get("iam_role_name")
    if not name:
        return
    # Delete inline role policies
    try:
        iam.delete_role_policy(RoleName=name, PolicyName="inline-role-policy")
    except Exception:
        pass
    # Detach managed policies
    try:
        policies = iam.list_attached_role_policies(RoleName=name).get("AttachedPolicies", [])
        for p in policies:
            iam.detach_role_policy(RoleName=name, PolicyArn=p["PolicyArn"])
    except Exception:
        pass
    # Remove from instance profiles
    try:
        profiles = iam.list_instance_profiles_for_role(RoleName=name).get("InstanceProfiles", [])
        for ip in profiles:
            iam.remove_role_from_instance_profile(
                InstanceProfileName=ip["InstanceProfileName"], RoleName=name
            )
    except Exception:
        pass
    # Delete instance profiles created by test
    ip_name = ctx.get("iam_instance_profile")
    if ip_name:
        try:
            iam.delete_instance_profile(InstanceProfileName=ip_name)
        except Exception:
            pass
    try:
        iam.delete_role(RoleName=name)
    except Exception:
        pass


def CreateRole(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-r-create"
    resp = iam.create_role(RoleName=name, AssumeRolePolicyDocument=_ASSUME_POLICY)
    try:
        if resp.get("Role", {}).get("RoleName") != name:
            raise AssertionError(f"CreateRole: wrong name")
    finally:
        iam.delete_role(RoleName=name)


def GetRole(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    resp = iam.get_role(RoleName=name)
    if resp.get("Role", {}).get("RoleName") != name:
        raise AssertionError(f"GetRole: wrong name {resp.get('Role', {}).get('RoleName')!r}")


def ListRoles(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    resp = iam.list_roles()
    names = [r["RoleName"] for r in resp.get("Roles", [])]
    if name not in names:
        raise AssertionError(f"ListRoles: {name!r} not found in roles list")


def AttachRolePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    iam.attach_role_policy(
        RoleName=name,
        PolicyArn="arn:aws:iam::aws:policy/ReadOnlyAccess",
    )


def ListAttachedRolePolicies(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    resp = iam.list_attached_role_policies(RoleName=name)
    arns = [p["PolicyArn"] for p in resp.get("AttachedPolicies", [])]
    if "arn:aws:iam::aws:policy/ReadOnlyAccess" not in arns:
        raise AssertionError(f"ListAttachedRolePolicies: ReadOnlyAccess not found in {arns}")


def DetachRolePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    iam.detach_role_policy(
        RoleName=name,
        PolicyArn="arn:aws:iam::aws:policy/ReadOnlyAccess",
    )


def CreateInstanceProfile(ctx: TestContext) -> None:
    iam = _iam(ctx)
    ip_name = f"{ctx.run_id}-ip"
    resp = iam.create_instance_profile(InstanceProfileName=ip_name)
    if not resp.get("InstanceProfile", {}).get("InstanceProfileName"):
        raise AssertionError("CreateInstanceProfile: missing InstanceProfileName")
    ctx["iam_instance_profile"] = ip_name


def AddRoleToInstanceProfile(ctx: TestContext) -> None:
    iam = _iam(ctx)
    role = ctx["iam_role_name"]
    ip = ctx["iam_instance_profile"]
    iam.add_role_to_instance_profile(InstanceProfileName=ip, RoleName=role)


def GetInstanceProfile(ctx: TestContext) -> None:
    iam = _iam(ctx)
    ip = ctx["iam_instance_profile"]
    resp = iam.get_instance_profile(InstanceProfileName=ip)
    if resp.get("InstanceProfile", {}).get("InstanceProfileName") != ip:
        raise AssertionError(f"GetInstanceProfile: wrong name")
    roles = resp["InstanceProfile"].get("Roles", [])
    role = ctx["iam_role_name"]
    if not any(r["RoleName"] == role for r in roles):
        raise AssertionError(f"GetInstanceProfile: role {role!r} not attached")


def DeleteRole(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-r-del"
    iam.create_role(RoleName=name, AssumeRolePolicyDocument=_ASSUME_POLICY)
    iam.delete_role(RoleName=name)
    resp = iam.list_roles()
    names = [r["RoleName"] for r in resp.get("Roles", [])]
    if name in names:
        raise AssertionError(f"DeleteRole: {name!r} still listed after deletion")


def PutRolePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    iam.put_role_policy(
        RoleName=name,
        PolicyName="inline-role-policy",
        PolicyDocument=_POLICY_DOC,
    )


def GetRolePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    resp = iam.get_role_policy(RoleName=name, PolicyName="inline-role-policy")
    if resp.get("PolicyName") != "inline-role-policy":
        raise AssertionError(f"GetRolePolicy: wrong name {resp.get('PolicyName')!r}")


def ListRolePolicies(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    resp = iam.list_role_policies(RoleName=name)
    names = resp.get("PolicyNames", [])
    if "inline-role-policy" not in names:
        raise AssertionError(f"ListRolePolicies: inline-role-policy not found in {names}")


def DeleteRolePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = ctx["iam_role_name"]
    iam.delete_role_policy(RoleName=name, PolicyName="inline-role-policy")


# ── iam-policies ──────────────────────────────────────────────────────────────

def setup_iam_policies(ctx: TestContext) -> None:
    ctx["iam_policy_arn"] = None


def teardown_iam_policies(ctx: TestContext) -> None:
    arn = ctx.get("iam_policy_arn")
    if arn:
        try:
            _iam(ctx).delete_policy(PolicyArn=arn)
        except Exception:
            pass


def CreatePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-policy"
    resp = iam.create_policy(
        PolicyName=name,
        PolicyDocument=_POLICY_DOC,
    )
    arn = resp.get("Policy", {}).get("Arn")
    if not arn:
        raise AssertionError("CreatePolicy: missing PolicyArn")
    ctx["iam_policy_arn"] = arn


def GetPolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    arn = ctx.get("iam_policy_arn")
    if not arn:
        raise AssertionError("GetPolicy: no policy ARN")
    resp = iam.get_policy(PolicyArn=arn)
    if resp.get("Policy", {}).get("Arn") != arn:
        raise AssertionError(f"GetPolicy: wrong ARN {resp.get('Policy', {}).get('Arn')!r}")


def ListPolicies(ctx: TestContext) -> None:
    iam = _iam(ctx)
    arn = ctx.get("iam_policy_arn")
    resp = iam.list_policies(Scope="Local")
    arns = [p["Arn"] for p in resp.get("Policies", [])]
    if arn and arn not in arns:
        raise AssertionError(f"ListPolicies: {arn!r} not found in local policies")


def DeletePolicy(ctx: TestContext) -> None:
    iam = _iam(ctx)
    arn = ctx.get("iam_policy_arn")
    if not arn:
        raise AssertionError("DeletePolicy: no policy to delete")
    iam.delete_policy(PolicyArn=arn)
    ctx["iam_policy_arn"] = None


# ── iam-groups ────────────────────────────────────────────────────────────────

def setup_iam_groups(ctx: TestContext) -> None:
    iam = _iam(ctx)
    group_name = f"{ctx.run_id}-group"
    user_name = f"{ctx.run_id}-guser"
    iam.create_group(GroupName=group_name)
    iam.create_user(UserName=user_name)
    ctx["iam_group_name"] = group_name
    ctx["iam_group_user"] = user_name


def teardown_iam_groups(ctx: TestContext) -> None:
    iam = _iam(ctx)
    user = ctx.get("iam_group_user")
    group = ctx.get("iam_group_name")
    if user and group:
        try:
            iam.remove_user_from_group(GroupName=group, UserName=user)
        except Exception:
            pass
    if group:
        try:
            iam.delete_group(GroupName=group)
        except Exception:
            pass
    if user:
        try:
            iam.delete_user(UserName=user)
        except Exception:
            pass


def CreateGroup(ctx: TestContext) -> None:
    if not ctx.get("iam_group_name"):
        raise AssertionError("CreateGroup: group not created in setup")


def AddUserToGroup(ctx: TestContext) -> None:
    iam = _iam(ctx)
    iam.add_user_to_group(
        GroupName=ctx["iam_group_name"],
        UserName=ctx["iam_group_user"],
    )


def ListGroupsForUser(ctx: TestContext) -> None:
    iam = _iam(ctx)
    user = ctx["iam_group_user"]
    group = ctx["iam_group_name"]
    resp = iam.list_groups_for_user(UserName=user)
    names = [g["GroupName"] for g in resp.get("Groups", [])]
    if group not in names:
        raise AssertionError(f"ListGroupsForUser: {group!r} not found in {names}")


def RemoveUserFromGroup(ctx: TestContext) -> None:
    iam = _iam(ctx)
    iam.remove_user_from_group(
        GroupName=ctx["iam_group_name"],
        UserName=ctx["iam_group_user"],
    )


def GetGroup(ctx: TestContext) -> None:
    iam = _iam(ctx)
    group = ctx["iam_group_name"]
    resp = iam.get_group(GroupName=group)
    if resp.get("Group", {}).get("GroupName") != group:
        raise AssertionError(f"GetGroup: wrong group name")


def DeleteGroup(ctx: TestContext) -> None:
    iam = _iam(ctx)
    name = f"{ctx.run_id}-grp-del"
    iam.create_group(GroupName=name)
    iam.delete_group(GroupName=name)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateUser": CreateUser,
    "GetUser": GetUser,
    "ListUsers": ListUsers,
    "CreateAccessKey": CreateAccessKey,
    "DeleteAccessKey": DeleteAccessKey,
    "PutUserPolicy": PutUserPolicy,
    "GetUserPolicy": GetUserPolicy,
    "DeleteUserPolicy": DeleteUserPolicy,
    "DeleteUser": DeleteUser,
    "UpdateUser": UpdateUser,
    "ListAccessKeys": ListAccessKeys,
    "CreateRole": CreateRole,
    "GetRole": GetRole,
    "ListRoles": ListRoles,
    "AttachRolePolicy": AttachRolePolicy,
    "ListAttachedRolePolicies": ListAttachedRolePolicies,
    "DetachRolePolicy": DetachRolePolicy,
    "PutRolePolicy": PutRolePolicy,
    "GetRolePolicy": GetRolePolicy,
    "ListRolePolicies": ListRolePolicies,
    "DeleteRolePolicy": DeleteRolePolicy,
    "CreateInstanceProfile": CreateInstanceProfile,
    "AddRoleToInstanceProfile": AddRoleToInstanceProfile,
    "GetInstanceProfile": GetInstanceProfile,
    "DeleteRole": DeleteRole,
    "CreatePolicy": CreatePolicy,
    "GetPolicy": GetPolicy,
    "ListPolicies": ListPolicies,
    "DeletePolicy": DeletePolicy,
    "CreateGroup": CreateGroup,
    "AddUserToGroup": AddUserToGroup,
    "ListGroupsForUser": ListGroupsForUser,
    "RemoveUserFromGroup": RemoveUserFromGroup,
    "GetGroup": GetGroup,
    "DeleteGroup": DeleteGroup,
}

SETUP = {
    "iam-users": setup_iam_users,
    "iam-roles": setup_iam_roles,
    "iam-policies": setup_iam_policies,
    "iam-groups": setup_iam_groups,
}

TEARDOWN = {
    "iam-users": teardown_iam_users,
    "iam-roles": teardown_iam_roles,
    "iam-policies": teardown_iam_policies,
    "iam-groups": teardown_iam_groups,
}
