"""
groups/kms.py — KMS compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import base64
from lib.harness import TestContext
from lib.clients import make_clients


def _kms(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).kms


# ── kms-keys ──────────────────────────────────────────────────────────────────

def setup_kms_keys(ctx: TestContext) -> None:
    kms = _kms(ctx)
    resp = kms.create_key(Description=f"compat-test {ctx.run_id}")
    ctx["kms_key_id"] = resp["KeyMetadata"]["KeyId"]
    ctx["kms_key_arn"] = resp["KeyMetadata"]["Arn"]


def teardown_kms_keys(ctx: TestContext) -> None:
    key_id = ctx.get("kms_key_id")
    if key_id:
        kms = _kms(ctx)
        # Delete alias first — it outlives the key otherwise.
        alias = ctx.get("kms_alias")
        if alias:
            try:
                kms.delete_alias(AliasName=alias)
            except Exception:
                pass
        try:
            kms.schedule_key_deletion(KeyId=key_id, PendingWindowInDays=7)
        except Exception:
            pass


def CreateKey(ctx: TestContext) -> None:
    kms = _kms(ctx)
    resp = kms.create_key(Description=f"compat temp {ctx.run_id}")
    key_id = resp["KeyMetadata"]["KeyId"]
    try:
        if not key_id:
            raise AssertionError("CreateKey: missing KeyId")
    finally:
        try:
            kms.schedule_key_deletion(KeyId=key_id, PendingWindowInDays=7)
        except Exception:
            pass


def DescribeKey(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    resp = kms.describe_key(KeyId=key_id)
    if resp["KeyMetadata"]["KeyId"] != key_id:
        raise AssertionError(f"DescribeKey: wrong KeyId {resp['KeyMetadata']['KeyId']!r}")


def CreateAlias(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    alias = f"alias/{ctx.run_id}-compat"
    kms.create_alias(AliasName=alias, TargetKeyId=key_id)
    ctx["kms_alias"] = alias
    resp = kms.list_aliases()
    names = [a["AliasName"] for a in resp.get("Aliases", [])]
    assert alias in names, f"CreateAlias: alias {alias} not found in list"


def ListAliases(ctx: TestContext) -> None:
    kms = _kms(ctx)
    alias = ctx.get("kms_alias")
    resp = kms.list_aliases()
    names = [a["AliasName"] for a in resp.get("Aliases", [])]
    if alias and alias not in names:
        raise AssertionError(f"ListAliases: {alias!r} not found in aliases")


def ListKeys(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    resp = kms.list_keys()
    key_ids = [k["KeyId"] for k in resp.get("Keys", [])]
    if key_id not in key_ids:
        raise AssertionError(f"ListKeys: {key_id!r} not found in key list")


def DisableKey(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    kms.disable_key(KeyId=key_id)
    resp = kms.describe_key(KeyId=key_id)
    if resp["KeyMetadata"]["KeyState"] != "Disabled":
        raise AssertionError(f"DisableKey: expected Disabled, got {resp['KeyMetadata']['KeyState']!r}")


def EnableKey(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    kms.enable_key(KeyId=key_id)
    resp = kms.describe_key(KeyId=key_id)
    if resp["KeyMetadata"]["KeyState"] != "Enabled":
        raise AssertionError(f"EnableKey: expected Enabled, got {resp['KeyMetadata']['KeyState']!r}")


def ScheduleKeyDeletion(ctx: TestContext) -> None:
    kms = _kms(ctx)
    # Create a fresh key to schedule for deletion
    resp = kms.create_key(Description="to-delete")
    key_id = resp["KeyMetadata"]["KeyId"]
    kms.schedule_key_deletion(KeyId=key_id, PendingWindowInDays=7)
    desc = kms.describe_key(KeyId=key_id)
    if desc["KeyMetadata"]["KeyState"] != "PendingDeletion":
        raise AssertionError(f"ScheduleKeyDeletion: expected PendingDeletion, got {desc['KeyMetadata']['KeyState']!r}")
    ctx["kms_pending_key_id"] = key_id


def CancelKeyDeletion(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx.get("kms_pending_key_id")
    if not key_id:
        raise AssertionError("CancelKeyDeletion: no pending key")
    kms.cancel_key_deletion(KeyId=key_id)
    desc = kms.describe_key(KeyId=key_id)
    state = desc["KeyMetadata"]["KeyState"]
    assert state == "Disabled", f"CancelKeyDeletion: expected Disabled, got {state}"
    try:
        kms.schedule_key_deletion(KeyId=key_id, PendingWindowInDays=7)
    except Exception:
        pass


def TagKMSResource(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    kms.tag_resource(
        KeyId=key_id,
        Tags=[{"TagKey": "env", "TagValue": "test"}],
    )


def ListKMSResourceTags(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    resp = kms.list_resource_tags(KeyId=key_id)
    tags = resp.get("Tags", [])
    found = any(t["TagKey"] == "env" for t in tags)
    if not found:
        raise AssertionError("ListKMSResourceTags: tag 'env' not found")


def UntagKMSResource(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_key_id"]
    kms.untag_resource(KeyId=key_id, TagKeys=["env"])


# ── kms-crypto ────────────────────────────────────────────────────────────────

def setup_kms_crypto(ctx: TestContext) -> None:
    kms = _kms(ctx)
    resp = kms.create_key(Description=f"crypto-test {ctx.run_id}")
    ctx["kms_crypto_key_id"] = resp["KeyMetadata"]["KeyId"]


def teardown_kms_crypto(ctx: TestContext) -> None:
    key_id = ctx.get("kms_crypto_key_id")
    if key_id:
        try:
            _kms(ctx).schedule_key_deletion(KeyId=key_id, PendingWindowInDays=7)
        except Exception:
            pass


def Encrypt(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_crypto_key_id"]
    resp = kms.encrypt(KeyId=key_id, Plaintext=b"hello kms")
    if not resp.get("CiphertextBlob"):
        raise AssertionError("Encrypt: missing CiphertextBlob")
    ctx["kms_ciphertext"] = resp["CiphertextBlob"]


def Decrypt(ctx: TestContext) -> None:
    kms = _kms(ctx)
    ciphertext = ctx.get("kms_ciphertext")
    if not ciphertext:
        raise AssertionError("Decrypt: no ciphertext available")
    resp = kms.decrypt(CiphertextBlob=ciphertext)
    plaintext = resp.get("Plaintext", b"")
    if plaintext != b"hello kms":
        raise AssertionError(f"Decrypt: expected b'hello kms', got {plaintext!r}")


def GenerateDataKey(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_crypto_key_id"]
    resp = kms.generate_data_key(KeyId=key_id, KeySpec="AES_256")
    if not resp.get("Plaintext"):
        raise AssertionError("GenerateDataKey: missing Plaintext")
    if not resp.get("CiphertextBlob"):
        raise AssertionError("GenerateDataKey: missing CiphertextBlob")


def GenerateDataKeyWithoutPlaintext(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_crypto_key_id"]
    resp = kms.generate_data_key_without_plaintext(KeyId=key_id, KeySpec="AES_256")
    if not resp.get("CiphertextBlob"):
        raise AssertionError("GenerateDataKeyWithoutPlaintext: missing CiphertextBlob")
    if resp.get("Plaintext"):
        raise AssertionError("GenerateDataKeyWithoutPlaintext: should not return Plaintext")


# ── kms-asymmetric ────────────────────────────────────────────────────────────

def setup_kms_asymmetric(ctx: TestContext) -> None:
    kms = _kms(ctx)
    resp = kms.create_key(
        KeySpec="RSA_2048",
        KeyUsage="SIGN_VERIFY",
        Description=f"asymmetric {ctx.run_id}",
    )
    ctx["kms_asym_key_id"] = resp["KeyMetadata"]["KeyId"]


def teardown_kms_asymmetric(ctx: TestContext) -> None:
    key_id = ctx.get("kms_asym_key_id")
    if key_id:
        try:
            _kms(ctx).schedule_key_deletion(KeyId=key_id, PendingWindowInDays=7)
        except Exception:
            pass


def Sign(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_asym_key_id"]
    resp = kms.sign(
        KeyId=key_id,
        Message=b"message to sign",
        MessageType="RAW",
        SigningAlgorithm="RSASSA_PKCS1_V1_5_SHA_256",
    )
    if not resp.get("Signature"):
        raise AssertionError("Sign: missing Signature")
    ctx["kms_signature"] = resp["Signature"]


def Verify(ctx: TestContext) -> None:
    kms = _kms(ctx)
    key_id = ctx["kms_asym_key_id"]
    sig = ctx.get("kms_signature")
    if not sig:
        raise AssertionError("Verify: no signature available")
    resp = kms.verify(
        KeyId=key_id,
        Message=b"message to sign",
        MessageType="RAW",
        Signature=sig,
        SigningAlgorithm="RSASSA_PKCS1_V1_5_SHA_256",
    )
    if not resp.get("SignatureValid"):
        raise AssertionError("Verify: signature not valid")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateKey": CreateKey,
    "DescribeKey": DescribeKey,
    "CreateKmsAlias": CreateAlias,
    "ListKmsAliases": ListAliases,
    "ListKeys": ListKeys,
    "DisableKey": DisableKey,
    "EnableKey": EnableKey,
    "ScheduleKeyDeletion": ScheduleKeyDeletion,
    "CancelKeyDeletion": CancelKeyDeletion,
    "TagKMSResource": TagKMSResource,
    "ListKMSResourceTags": ListKMSResourceTags,
    "UntagKMSResource": UntagKMSResource,
    "Encrypt": Encrypt,
    "Decrypt": Decrypt,
    "GenerateDataKey": GenerateDataKey,
    "GenerateDataKeyWithoutPlaintext": GenerateDataKeyWithoutPlaintext,
    "Sign": Sign,
    "Verify": Verify,
}

SETUP = {
    "kms-keys": setup_kms_keys,
    "kms-crypto": setup_kms_crypto,
    "kms-asymmetric": setup_kms_asymmetric,
}

TEARDOWN = {
    "kms-keys": teardown_kms_keys,
    "kms-crypto": teardown_kms_crypto,
    "kms-asymmetric": teardown_kms_asymmetric,
}
