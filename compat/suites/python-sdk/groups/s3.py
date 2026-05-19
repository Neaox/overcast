"""
groups/s3.py — S3 compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import io
from lib.harness import TestContext
from lib.clients import make_clients


def _s3(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).s3


# ── s3-crud ──────────────────────────────────────────────────────────────────

def setup_s3_crud(ctx: TestContext) -> None:
    bucket = f"{ctx.run_id}-s3-crud"
    _s3(ctx).create_bucket(Bucket=bucket)
    ctx["s3_crud_bucket"] = bucket


def teardown_s3_crud(ctx: TestContext) -> None:
    bucket = ctx.get("s3_crud_bucket")
    if not bucket:
        return
    s3 = _s3(ctx)
    try:
        paginator = s3.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=bucket):
            objects = [{"Key": o["Key"]} for o in page.get("Contents", [])]
            if objects:
                s3.delete_objects(Bucket=bucket, Delete={"Objects": objects})
    except Exception:
        pass
    try:
        s3.delete_bucket(Bucket=bucket)
    except Exception:
        pass


def CreateBucket(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = f"{ctx.run_id}-s3-create"
    s3.create_bucket(Bucket=bucket)
    try:
        resp = s3.list_buckets()
        names = [b["Name"] for b in resp.get("Buckets", [])]
        if bucket not in names:
            raise AssertionError(f"bucket {bucket} not found after CreateBucket")
    finally:
        s3.delete_bucket(Bucket=bucket)


def PutObject(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    resp = s3.put_object(Bucket=bucket, Key="hello.txt", Body=b"hello world")
    if resp["ResponseMetadata"]["HTTPStatusCode"] not in (200, 201):
        raise AssertionError("PutObject: unexpected status")
    if not resp.get("ETag"):
        raise AssertionError("PutObject: missing ETag in response")


def HeadObject(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    resp = s3.head_object(Bucket=bucket, Key="hello.txt")
    if resp["ContentLength"] != len(b"hello world"):
        raise AssertionError(f"HeadObject: wrong ContentLength {resp['ContentLength']}")


def GetObject(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    resp = s3.get_object(Bucket=bucket, Key="hello.txt")
    body = resp["Body"].read()
    if body != b"hello world":
        raise AssertionError(f"GetObject: wrong body {body!r}")


def ListObjectsV2(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    resp = s3.list_objects_v2(Bucket=bucket)
    keys = [o["Key"] for o in resp.get("Contents", [])]
    if "hello.txt" not in keys:
        raise AssertionError(f"ListObjectsV2: hello.txt not found; got {keys}")


def PutObjectMultipleKeys(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    for i in range(3):
        s3.put_object(Bucket=bucket, Key=f"multi/key-{i}.txt", Body=f"value {i}".encode())
    resp = s3.list_objects_v2(Bucket=bucket, Prefix="multi/")
    count = resp.get("KeyCount", 0)
    if count < 3:
        raise AssertionError(f"PutObjectMultipleKeys: expected ≥3 objects, got {count}")


def ListObjectsV2Delimiter(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    resp = s3.list_objects_v2(Bucket=bucket, Delimiter="/")
    prefixes = [p["Prefix"] for p in resp.get("CommonPrefixes", [])]
    if "multi/" not in prefixes:
        raise AssertionError(f"ListObjectsV2Delimiter: expected 'multi/' prefix; got {prefixes}")


def DeleteObject(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    s3.delete_object(Bucket=bucket, Key="hello.txt")
    resp = s3.list_objects_v2(Bucket=bucket, Prefix="hello.txt")
    if resp.get("KeyCount", 0) != 0:
        raise AssertionError("DeleteObject: object still present after delete")


def DeleteObjects(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_crud_bucket"]
    resp = s3.delete_objects(
        Bucket=bucket,
        Delete={"Objects": [{"Key": f"multi/key-{i}.txt"} for i in range(3)]},
    )
    if resp.get("Errors"):
        raise AssertionError(f"DeleteObjects: errors {resp['Errors']}")
    list_resp = s3.list_objects_v2(Bucket=bucket, Prefix="multi/")
    if list_resp.get("KeyCount", 0) != 0:
        raise AssertionError("DeleteObjects: objects still present after bulk delete")


def DeleteBucket(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = f"{ctx.run_id}-s3-del"
    s3.create_bucket(Bucket=bucket)
    s3.delete_bucket(Bucket=bucket)
    resp = s3.list_buckets()
    names = [b["Name"] for b in resp.get("Buckets", [])]
    if bucket in names:
        raise AssertionError(f"DeleteBucket: {bucket} still listed after deletion")


# ── s3-copy ───────────────────────────────────────────────────────────────────

def setup_s3_copy(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    src = f"{ctx.run_id}-s3-copy-src"
    dst = f"{ctx.run_id}-s3-copy-dst"
    s3.create_bucket(Bucket=src)
    s3.create_bucket(Bucket=dst)
    ctx["s3_copy_src"] = src
    ctx["s3_copy_dst"] = dst


def teardown_s3_copy(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    for key in ("s3_copy_src", "s3_copy_dst"):
        bucket = ctx.get(key)
        if not bucket:
            continue
        try:
            paginator = s3.get_paginator("list_objects_v2")
            for page in paginator.paginate(Bucket=bucket):
                objects = [{"Key": o["Key"]} for o in page.get("Contents", [])]
                if objects:
                    s3.delete_objects(Bucket=bucket, Delete={"Objects": objects})
            s3.delete_bucket(Bucket=bucket)
        except Exception:
            pass


def CreateSourceBucket(ctx: TestContext) -> None:
    # Buckets created in setup; this is a no-op validation step
    if not ctx.get("s3_copy_src"):
        raise AssertionError("s3_copy_src not set by setup")


def PutSourceObject(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    src = ctx["s3_copy_src"]
    s3.put_object(Bucket=src, Key="source.txt", Body=b"copy me")


def CopyObject(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    src = ctx["s3_copy_src"]
    dst = ctx["s3_copy_dst"]
    s3.copy_object(
        Bucket=dst, Key="dest.txt",
        CopySource={"Bucket": src, "Key": "source.txt"},
    )
    resp = s3.get_object(Bucket=dst, Key="dest.txt")
    body = resp["Body"].read()
    if body != b"copy me":
        raise AssertionError(f"CopyObject: wrong body {body!r}")


# ── s3-multipart ──────────────────────────────────────────────────────────────

def setup_s3_multipart(ctx: TestContext) -> None:
    bucket = f"{ctx.run_id}-s3-mp"
    _s3(ctx).create_bucket(Bucket=bucket)
    ctx["s3_mp_bucket"] = bucket


def teardown_s3_multipart(ctx: TestContext) -> None:
    bucket = ctx.get("s3_mp_bucket")
    if not bucket:
        return
    s3 = _s3(ctx)
    try:
        # Abort any incomplete multipart uploads first.
        paginator = s3.get_paginator("list_multipart_uploads")
        for page in paginator.paginate(Bucket=bucket):
            for upload in page.get("Uploads", []):
                try:
                    s3.abort_multipart_upload(
                        Bucket=bucket,
                        Key=upload["Key"],
                        UploadId=upload["UploadId"],
                    )
                except Exception:
                    pass
        paginator = s3.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=bucket):
            objects = [{"Key": o["Key"]} for o in page.get("Contents", [])]
            if objects:
                s3.delete_objects(Bucket=bucket, Delete={"Objects": objects})
        s3.delete_bucket(Bucket=bucket)
    except Exception:
        pass


def CreateMultipartUpload(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_mp_bucket"]
    resp = s3.create_multipart_upload(Bucket=bucket, Key="multipart.bin")
    if not resp.get("UploadId"):
        raise AssertionError("CreateMultipartUpload: missing UploadId")
    ctx["mp_upload_id"] = resp["UploadId"]
    ctx["mp_parts"] = []


def UploadPart(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_mp_bucket"]
    upload_id = ctx["mp_upload_id"]
    # Minimum part size is 5 MiB; part 1 only needs to be ≥5 MiB for real AWS
    # but Overcast accepts any size.
    data = b"a" * (5 * 1024 * 1024 + 1)
    resp = s3.upload_part(
        Bucket=bucket, Key="multipart.bin",
        UploadId=upload_id, PartNumber=1, Body=data,
    )
    if not resp.get("ETag"):
        raise AssertionError("UploadPart: missing ETag")
    ctx["mp_parts"].append({"PartNumber": 1, "ETag": resp["ETag"]})


def CompleteMultipartUpload(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_mp_bucket"]
    upload_id = ctx["mp_upload_id"]
    parts = ctx["mp_parts"]
    resp = s3.complete_multipart_upload(
        Bucket=bucket, Key="multipart.bin",
        UploadId=upload_id,
        MultipartUpload={"Parts": parts},
    )
    if not resp.get("Key"):
        raise AssertionError("CompleteMultipartUpload: missing Key in response")
    ctx["mp_upload_id"] = None  # mark completed


def AbortMultipartUpload(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_mp_bucket"]
    # Start a fresh upload to abort
    resp = s3.create_multipart_upload(Bucket=bucket, Key="to-abort.bin")
    upload_id = resp["UploadId"]
    s3.abort_multipart_upload(Bucket=bucket, Key="to-abort.bin", UploadId=upload_id)


# ── s3-versioning ─────────────────────────────────────────────────────────────

def setup_s3_versioning(ctx: TestContext) -> None:
    bucket = f"{ctx.run_id}-s3-ver"
    _s3(ctx).create_bucket(Bucket=bucket)
    ctx["s3_ver_bucket"] = bucket


def teardown_s3_versioning(ctx: TestContext) -> None:
    bucket = ctx.get("s3_ver_bucket")
    if not bucket:
        return
    s3 = _s3(ctx)
    try:
        paginator = s3.get_paginator("list_object_versions")
        for page in paginator.paginate(Bucket=bucket):
            versions = [{"Key": v["Key"], "VersionId": v["VersionId"]}
                        for v in page.get("Versions", [])]
            delete_markers = [{"Key": m["Key"], "VersionId": m["VersionId"]}
                              for m in page.get("DeleteMarkers", [])]
            objects = versions + delete_markers
            if objects:
                s3.delete_objects(Bucket=bucket, Delete={"Objects": objects})
        s3.delete_bucket(Bucket=bucket)
    except Exception:
        pass


def PutBucketVersioning(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_ver_bucket"]
    s3.put_bucket_versioning(
        Bucket=bucket,
        VersioningConfiguration={"Status": "Enabled"},
    )
    resp = s3.get_bucket_versioning(Bucket=bucket)
    assert resp["Status"] == "Enabled", f"PutBucketVersioning: expected Enabled, got {resp.get('Status')}"


def GetBucketVersioning(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_ver_bucket"]
    resp = s3.get_bucket_versioning(Bucket=bucket)
    if resp.get("Status") != "Enabled":
        raise AssertionError(f"GetBucketVersioning: expected Enabled, got {resp.get('Status')!r}")


# ── s3-tagging ────────────────────────────────────────────────────────────────

def setup_s3_tagging(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = f"{ctx.run_id}-s3-tag"
    s3.create_bucket(Bucket=bucket)
    s3.put_object(Bucket=bucket, Key="tagged.txt", Body=b"tag me")
    ctx["s3_tag_bucket"] = bucket


def teardown_s3_tagging(ctx: TestContext) -> None:
    bucket = ctx.get("s3_tag_bucket")
    if not bucket:
        return
    s3 = _s3(ctx)
    try:
        s3.delete_object(Bucket=bucket, Key="tagged.txt")
        s3.delete_bucket(Bucket=bucket)
    except Exception:
        pass


def PutObjectTagging(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_tag_bucket"]
    s3.put_object_tagging(
        Bucket=bucket, Key="tagged.txt",
        Tagging={"TagSet": [{"Key": "env", "Value": "compat"}]},
    )
    resp = s3.get_object_tagging(Bucket=bucket, Key="tagged.txt")
    tags = {t["Key"]: t["Value"] for t in resp.get("TagSet", [])}
    assert tags.get("env") == "compat", f"PutObjectTagging: env tag not found, got {tags}"


def GetObjectTagging(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_tag_bucket"]
    resp = s3.get_object_tagging(Bucket=bucket, Key="tagged.txt")
    tags = {t["Key"]: t["Value"] for t in resp.get("TagSet", [])}
    if tags.get("env") != "compat":
        raise AssertionError(f"GetObjectTagging: expected env=compat, got {tags}")


def PutBucketTagging(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_tag_bucket"]
    s3.put_bucket_tagging(
        Bucket=bucket,
        Tagging={"TagSet": [{"Key": "project", "Value": "overcast"}]},
    )
    resp = s3.get_bucket_tagging(Bucket=bucket)
    tags = {t["Key"]: t["Value"] for t in resp.get("TagSet", [])}
    assert tags.get("project") == "overcast", f"PutBucketTagging: project tag not found, got {tags}"


def GetBucketTagging(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_tag_bucket"]
    resp = s3.get_bucket_tagging(Bucket=bucket)
    tags = {t["Key"]: t["Value"] for t in resp.get("TagSet", [])}
    if tags.get("project") != "overcast":
        raise AssertionError(f"GetBucketTagging: expected project=overcast, got {tags}")


# ── s3-website ────────────────────────────────────────────────────────────────

def setup_s3_website(ctx: TestContext) -> None:
    bucket = f"{ctx.run_id}-s3-web"
    _s3(ctx).create_bucket(Bucket=bucket)
    ctx["s3_web_bucket"] = bucket


def teardown_s3_website(ctx: TestContext) -> None:
    bucket = ctx.get("s3_web_bucket")
    if not bucket:
        return
    try:
        _s3(ctx).delete_bucket(Bucket=bucket)
    except Exception:
        pass


def PutBucketWebsite(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_web_bucket"]
    s3.put_bucket_website(
        Bucket=bucket,
        WebsiteConfiguration={
            "IndexDocument": {"Suffix": "index.html"},
            "ErrorDocument": {"Key": "error.html"},
        },
    )
    resp = s3.get_bucket_website(Bucket=bucket)
    assert resp["IndexDocument"]["Suffix"] == "index.html", "PutBucketWebsite: IndexDocument.Suffix mismatch"


def GetBucketWebsite(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_web_bucket"]
    resp = s3.get_bucket_website(Bucket=bucket)
    if resp.get("IndexDocument", {}).get("Suffix") != "index.html":
        raise AssertionError(f"GetBucketWebsite: unexpected config {resp}")


# ── s3-cors ───────────────────────────────────────────────────────────────────

def setup_s3_cors(ctx: TestContext) -> None:
    bucket = f"{ctx.run_id}-s3-cors"
    _s3(ctx).create_bucket(Bucket=bucket)
    ctx["s3_cors_bucket"] = bucket


def teardown_s3_cors(ctx: TestContext) -> None:
    bucket = ctx.get("s3_cors_bucket")
    if not bucket:
        return
    try:
        _s3(ctx).delete_bucket(Bucket=bucket)
    except Exception:
        pass


def PutBucketCors(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_cors_bucket"]
    s3.put_bucket_cors(
        Bucket=bucket,
        CORSConfiguration={
            "CORSRules": [{
                "AllowedHeaders": ["*"],
                "AllowedMethods": ["GET", "PUT"],
                "AllowedOrigins": ["https://example.com"],
                "ExposeHeaders": ["ETag"],
                "MaxAgeSeconds": 3600,
            }],
        },
    )
    resp = s3.get_bucket_cors(Bucket=bucket)
    assert len(resp.get("CORSRules", [])) >= 1, "PutBucketCors: expected >=1 CORS rule"


def GetBucketCors(ctx: TestContext) -> None:
    s3 = _s3(ctx)
    bucket = ctx["s3_cors_bucket"]
    resp = s3.get_bucket_cors(Bucket=bucket)
    rules = resp.get("CORSRules", [])
    if not rules:
        raise AssertionError("GetBucketCors: no rules returned")
    if "GET" not in rules[0].get("AllowedMethods", []):
        raise AssertionError(f"GetBucketCors: expected GET in AllowedMethods; got {rules[0]}")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateBucket": CreateBucket,
    "PutObject": PutObject,
    "HeadObject": HeadObject,
    "GetObject": GetObject,
    "ListObjectsV2": ListObjectsV2,
    "PutObjectMultipleKeys": PutObjectMultipleKeys,
    "ListObjectsV2Delimiter": ListObjectsV2Delimiter,
    "DeleteObject": DeleteObject,
    "DeleteObjects": DeleteObjects,
    "DeleteBucket": DeleteBucket,
    "CreateSourceBucket": CreateSourceBucket,
    "PutSourceObject": PutSourceObject,
    "CopyObject": CopyObject,
    "CreateMultipartUpload": CreateMultipartUpload,
    "UploadPart": UploadPart,
    "CompleteMultipartUpload": CompleteMultipartUpload,
    "AbortMultipartUpload": AbortMultipartUpload,
    "PutBucketVersioning": PutBucketVersioning,
    "GetBucketVersioning": GetBucketVersioning,
    "PutObjectTagging": PutObjectTagging,
    "GetObjectTagging": GetObjectTagging,
    "PutBucketTagging": PutBucketTagging,
    "GetBucketTagging": GetBucketTagging,
    "PutBucketWebsite": PutBucketWebsite,
    "GetBucketWebsite": GetBucketWebsite,
    "PutBucketCors": PutBucketCors,
    "GetBucketCors": GetBucketCors,
}

SETUP = {
    "s3-crud": setup_s3_crud,
    "s3-copy": setup_s3_copy,
    "s3-multipart": setup_s3_multipart,
    "s3-versioning": setup_s3_versioning,
    "s3-tagging": setup_s3_tagging,
    "s3-website": setup_s3_website,
    "s3-cors": setup_s3_cors,
}

TEARDOWN = {
    "s3-crud": teardown_s3_crud,
    "s3-copy": teardown_s3_copy,
    "s3-multipart": teardown_s3_multipart,
    "s3-versioning": teardown_s3_versioning,
    "s3-tagging": teardown_s3_tagging,
    "s3-website": teardown_s3_website,
    "s3-cors": teardown_s3_cors,
}
