"""
groups/cloudfront.py — CloudFront compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _cloudfront(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("cloudfront")


# ── cloudfront-distributions ─────────────────────────────────────────────────

def CreateDistribution(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_distribution(
        DistributionConfig={
            "CallerReference": f"compat-{ctx.run_id}",
            "Comment": "compat test distribution",
            "Enabled": True,
            "Origins": {
                "Quantity": 1,
                "Items": [
                    {
                        "Id": "origin-1",
                        "DomainName": "example.com",
                        "S3OriginConfig": {"OriginAccessIdentity": ""},
                    },
                ],
            },
            "DefaultCacheBehavior": {
                "TargetOriginId": "origin-1",
                "ViewerProtocolPolicy": "redirect-to-https",
                "ForwardedValues": {
                    "QueryString": False,
                    "Cookies": {"Forward": "none"},
                },
                "MinTTL": 0,
                "TrustedSigners": {"Enabled": False, "Quantity": 0},
            },
        },
    )
    distro = resp.get("Distribution", {})
    if not distro.get("Id"):
        raise AssertionError("CreateDistribution: missing Id")
    ctx["cf_distro_id"] = distro["Id"]
    ctx["cf_distro_etag"] = resp.get("ETag", "")


def GetDistribution(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    distro_id = ctx.get("cf_distro_id")
    if not distro_id:
        raise AssertionError("GetDistribution: no distribution from CreateDistribution")
    resp = cf.get_distribution(Id=distro_id)
    if not resp.get("Distribution", {}).get("Id"):
        raise AssertionError("GetDistribution: missing Id")


def ListDistributions(ctx: TestContext) -> None:
    _cloudfront(ctx).list_distributions()


def DeleteDistribution(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    distro_id = ctx.get("cf_distro_id")
    if not distro_id:
        return
    # Must disable before deleting.
    resp = cf.get_distribution(Id=distro_id)
    etag = resp.get("ETag", "")
    config = resp["Distribution"]["DistributionConfig"]
    config["Enabled"] = False
    upd = cf.update_distribution(Id=distro_id, IfMatch=etag, DistributionConfig=config)
    new_etag = upd.get("ETag", "")
    cf.delete_distribution(Id=distro_id, IfMatch=new_etag)


# ── cloudfront-oac ───────────────────────────────────────────────────────────

def CreateOriginAccessControl(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_origin_access_control(
        OriginAccessControlConfig={
            "Name": f"oc-oac-{ctx.run_id}",
            "OriginAccessControlOriginType": "s3",
            "SigningBehavior": "always",
            "SigningProtocol": "sigv4",
        }
    )
    oac = resp.get("OriginAccessControl", {})
    if not oac.get("Id"):
        raise AssertionError("CreateOriginAccessControl: missing Id")
    ctx["oac_id"] = oac["Id"]
    ctx["oac_etag"] = resp.get("ETag", "")


def GetOriginAccessControl(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    oac_id = ctx.get("oac_id")
    if not oac_id:
        raise AssertionError("GetOriginAccessControl: no OAC from Create")
    resp = cf.get_origin_access_control(Id=oac_id)
    if not resp.get("OriginAccessControl", {}).get("Id"):
        raise AssertionError("GetOriginAccessControl: missing Id")


def UpdateOriginAccessControl(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    oac_id = ctx.get("oac_id")
    etag = ctx.get("oac_etag")
    if not oac_id or not etag:
        raise AssertionError("UpdateOriginAccessControl: missing prerequisite")
    resp = cf.update_origin_access_control(
        Id=oac_id,
        IfMatch=etag,
        OriginAccessControlConfig={
            "Name": f"oc-oac-{ctx.run_id}",
            "OriginAccessControlOriginType": "s3",
            "SigningBehavior": "never",
            "SigningProtocol": "sigv4",
        },
    )
    ctx["oac_etag"] = resp.get("ETag", "")


def ListOriginAccessControls(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_origin_access_controls()
    if resp.get("OriginAccessControlList") is None:
        raise AssertionError("ListOriginAccessControls: missing list")


def DeleteOriginAccessControl(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    oac_id = ctx.get("oac_id")
    etag = ctx.get("oac_etag")
    if not oac_id or not etag:
        return
    cf.delete_origin_access_control(Id=oac_id, IfMatch=etag)


# ── cloudfront-cache-policy ──────────────────────────────────────────────────

def CreateCachePolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_cache_policy(
        CachePolicyConfig={
            "Name": f"oc-cp-{ctx.run_id}",
            "MinTTL": 0,
            "DefaultTTL": 86400,
            "MaxTTL": 31536000,
            "ParametersInCacheKeyAndForwardedToOrigin": {
                "CookiesConfig": {"CookieBehavior": "none"},
                "EnableAcceptEncodingGzip": False,
                "HeadersConfig": {"HeaderBehavior": "none"},
                "QueryStringsConfig": {"QueryStringBehavior": "none"},
            },
        }
    )
    cp = resp.get("CachePolicy", {})
    if not cp.get("Id"):
        raise AssertionError("CreateCachePolicy: missing Id")
    ctx["cp_id"] = cp["Id"]
    ctx["cp_etag"] = resp.get("ETag", "")


def GetCachePolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cp_id = ctx.get("cp_id")
    if not cp_id:
        raise AssertionError("GetCachePolicy: no cp_id")
    resp = cf.get_cache_policy(Id=cp_id)
    if not resp.get("CachePolicy", {}).get("Id"):
        raise AssertionError("GetCachePolicy: missing Id")


def GetCachePolicyConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cp_id = ctx.get("cp_id")
    if not cp_id:
        raise AssertionError("GetCachePolicyConfig: no cp_id")
    resp = cf.get_cache_policy_config(Id=cp_id)
    if resp.get("CachePolicyConfig") is None:
        raise AssertionError("GetCachePolicyConfig: missing config")


def UpdateCachePolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cp_id = ctx.get("cp_id")
    etag = ctx.get("cp_etag")
    if not cp_id or not etag:
        raise AssertionError("UpdateCachePolicy: missing prerequisite")
    resp = cf.update_cache_policy(
        Id=cp_id,
        IfMatch=etag,
        CachePolicyConfig={
            "Name": f"oc-cp-{ctx.run_id}",
            "MinTTL": 0,
            "DefaultTTL": 3600,
            "MaxTTL": 86400,
            "ParametersInCacheKeyAndForwardedToOrigin": {
                "CookiesConfig": {"CookieBehavior": "none"},
                "EnableAcceptEncodingGzip": True,
                "HeadersConfig": {"HeaderBehavior": "none"},
                "QueryStringsConfig": {"QueryStringBehavior": "none"},
            },
        },
    )
    ctx["cp_etag"] = resp.get("ETag", "")


def ListCachePolicies(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_cache_policies()
    if resp.get("CachePolicyList") is None:
        raise AssertionError("ListCachePolicies: missing list")


def DeleteCachePolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cp_id = ctx.get("cp_id")
    etag = ctx.get("cp_etag")
    if not cp_id or not etag:
        return
    cf.delete_cache_policy(Id=cp_id, IfMatch=etag)


# ── cloudfront-key-group ─────────────────────────────────────────────────────

def CreateKeyGroup(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_key_group(
        KeyGroupConfig={
            "Name": f"oc-kg-{ctx.run_id}",
            "Items": ["K1234567890ABCDE"],
        }
    )
    kg = resp.get("KeyGroup", {})
    if not kg.get("Id"):
        raise AssertionError("CreateKeyGroup: missing Id")
    ctx["kg_id"] = kg["Id"]
    ctx["kg_etag"] = resp.get("ETag", "")


def GetKeyGroup(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    kg_id = ctx.get("kg_id")
    if not kg_id:
        raise AssertionError("GetKeyGroup: no kg_id")
    resp = cf.get_key_group(Id=kg_id)
    if not resp.get("KeyGroup", {}).get("Id"):
        raise AssertionError("GetKeyGroup: missing Id")


def GetKeyGroupConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    kg_id = ctx.get("kg_id")
    if not kg_id:
        raise AssertionError("GetKeyGroupConfig: no kg_id")
    resp = cf.get_key_group_config(Id=kg_id)
    if resp.get("KeyGroupConfig") is None:
        raise AssertionError("GetKeyGroupConfig: missing config")


def UpdateKeyGroup(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    kg_id = ctx.get("kg_id")
    etag = ctx.get("kg_etag")
    if not kg_id or not etag:
        raise AssertionError("UpdateKeyGroup: missing prerequisite")
    resp = cf.update_key_group(
        Id=kg_id,
        IfMatch=etag,
        KeyGroupConfig={
            "Name": f"oc-kg-{ctx.run_id}",
            "Comment": "updated",
            "Items": ["K1234567890ABCDE"],
        },
    )
    ctx["kg_etag"] = resp.get("ETag", "")


def ListKeyGroups(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_key_groups()
    if resp.get("KeyGroupList") is None:
        raise AssertionError("ListKeyGroups: missing list")


def DeleteKeyGroup(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    kg_id = ctx.get("kg_id")
    etag = ctx.get("kg_etag")
    if not kg_id or not etag:
        return
    cf.delete_key_group(Id=kg_id, IfMatch=etag)


# ── cloudfront-realtime-log ──────────────────────────────────────────────────

def CreateRealtimeLogConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    name = f"oc-rlc-{ctx.run_id}"
    resp = cf.create_realtime_log_config(
        Name=name,
        SamplingRate=100,
        EndPoints=[
            {
                "StreamType": "Kinesis",
                "KinesisStreamConfig": {
                    "RoleARN": "arn:aws:iam::000000000000:role/test",
                    "StreamARN": "arn:aws:kinesis:us-east-1:000000000000:stream/test",
                },
            }
        ],
        Fields=["timestamp", "c-ip"],
    )
    rlc = resp.get("RealtimeLogConfig", {})
    if not rlc.get("ARN"):
        raise AssertionError("CreateRealtimeLogConfig: missing ARN")
    ctx["rlc_name"] = name
    ctx["rlc_arn"] = rlc["ARN"]


def GetRealtimeLogConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    name = ctx.get("rlc_name")
    if not name:
        raise AssertionError("GetRealtimeLogConfig: no rlc_name")
    resp = cf.get_realtime_log_config(Name=name)
    if not resp.get("RealtimeLogConfig", {}).get("ARN"):
        raise AssertionError("GetRealtimeLogConfig: missing ARN")


def UpdateRealtimeLogConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    name = ctx.get("rlc_name")
    arn = ctx.get("rlc_arn")
    if not name or not arn:
        raise AssertionError("UpdateRealtimeLogConfig: missing prerequisite")
    cf.update_realtime_log_config(
        Name=name,
        ARN=arn,
        SamplingRate=50,
        EndPoints=[
            {
                "StreamType": "Kinesis",
                "KinesisStreamConfig": {
                    "RoleARN": "arn:aws:iam::000000000000:role/test",
                    "StreamARN": "arn:aws:kinesis:us-east-1:000000000000:stream/test",
                },
            }
        ],
        Fields=["timestamp", "c-ip", "sc-status"],
    )


def ListRealtimeLogConfigs(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_realtime_log_configs()
    if resp.get("RealtimeLogConfigs") is None:
        raise AssertionError("ListRealtimeLogConfigs: missing list")


def DeleteRealtimeLogConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    name = ctx.get("rlc_name")
    if not name:
        return
    cf.delete_realtime_log_config(Name=name)


# ── cloudfront-monitoring ────────────────────────────────────────────────────

def CreateMonitoringSubscription(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    dist_id = ctx.get("mon_distro_id")
    if not dist_id:
        raise AssertionError("CreateMonitoringSubscription: no mon_distro_id")
    cf.create_monitoring_subscription(
        DistributionId=dist_id,
        MonitoringSubscription={
            "RealtimeMetricsSubscriptionConfig": {
                "RealtimeMetricsSubscriptionStatus": "Enabled",
            }
        },
    )


def GetMonitoringSubscription(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    dist_id = ctx.get("mon_distro_id")
    if not dist_id:
        raise AssertionError("GetMonitoringSubscription: no mon_distro_id")
    resp = cf.get_monitoring_subscription(DistributionId=dist_id)
    if resp.get("MonitoringSubscription") is None:
        raise AssertionError("GetMonitoringSubscription: missing subscription")


def DeleteMonitoringSubscription(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    dist_id = ctx.get("mon_distro_id")
    if not dist_id:
        return
    cf.delete_monitoring_subscription(DistributionId=dist_id)


# ── cloudfront-fle-config ───────────────────────────────────────────────────

def CreateFieldLevelEncryptionConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_field_level_encryption_config(
        FieldLevelEncryptionConfig={
            "CallerReference": f"oc-fle-{ctx.run_id}",
            "Comment": "compat test",
        }
    )
    fle = resp.get("FieldLevelEncryption", {})
    if not fle.get("Id"):
        raise AssertionError("CreateFieldLevelEncryptionConfig: missing Id")
    ctx["fle_id"] = fle["Id"]
    ctx["fle_etag"] = resp.get("ETag", "")


def GetFieldLevelEncryption(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    fle_id = ctx.get("fle_id")
    if not fle_id:
        raise AssertionError("GetFieldLevelEncryption: no fle_id")
    resp = cf.get_field_level_encryption(Id=fle_id)
    if not resp.get("FieldLevelEncryption", {}).get("Id"):
        raise AssertionError("GetFieldLevelEncryption: missing Id")


def GetFieldLevelEncryptionConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    fle_id = ctx.get("fle_id")
    if not fle_id:
        raise AssertionError("GetFieldLevelEncryptionConfig: no fle_id")
    resp = cf.get_field_level_encryption_config(Id=fle_id)
    if resp.get("FieldLevelEncryptionConfig") is None:
        raise AssertionError("GetFieldLevelEncryptionConfig: missing config")


def UpdateFieldLevelEncryptionConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    fle_id = ctx.get("fle_id")
    etag = ctx.get("fle_etag")
    if not fle_id or not etag:
        raise AssertionError("UpdateFieldLevelEncryptionConfig: missing prerequisite")
    resp = cf.update_field_level_encryption_config(
        Id=fle_id,
        IfMatch=etag,
        FieldLevelEncryptionConfig={
            "CallerReference": f"oc-fle-{ctx.run_id}",
            "Comment": "compat test updated",
        },
    )
    ctx["fle_etag"] = resp.get("ETag", "")


def ListFieldLevelEncryptionConfigs(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_field_level_encryption_configs()
    if resp.get("FieldLevelEncryptionList") is None:
        raise AssertionError("ListFieldLevelEncryptionConfigs: missing list")


def DeleteFieldLevelEncryption(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    fle_id = ctx.get("fle_id")
    etag = ctx.get("fle_etag")
    if not fle_id or not etag:
        return
    cf.delete_field_level_encryption_config(Id=fle_id, IfMatch=etag)


# ── cloudfront-fle-profile ──────────────────────────────────────────────────

def CreateFieldLevelEncryptionProfile(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_field_level_encryption_profile(
        FieldLevelEncryptionProfileConfig={
            "CallerReference": f"oc-flep-{ctx.run_id}",
            "Name": f"oc-flep-{ctx.run_id}",
            "Comment": "compat test",
            "EncryptionEntities": {"Quantity": 0, "Items": []},
        }
    )
    prof = resp.get("FieldLevelEncryptionProfile", {})
    if not prof.get("Id"):
        raise AssertionError("CreateFieldLevelEncryptionProfile: missing Id")
    ctx["flep_id"] = prof["Id"]
    ctx["flep_etag"] = resp.get("ETag", "")


def GetFieldLevelEncryptionProfile(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    flep_id = ctx.get("flep_id")
    if not flep_id:
        raise AssertionError("GetFieldLevelEncryptionProfile: no flep_id")
    resp = cf.get_field_level_encryption_profile(Id=flep_id)
    if not resp.get("FieldLevelEncryptionProfile", {}).get("Id"):
        raise AssertionError("GetFieldLevelEncryptionProfile: missing Id")


def GetFieldLevelEncryptionProfileConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    flep_id = ctx.get("flep_id")
    if not flep_id:
        raise AssertionError("GetFieldLevelEncryptionProfileConfig: no flep_id")
    resp = cf.get_field_level_encryption_profile_config(Id=flep_id)
    if resp.get("FieldLevelEncryptionProfileConfig") is None:
        raise AssertionError("GetFieldLevelEncryptionProfileConfig: missing config")


def UpdateFieldLevelEncryptionProfile(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    flep_id = ctx.get("flep_id")
    etag = ctx.get("flep_etag")
    if not flep_id or not etag:
        raise AssertionError("UpdateFieldLevelEncryptionProfile: missing prerequisite")
    resp = cf.update_field_level_encryption_profile(
        Id=flep_id,
        IfMatch=etag,
        FieldLevelEncryptionProfileConfig={
            "CallerReference": f"oc-flep-{ctx.run_id}",
            "Name": f"oc-flep-{ctx.run_id}",
            "Comment": "compat test updated",
            "EncryptionEntities": {"Quantity": 0, "Items": []},
        },
    )
    ctx["flep_etag"] = resp.get("ETag", "")


def ListFieldLevelEncryptionProfiles(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_field_level_encryption_profiles()
    if resp.get("FieldLevelEncryptionProfileList") is None:
        raise AssertionError("ListFieldLevelEncryptionProfiles: missing list")


def DeleteFieldLevelEncryptionProfile(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    flep_id = ctx.get("flep_id")
    etag = ctx.get("flep_etag")
    if not flep_id or not etag:
        return
    cf.delete_field_level_encryption_profile(Id=flep_id, IfMatch=etag)


# ── cloudfront-continuous-deployment ─────────────────────────────────────────

def CreateContinuousDeploymentPolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_continuous_deployment_policy(
        ContinuousDeploymentPolicyConfig={
            "StagingDistributionDnsNames": {
                "Quantity": 1,
                "Items": ["d1234.cloudfront.net"],
            },
            "Enabled": True,
        }
    )
    cdp = resp.get("ContinuousDeploymentPolicy", {})
    if not cdp.get("Id"):
        raise AssertionError("CreateContinuousDeploymentPolicy: missing Id")
    ctx["cdp_id"] = cdp["Id"]
    ctx["cdp_etag"] = resp.get("ETag", "")


def GetContinuousDeploymentPolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cdp_id = ctx.get("cdp_id")
    if not cdp_id:
        raise AssertionError("GetContinuousDeploymentPolicy: no cdp_id")
    resp = cf.get_continuous_deployment_policy(Id=cdp_id)
    if not resp.get("ContinuousDeploymentPolicy", {}).get("Id"):
        raise AssertionError("GetContinuousDeploymentPolicy: missing Id")


def GetContinuousDeploymentPolicyConfig(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cdp_id = ctx.get("cdp_id")
    if not cdp_id:
        raise AssertionError("GetContinuousDeploymentPolicyConfig: no cdp_id")
    resp = cf.get_continuous_deployment_policy_config(Id=cdp_id)
    if resp.get("ContinuousDeploymentPolicyConfig") is None:
        raise AssertionError("GetContinuousDeploymentPolicyConfig: missing config")


def UpdateContinuousDeploymentPolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cdp_id = ctx.get("cdp_id")
    etag = ctx.get("cdp_etag")
    if not cdp_id or not etag:
        raise AssertionError("UpdateContinuousDeploymentPolicy: missing prerequisite")
    resp = cf.update_continuous_deployment_policy(
        Id=cdp_id,
        IfMatch=etag,
        ContinuousDeploymentPolicyConfig={
            "StagingDistributionDnsNames": {
                "Quantity": 1,
                "Items": ["d5678.cloudfront.net"],
            },
            "Enabled": False,
        },
    )
    ctx["cdp_etag"] = resp.get("ETag", "")


def ListContinuousDeploymentPolicies(ctx: TestContext) -> None:
    resp = _cloudfront(ctx).list_continuous_deployment_policies()
    if resp.get("ContinuousDeploymentPolicyList") is None:
        raise AssertionError("ListContinuousDeploymentPolicies: missing list")


def DeleteContinuousDeploymentPolicy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cdp_id = ctx.get("cdp_id")
    etag = ctx.get("cdp_etag")
    if not cdp_id or not etag:
        return
    cf.delete_continuous_deployment_policy(Id=cdp_id, IfMatch=etag)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    # Distributions
    "CreateDistribution": CreateDistribution,
    "GetDistribution": GetDistribution,
    "ListDistributions": ListDistributions,
    "DeleteDistribution": DeleteDistribution,
    # OAC
    "CreateOriginAccessControl": CreateOriginAccessControl,
    "GetOriginAccessControl": GetOriginAccessControl,
    "UpdateOriginAccessControl": UpdateOriginAccessControl,
    "ListOriginAccessControls": ListOriginAccessControls,
    "DeleteOriginAccessControl": DeleteOriginAccessControl,
    # Cache Policy
    "CreateCachePolicy": CreateCachePolicy,
    "GetCachePolicy": GetCachePolicy,
    "GetCachePolicyConfig": GetCachePolicyConfig,
    "UpdateCachePolicy": UpdateCachePolicy,
    "ListCachePolicies": ListCachePolicies,
    "DeleteCachePolicy": DeleteCachePolicy,
    # Key Group
    "CreateKeyGroup": CreateKeyGroup,
    "GetKeyGroup": GetKeyGroup,
    "GetKeyGroupConfig": GetKeyGroupConfig,
    "UpdateKeyGroup": UpdateKeyGroup,
    "ListKeyGroups": ListKeyGroups,
    "DeleteKeyGroup": DeleteKeyGroup,
    # Realtime Log
    "CreateRealtimeLogConfig": CreateRealtimeLogConfig,
    "GetRealtimeLogConfig": GetRealtimeLogConfig,
    "UpdateRealtimeLogConfig": UpdateRealtimeLogConfig,
    "ListRealtimeLogConfigs": ListRealtimeLogConfigs,
    "DeleteRealtimeLogConfig": DeleteRealtimeLogConfig,
    # Monitoring
    "CreateMonitoringSubscription": CreateMonitoringSubscription,
    "GetMonitoringSubscription": GetMonitoringSubscription,
    "DeleteMonitoringSubscription": DeleteMonitoringSubscription,
    # FLE Config
    "CreateFieldLevelEncryptionConfig": CreateFieldLevelEncryptionConfig,
    "GetFieldLevelEncryption": GetFieldLevelEncryption,
    "GetFieldLevelEncryptionConfig": GetFieldLevelEncryptionConfig,
    "UpdateFieldLevelEncryptionConfig": UpdateFieldLevelEncryptionConfig,
    "ListFieldLevelEncryptionConfigs": ListFieldLevelEncryptionConfigs,
    "DeleteFieldLevelEncryption": DeleteFieldLevelEncryption,
    # FLE Profile
    "CreateFieldLevelEncryptionProfile": CreateFieldLevelEncryptionProfile,
    "GetFieldLevelEncryptionProfile": GetFieldLevelEncryptionProfile,
    "GetFieldLevelEncryptionProfileConfig": GetFieldLevelEncryptionProfileConfig,
    "UpdateFieldLevelEncryptionProfile": UpdateFieldLevelEncryptionProfile,
    "ListFieldLevelEncryptionProfiles": ListFieldLevelEncryptionProfiles,
    "DeleteFieldLevelEncryptionProfile": DeleteFieldLevelEncryptionProfile,
    # Continuous Deployment
    "CreateContinuousDeploymentPolicy": CreateContinuousDeploymentPolicy,
    "GetContinuousDeploymentPolicy": GetContinuousDeploymentPolicy,
    "GetContinuousDeploymentPolicyConfig": GetContinuousDeploymentPolicyConfig,
    "UpdateContinuousDeploymentPolicy": UpdateContinuousDeploymentPolicy,
    "ListContinuousDeploymentPolicies": ListContinuousDeploymentPolicies,
    "DeleteContinuousDeploymentPolicy": DeleteContinuousDeploymentPolicy,
}

SETUP = {
    "cloudfront-monitoring": lambda ctx: _setup_monitoring(ctx),
}

TEARDOWN = {
    "cloudfront-distributions": lambda ctx: _teardown_distribution(ctx),
    "cloudfront-oac": lambda ctx: _teardown_oac(ctx),
    "cloudfront-cache-policy": lambda ctx: _teardown_cache_policy(ctx),
    "cloudfront-key-group": lambda ctx: _teardown_key_group(ctx),
    "cloudfront-realtime-log": lambda ctx: _teardown_realtime_log(ctx),
    "cloudfront-monitoring": lambda ctx: _teardown_monitoring(ctx),
    "cloudfront-fle-config": lambda ctx: _teardown_fle_config(ctx),
    "cloudfront-fle-profile": lambda ctx: _teardown_fle_profile(ctx),
    "cloudfront-continuous-deployment": lambda ctx: _teardown_cdp(ctx),
}


def _setup_monitoring(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    resp = cf.create_distribution(
        DistributionConfig={
            "CallerReference": f"compat-mon-{ctx.run_id}",
            "Comment": "compat monitoring test distribution",
            "Enabled": True,
            "Origins": {
                "Quantity": 1,
                "Items": [
                    {
                        "Id": "origin-1",
                        "DomainName": "example.com",
                        "S3OriginConfig": {"OriginAccessIdentity": ""},
                    },
                ],
            },
            "DefaultCacheBehavior": {
                "TargetOriginId": "origin-1",
                "ViewerProtocolPolicy": "redirect-to-https",
                "ForwardedValues": {
                    "QueryString": False,
                    "Cookies": {"Forward": "none"},
                },
                "MinTTL": 0,
                "TrustedSigners": {"Enabled": False, "Quantity": 0},
            },
        }
    )
    ctx["mon_distro_id"] = resp.get("Distribution", {}).get("Id", "")


def _teardown_distribution(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    distro_id = ctx.get("cf_distro_id")
    if not distro_id:
        return
    try:
        resp = cf.get_distribution(Id=distro_id)
        etag = resp.get("ETag", "")
        config = resp["Distribution"]["DistributionConfig"]
        if config.get("Enabled"):
            config["Enabled"] = False
            upd = cf.update_distribution(Id=distro_id, IfMatch=etag, DistributionConfig=config)
            etag = upd.get("ETag", "")
        cf.delete_distribution(Id=distro_id, IfMatch=etag)
    except Exception:
        pass


def _teardown_oac(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    oac_id = ctx.get("oac_id")
    if not oac_id:
        return
    try:
        resp = cf.get_origin_access_control(Id=oac_id)
        cf.delete_origin_access_control(Id=oac_id, IfMatch=resp.get("ETag", ""))
    except Exception:
        pass


def _teardown_cache_policy(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cp_id = ctx.get("cp_id")
    if not cp_id:
        return
    try:
        resp = cf.get_cache_policy(Id=cp_id)
        cf.delete_cache_policy(Id=cp_id, IfMatch=resp.get("ETag", ""))
    except Exception:
        pass


def _teardown_key_group(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    kg_id = ctx.get("kg_id")
    if not kg_id:
        return
    try:
        resp = cf.get_key_group(Id=kg_id)
        cf.delete_key_group(Id=kg_id, IfMatch=resp.get("ETag", ""))
    except Exception:
        pass


def _teardown_realtime_log(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    name = ctx.get("rlc_name")
    if not name:
        return
    try:
        cf.delete_realtime_log_config(Name=name)
    except Exception:
        pass


def _teardown_monitoring(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    dist_id = ctx.get("mon_distro_id")
    if not dist_id:
        return
    try:
        cf.delete_monitoring_subscription(DistributionId=dist_id)
    except Exception:
        pass
    try:
        resp = cf.get_distribution(Id=dist_id)
        etag = resp.get("ETag", "")
        config = resp["Distribution"]["DistributionConfig"]
        if config.get("Enabled"):
            config["Enabled"] = False
            upd = cf.update_distribution(Id=dist_id, IfMatch=etag, DistributionConfig=config)
            etag = upd.get("ETag", "")
        cf.delete_distribution(Id=dist_id, IfMatch=etag)
    except Exception:
        pass


def _teardown_fle_config(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    fle_id = ctx.get("fle_id")
    if not fle_id:
        return
    try:
        resp = cf.get_field_level_encryption(Id=fle_id)
        cf.delete_field_level_encryption_config(Id=fle_id, IfMatch=resp.get("ETag", ""))
    except Exception:
        pass


def _teardown_fle_profile(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    flep_id = ctx.get("flep_id")
    if not flep_id:
        return
    try:
        resp = cf.get_field_level_encryption_profile(Id=flep_id)
        cf.delete_field_level_encryption_profile(Id=flep_id, IfMatch=resp.get("ETag", ""))
    except Exception:
        pass


def _teardown_cdp(ctx: TestContext) -> None:
    cf = _cloudfront(ctx)
    cdp_id = ctx.get("cdp_id")
    if not cdp_id:
        return
    try:
        resp = cf.get_continuous_deployment_policy(Id=cdp_id)
        cf.delete_continuous_deployment_policy(Id=cdp_id, IfMatch=resp.get("ETag", ""))
    except Exception:
        pass
