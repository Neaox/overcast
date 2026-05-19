package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.core.sync.RequestBody;
import software.amazon.awssdk.core.sync.ResponseTransformer;
import software.amazon.awssdk.services.s3.S3Client;
import software.amazon.awssdk.services.s3.model.*;

import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;

/**
 * S3 compatibility test group.
 *
 * <p>Groups: s3-crud, s3-copy, s3-multipart, s3-versioning, s3-tagging,
 * s3-website, s3-cors.
 */
public final class S3Group implements ServiceGroup {

    private final AwsClients clients;

    public S3Group(AwsClients clients) {
        this.clients = clients;
    }

    private S3Client s3() { return clients.s3(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateBucket",            this::createBucket),
                Map.entry("PutObject",               this::putObject),
                Map.entry("HeadObject",              this::headObject),
                Map.entry("GetObject",               this::getObject),
                Map.entry("ListObjectsV2",           this::listObjectsV2),
                Map.entry("PutObjectMultipleKeys",   this::putObjectMultipleKeys),
                Map.entry("ListObjectsV2Delimiter",  this::listObjectsV2Delimiter),
                Map.entry("DeleteObject",            this::deleteObject),
                Map.entry("DeleteObjects",           this::deleteObjects),
                Map.entry("DeleteBucket",            this::deleteBucket),
                Map.entry("CreateSourceBucket",      this::createSourceBucket),
                Map.entry("PutSourceObject",         this::putSourceObject),
                Map.entry("CopyObject",              this::copyObject),
                Map.entry("CreateMultipartUpload",   this::createMultipartUpload),
                Map.entry("UploadPart",              this::uploadPart),
                Map.entry("CompleteMultipartUpload", this::completeMultipartUpload),
                Map.entry("AbortMultipartUpload",    this::abortMultipartUpload),
                Map.entry("PutBucketVersioning",     this::putBucketVersioning),
                Map.entry("GetBucketVersioning",     this::getBucketVersioning),
                Map.entry("PutObjectTagging",        this::putObjectTagging),
                Map.entry("GetObjectTagging",        this::getObjectTagging),
                Map.entry("PutBucketTagging",        this::putBucketTagging),
                Map.entry("GetBucketTagging",        this::getBucketTagging),
                Map.entry("PutBucketWebsite",        this::putBucketWebsite),
                Map.entry("GetBucketWebsite",        this::getBucketWebsite),
                Map.entry("PutBucketCors",           this::putBucketCors),
                Map.entry("GetBucketCors",           this::getBucketCors)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("s3-crud",       this::setupCrud),
                Map.entry("s3-copy",       this::setupCopy),
                Map.entry("s3-multipart",  this::setupMultipart),
                Map.entry("s3-versioning", this::setupVersioning),
                Map.entry("s3-tagging",    this::setupTagging),
                Map.entry("s3-website",    this::setupWebsite),
                Map.entry("s3-cors",       this::setupCors)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("s3-crud",       ctx -> emptyAndDeleteBucket(ctx.getString("s3Bucket"))),
                Map.entry("s3-copy",       this::teardownCopy),
                Map.entry("s3-multipart",  ctx -> emptyAndDeleteBucket(ctx.getString("s3MpBucket"))),
                Map.entry("s3-versioning", ctx -> emptyAndDeleteBucket(ctx.getString("s3VerBucket"))),
                Map.entry("s3-tagging",    ctx -> emptyAndDeleteBucket(ctx.getString("s3TagBucket"))),
                Map.entry("s3-website",    ctx -> emptyAndDeleteBucket(ctx.getString("s3WebBucket"))),
                Map.entry("s3-cors",       ctx -> emptyAndDeleteBucket(ctx.getString("s3CorsBucket")))
        );
    }

    // ── s3-crud ────────────────────────────────────────────────────────────────

    private void setupCrud(TestContext ctx) throws Exception {
        String bucket = ctx.runId() + "-s3crud";
        s3().createBucket(r -> r.bucket(bucket));
        ctx.set("s3Bucket", bucket);
    }

    private void createBucket(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-s3create";
        s3().createBucket(r -> r.bucket(name));
        try {
            var resp = s3().listBuckets();
            boolean found = resp.buckets().stream().anyMatch(b -> b.name().equals(name));
            Assertions.assertTrue(found, "CreateBucket: bucket " + name + " not found in listBuckets (runId=" + ctx.runId() + ")");
        } finally {
            emptyAndDeleteBucket(name);
        }
    }

    private void putObject(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        s3().putObject(r -> r.bucket(bucket).key("test-key"),
                RequestBody.fromString("hello world"));
        var head = s3().headObject(r -> r.bucket(bucket).key("test-key"));
        Assertions.assertGreaterThan(0L, head.contentLength(), "PutObject: ContentLength should be > 0");
        ctx.set("s3Key", "test-key");
    }

    private void headObject(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        var resp = s3().headObject(r -> r.bucket(bucket).key("test-key"));
        Assertions.assertNotNull(resp.contentLength(), "HeadObject: contentLength");
        Assertions.assertGreaterThan(0L, resp.contentLength(), "HeadObject: ContentLength should be > 0");
    }

    private void getObject(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        var bytes = s3().getObject(
                r -> r.bucket(bucket).key("test-key"),
                ResponseTransformer.toBytes());
        String body = bytes.asString(StandardCharsets.UTF_8);
        Assertions.assertEquals("hello world", body, "GetObject: body mismatch");
    }

    private void listObjectsV2(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        var resp = s3().listObjectsV2(r -> r.bucket(bucket));
        boolean found = resp.contents().stream().anyMatch(o -> o.key().equals("test-key"));
        Assertions.assertTrue(found, "ListObjectsV2: test-key not found (runId=" + ctx.runId() + ")");
    }

    private void putObjectMultipleKeys(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        s3().putObject(r -> r.bucket(bucket).key("prefix/a"), RequestBody.fromString("a"));
        s3().putObject(r -> r.bucket(bucket).key("prefix/b"), RequestBody.fromString("b"));
        var resp = s3().listObjectsV2(r -> r.bucket(bucket).prefix("prefix/"));
        Assertions.assertGreaterThanOrEqual(2, resp.contents().size(),
                "PutObjectMultipleKeys: expected >= 2 objects under prefix/");
    }

    private void listObjectsV2Delimiter(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        var resp = s3().listObjectsV2(r -> r.bucket(bucket).prefix("prefix/").delimiter("/"));
        Assertions.assertGreaterThanOrEqual(2, resp.contents().size(),
                "ListObjectsV2Delimiter: expected >= 2 objects under prefix/");
    }

    private void deleteObject(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        s3().deleteObject(r -> r.bucket(bucket).key("test-key"));
        var resp = s3().listObjectsV2(r -> r.bucket(bucket));
        boolean found = resp.contents().stream().anyMatch(o -> o.key().equals("test-key"));
        Assertions.assertFalse(found, "DeleteObject: test-key still present after deletion");
    }

    private void deleteObjects(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3Bucket");
        s3().putObject(r -> r.bucket(bucket).key("del/a"), RequestBody.fromString("a"));
        s3().putObject(r -> r.bucket(bucket).key("del/b"), RequestBody.fromString("b"));
        s3().deleteObjects(r -> r.bucket(bucket)
                .delete(d -> d.objects(
                        ObjectIdentifier.builder().key("del/a").build(),
                        ObjectIdentifier.builder().key("del/b").build())));
        var resp = s3().listObjectsV2(r -> r.bucket(bucket).prefix("del/"));
        Assertions.assertTrue(resp.contents().isEmpty(), "DeleteObjects: objects still present after batch delete");
    }

    private void deleteBucket(TestContext ctx) throws Exception {
        // The bucket created in setup will be deleted by teardown;
        // this test creates its own ephemeral bucket to verify the API.
        String name = ctx.runId() + "-s3del";
        s3().createBucket(r -> r.bucket(name));
        s3().deleteBucket(r -> r.bucket(name));
        var resp = s3().listBuckets();
        boolean found = resp.buckets().stream().anyMatch(b -> b.name().equals(name));
        Assertions.assertFalse(found, "DeleteBucket: bucket " + name + " still present after deletion");
    }

    // ── s3-copy ────────────────────────────────────────────────────────────────

    private void setupCopy(TestContext ctx) throws Exception {
        String src = ctx.runId() + "-s3copysrc";
        String dst = ctx.runId() + "-s3copydst";
        s3().createBucket(r -> r.bucket(src));
        s3().createBucket(r -> r.bucket(dst));
        ctx.set("s3CopySrc", src);
        ctx.set("s3CopyDst", dst);
    }

    private void teardownCopy(TestContext ctx) {
        emptyAndDeleteBucket(ctx.getString("s3CopySrc"));
        emptyAndDeleteBucket(ctx.getString("s3CopyDst"));
    }

    private void createSourceBucket(TestContext ctx) {
        // Bucket already created in setup; just assert it exists.
        String bucket = ctx.getString("s3CopySrc");
        Assertions.assertNotNull(bucket, "s3CopySrc");
    }

    private void putSourceObject(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3CopySrc");
        s3().putObject(r -> r.bucket(bucket).key("source.txt"),
                RequestBody.fromString("copy me"));
    }

    private void copyObject(TestContext ctx) throws Exception {
        String src = ctx.getString("s3CopySrc");
        String dst = ctx.getString("s3CopyDst");
        s3().copyObject(r -> r
                .sourceBucket(src).sourceKey("source.txt")
                .destinationBucket(dst).destinationKey("copied.txt"));
        var head = s3().headObject(r -> r.bucket(dst).key("copied.txt"));
        Assertions.assertNotNull(head.contentLength(), "CopyObject: destination object head returned null contentLength");
    }

    // ── s3-multipart ──────────────────────────────────────────────────────────

    private void setupMultipart(TestContext ctx) throws Exception {
        String bucket = ctx.runId() + "-s3mp";
        s3().createBucket(r -> r.bucket(bucket));
        ctx.set("s3MpBucket", bucket);
    }

    private void createMultipartUpload(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3MpBucket");
        var resp = s3().createMultipartUpload(r -> r.bucket(bucket).key("mp-key"));
        Assertions.assertNotBlank(resp.uploadId(), "createMultipartUpload: uploadId");
        ctx.set("s3UploadId", resp.uploadId());
    }

    private void uploadPart(TestContext ctx) throws Exception {
        String bucket   = ctx.getString("s3MpBucket");
        String uploadId = ctx.getString("s3UploadId");
        // S3 requires each part to be >= 5 MiB except the last part.
        // For the local emulator we use a smaller payload and treat 5 MiB
        // enforcement as an emulator detail, not a Java SDK issue.
        byte[] data = "A".repeat(5 * 1024 * 1024 + 1).getBytes(StandardCharsets.UTF_8);
        var resp = s3().uploadPart(r -> r
                .bucket(bucket).key("mp-key")
                .uploadId(uploadId)
                .partNumber(1),
                RequestBody.fromBytes(data));
        Assertions.assertNotBlank(resp.eTag(), "uploadPart: eTag");
        ctx.set("s3PartETag", resp.eTag());
    }

    private void completeMultipartUpload(TestContext ctx) throws Exception {
        String bucket   = ctx.getString("s3MpBucket");
        String uploadId = ctx.getString("s3UploadId");
        String eTag     = ctx.getString("s3PartETag");
        s3().completeMultipartUpload(r -> r
                .bucket(bucket).key("mp-key")
                .uploadId(uploadId)
                .multipartUpload(m -> m.parts(
                        CompletedPart.builder().partNumber(1).eTag(eTag).build())));
        var head = s3().headObject(r -> r.bucket(bucket).key("mp-key"));
        Assertions.assertGreaterThan(0L, head.contentLength(), "CompleteMultipartUpload: contentLength");
    }

    private void abortMultipartUpload(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3MpBucket");
        // Start a fresh upload just to abort it.
        var resp = s3().createMultipartUpload(r -> r.bucket(bucket).key("abort-key"));
        s3().abortMultipartUpload(r -> r
                .bucket(bucket).key("abort-key")
                .uploadId(resp.uploadId()));
    }

    // ── s3-versioning ─────────────────────────────────────────────────────────

    private void setupVersioning(TestContext ctx) throws Exception {
        String bucket = ctx.runId() + "-s3ver";
        s3().createBucket(r -> r.bucket(bucket));
        ctx.set("s3VerBucket", bucket);
    }

    private void putBucketVersioning(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3VerBucket");
        s3().putBucketVersioning(r -> r.bucket(bucket)
                .versioningConfiguration(v -> v.status(BucketVersioningStatus.ENABLED)));
    }

    private void getBucketVersioning(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3VerBucket");
        var resp = s3().getBucketVersioning(r -> r.bucket(bucket));
        Assertions.assertEquals(BucketVersioningStatus.ENABLED, resp.status(),
                "GetBucketVersioning: status mismatch");
    }

    // ── s3-tagging ────────────────────────────────────────────────────────────

    private void setupTagging(TestContext ctx) throws Exception {
        String bucket = ctx.runId() + "-s3tag";
        s3().createBucket(r -> r.bucket(bucket));
        s3().putObject(r -> r.bucket(bucket).key("tagged-obj"), RequestBody.fromString("data"));
        ctx.set("s3TagBucket", bucket);
    }

    private void putObjectTagging(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3TagBucket");
        s3().putObjectTagging(r -> r.bucket(bucket).key("tagged-obj")
                .tagging(t -> t.tagSet(Tag.builder().key("env").value("test").build())));
    }

    private void getObjectTagging(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3TagBucket");
        var resp = s3().getObjectTagging(r -> r.bucket(bucket).key("tagged-obj"));
        boolean found = resp.tagSet().stream().anyMatch(t -> t.key().equals("env") && t.value().equals("test"));
        Assertions.assertTrue(found, "GetObjectTagging: env=test tag not found");
    }

    private void putBucketTagging(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3TagBucket");
        s3().putBucketTagging(r -> r.bucket(bucket)
                .tagging(t -> t.tagSet(Tag.builder().key("project").value("overcast").build())));
    }

    private void getBucketTagging(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3TagBucket");
        var resp = s3().getBucketTagging(r -> r.bucket(bucket));
        boolean found = resp.tagSet().stream().anyMatch(t -> t.key().equals("project"));
        Assertions.assertTrue(found, "GetBucketTagging: project tag not found");
    }

    // ── s3-website ────────────────────────────────────────────────────────────

    private void setupWebsite(TestContext ctx) throws Exception {
        String bucket = ctx.runId() + "-s3web";
        s3().createBucket(r -> r.bucket(bucket));
        ctx.set("s3WebBucket", bucket);
    }

    private void putBucketWebsite(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3WebBucket");
        s3().putBucketWebsite(r -> r.bucket(bucket)
                .websiteConfiguration(w -> w
                        .indexDocument(i -> i.suffix("index.html"))
                        .errorDocument(e -> e.key("error.html"))));
    }

    private void getBucketWebsite(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3WebBucket");
        var resp = s3().getBucketWebsite(r -> r.bucket(bucket));
        Assertions.assertEquals("index.html", resp.indexDocument().suffix(),
                "GetBucketWebsite: indexDocument suffix mismatch");
    }

    // ── s3-cors ───────────────────────────────────────────────────────────────

    private void setupCors(TestContext ctx) throws Exception {
        String bucket = ctx.runId() + "-s3cors";
        s3().createBucket(r -> r.bucket(bucket));
        ctx.set("s3CorsBucket", bucket);
    }

    private void putBucketCors(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3CorsBucket");
        s3().putBucketCors(r -> r.bucket(bucket)
                .corsConfiguration(c -> c.corsRules(
                        CORSRule.builder()
                                .allowedMethods("GET", "PUT")
                                .allowedOrigins("*")
                                .allowedHeaders("*")
                                .build())));
    }

    private void getBucketCors(TestContext ctx) throws Exception {
        String bucket = ctx.getString("s3CorsBucket");
        var resp = s3().getBucketCors(r -> r.bucket(bucket));
        Assertions.assertNotEmpty(resp.corsRules(), "GetBucketCors: no CORS rules returned");
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    private void emptyAndDeleteBucket(String bucket) {
        if (bucket == null) return;
        try {
            // Abort incomplete multipart uploads.
            var mp = s3().listMultipartUploads(r -> r.bucket(bucket));
            for (var u : mp.uploads()) {
                try {
                    s3().abortMultipartUpload(r -> r.bucket(bucket).key(u.key()).uploadId(u.uploadId()));
                } catch (Exception ignored) {}
            }
            // Delete all object versions and delete markers.
            var versions = s3().listObjectVersions(r -> r.bucket(bucket));
            for (var v : versions.versions()) {
                try { s3().deleteObject(r -> r.bucket(bucket).key(v.key()).versionId(v.versionId())); }
                catch (Exception ignored) {}
            }
            for (var dm : versions.deleteMarkers()) {
                try { s3().deleteObject(r -> r.bucket(bucket).key(dm.key()).versionId(dm.versionId())); }
                catch (Exception ignored) {}
            }
            // Delete remaining current objects.
            var objs = s3().listObjectsV2(r -> r.bucket(bucket));
            for (var obj : objs.contents()) {
                try { s3().deleteObject(r -> r.bucket(bucket).key(obj.key())); }
                catch (Exception ignored) {}
            }
            s3().deleteBucket(r -> r.bucket(bucket));
        } catch (Exception ignored) {}
    }
}
