using Amazon.S3.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class S3Group(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["CreateBucket"] = CreateBucketAsync,
        ["PutObject"] = PutObjectAsync,
        ["HeadObject"] = HeadObjectAsync,
        ["GetObject"] = GetObjectAsync,
        ["ListObjectsV2"] = ListObjectsV2Async,
        ["PutObjectMultipleKeys"] = PutObjectMultipleKeysAsync,
        ["ListObjectsV2Delimiter"] = ListObjectsV2DelimiterAsync,
        ["DeleteObject"] = DeleteObjectAsync,
        ["DeleteObjects"] = DeleteObjectsAsync,
        ["DeleteBucket"] = DeleteBucketAsync,
        ["CreateSourceBucket"] = CreateSourceBucketAsync,
        ["PutSourceObject"] = PutSourceObjectAsync,
        ["CopyObject"] = CopyObjectAsync,
        ["CreateMultipartUpload"] = CreateMultipartUploadAsync,
        ["UploadPart"] = UploadPartAsync,
        ["CompleteMultipartUpload"] = CompleteMultipartUploadAsync,
        ["AbortMultipartUpload"] = AbortMultipartUploadAsync,
        ["PutBucketVersioning"] = PutBucketVersioningAsync,
        ["GetBucketVersioning"] = GetBucketVersioningAsync,
        ["PutObjectTagging"] = PutObjectTaggingAsync,
        ["GetObjectTagging"] = GetObjectTaggingAsync,
        ["PutBucketTagging"] = PutBucketTaggingAsync,
        ["GetBucketTagging"] = GetBucketTaggingAsync,
        ["PutBucketWebsite"] = PutBucketWebsiteAsync,
        ["GetBucketWebsite"] = GetBucketWebsiteAsync,
        ["PutBucketCors"] = PutBucketCorsAsync,
        ["GetBucketCors"] = GetBucketCorsAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["s3-crud"] = SetupCrudAsync,
        ["s3-copy"] = SetupCopyAsync,
        ["s3-multipart"] = SetupMultipartAsync,
        ["s3-versioning"] = SetupVersioningAsync,
        ["s3-tagging"] = SetupTaggingAsync,
        ["s3-website"] = SetupWebsiteAsync,
        ["s3-cors"] = SetupCorsAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["s3-crud"] = context => EmptyAndDeleteBucketAsync(context.GetString("s3Bucket")),
        ["s3-copy"] = async context =>
        {
            await EmptyAndDeleteBucketAsync(context.GetString("s3CopySrc"));
            await EmptyAndDeleteBucketAsync(context.GetString("s3CopyDst"));
        },
        ["s3-multipart"] = TeardownMultipartAsync,
        ["s3-versioning"] = TeardownVersioningAsync,
        ["s3-tagging"] = context => EmptyAndDeleteBucketAsync(context.GetString("s3TagBucket")),
        ["s3-website"] = context => EmptyAndDeleteBucketAsync(context.GetString("s3WebBucket")),
        ["s3-cors"] = context => EmptyAndDeleteBucketAsync(context.GetString("s3CorsBucket")),
    };

    private async Task SetupCrudAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3crud";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        context.Set("s3Bucket", bucket);
    }

    private async Task SetupCopyAsync(TestContext context)
    {
        var src = $"{context.RunId}-s3-copy-src";
        var dst = $"{context.RunId}-s3-copy-dst";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = src });
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = dst });
        context.Set("s3CopySrc", src);
        context.Set("s3CopyDst", dst);
    }

    private async Task SetupMultipartAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3-mp";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        context.Set("s3MpBucket", bucket);
    }

    private async Task SetupVersioningAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3-ver";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        context.Set("s3VerBucket", bucket);
    }

    private async Task SetupTaggingAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3-tag";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        await clients.S3().PutObjectAsync(new PutObjectRequest { BucketName = bucket, Key = "test-key", ContentBody = "tagged" });
        context.Set("s3TagBucket", bucket);
    }

    private async Task SetupWebsiteAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3-web";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        context.Set("s3WebBucket", bucket);
    }

    private async Task SetupCorsAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3-cors";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        context.Set("s3CorsBucket", bucket);
    }

    private async Task CreateBucketAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3create";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        try
        {
            var response = await clients.S3().ListBucketsAsync();
            Assertions.True(response.Buckets.Any(item => item.BucketName == bucket), $"CreateBucket: bucket {bucket} not found in ListBuckets (runId={context.RunId})");
        }
        finally
        {
            await EmptyAndDeleteBucketAsync(bucket);
        }
    }

    private async Task PutObjectAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        await clients.S3().PutObjectAsync(new PutObjectRequest
        {
            BucketName = bucket,
            Key = "test-key",
            ContentBody = "hello world",
        });
        var head = await clients.S3().GetObjectMetadataAsync(new GetObjectMetadataRequest
        {
            BucketName = bucket,
            Key = "test-key",
        });
        Assertions.GreaterThan(0, head.Headers.ContentLength, "PutObject: ContentLength should be > 0");
    }

    private async Task HeadObjectAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        var response = await clients.S3().GetObjectMetadataAsync(new GetObjectMetadataRequest
        {
            BucketName = bucket,
            Key = "test-key",
        });
        Assertions.GreaterThan(0, response.Headers.ContentLength, "HeadObject: ContentLength should be > 0");
    }

    private async Task GetObjectAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        using var response = await clients.S3().GetObjectAsync(new GetObjectRequest
        {
            BucketName = bucket,
            Key = "test-key",
        });
        using var reader = new StreamReader(response.ResponseStream);
        var body = await reader.ReadToEndAsync();
        Assertions.Equal("hello world", body, "GetObject: body mismatch");
    }

    private async Task ListObjectsV2Async(TestContext context)
    {
        var bucket = RequireBucket(context);
        var response = await clients.S3().ListObjectsV2Async(new ListObjectsV2Request
        {
            BucketName = bucket,
        });
        Assertions.True(response.S3Objects.Any(item => item.Key == "test-key"), $"ListObjectsV2: test-key not found (runId={context.RunId})");
    }

    private async Task PutObjectMultipleKeysAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        await clients.S3().PutObjectAsync(new PutObjectRequest { BucketName = bucket, Key = "prefix/a", ContentBody = "a" });
        await clients.S3().PutObjectAsync(new PutObjectRequest { BucketName = bucket, Key = "prefix/b", ContentBody = "b" });
        var response = await clients.S3().ListObjectsV2Async(new ListObjectsV2Request
        {
            BucketName = bucket,
            Prefix = "prefix/",
        });
        Assertions.GreaterThanOrEqual(2, response.S3Objects.Count, "PutObjectMultipleKeys: expected >= 2 objects under prefix/");
    }

    private async Task ListObjectsV2DelimiterAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        var response = await clients.S3().ListObjectsV2Async(new ListObjectsV2Request
        {
            BucketName = bucket,
            Prefix = "prefix/",
            Delimiter = "/",
        });
        Assertions.GreaterThanOrEqual(2, response.S3Objects.Count, "ListObjectsV2Delimiter: expected >= 2 objects under prefix/");
    }

    private async Task DeleteObjectAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        await clients.S3().DeleteObjectAsync(new DeleteObjectRequest
        {
            BucketName = bucket,
            Key = "test-key",
        });
        var response = await clients.S3().ListObjectsV2Async(new ListObjectsV2Request { BucketName = bucket });
        Assertions.False(response.S3Objects.Any(item => item.Key == "test-key"), "DeleteObject: test-key still present after deletion");
    }

    private async Task DeleteObjectsAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        await clients.S3().PutObjectAsync(new PutObjectRequest { BucketName = bucket, Key = "del/a", ContentBody = "a" });
        await clients.S3().PutObjectAsync(new PutObjectRequest { BucketName = bucket, Key = "del/b", ContentBody = "b" });
        await clients.S3().DeleteObjectsAsync(new DeleteObjectsRequest
        {
            BucketName = bucket,
            Objects = [new KeyVersion { Key = "del/a" }, new KeyVersion { Key = "del/b" }],
        });
        var response = await clients.S3().ListObjectsV2Async(new ListObjectsV2Request { BucketName = bucket, Prefix = "del/" });
        Assertions.True((response.S3Objects?.Count ?? 0) == 0, "DeleteObjects: objects still present after batch delete");
    }

    private async Task DeleteBucketAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3del";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        await clients.S3().DeleteBucketAsync(new DeleteBucketRequest { BucketName = bucket });
        var response = await clients.S3().ListBucketsAsync();
        Assertions.False(response.Buckets.Any(item => item.BucketName == bucket), $"DeleteBucket: bucket {bucket} still present after deletion");
    }

    private async Task CreateSourceBucketAsync(TestContext context)
    {
        var src = $"{context.RunId}-s3-copy-src";
        var dst = $"{context.RunId}-s3-copy-dst";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = src });
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = dst });
        try
        {
            var response = await clients.S3().ListBucketsAsync();
            Assertions.True(response.Buckets.Any(b => b.BucketName == src) && response.Buckets.Any(b => b.BucketName == dst), $"CreateSourceBucket: buckets not found in ListBuckets (runId={context.RunId})");
        }
        finally
        {
            await EmptyAndDeleteBucketAsync(src);
            await EmptyAndDeleteBucketAsync(dst);
        }
    }

    private async Task PutSourceObjectAsync(TestContext context)
    {
        var bucket = context.GetString("s3CopySrc") ?? throw new InvalidOperationException("s3CopySrc not set");
        await clients.S3().PutObjectAsync(new PutObjectRequest { BucketName = bucket, Key = "source.txt", ContentBody = "copy me" });
        var head = await clients.S3().GetObjectMetadataAsync(new GetObjectMetadataRequest { BucketName = bucket, Key = "source.txt" });
        Assertions.GreaterThan(0, head.Headers.ContentLength, "PutSourceObject: ContentLength should be > 0");
    }

    private async Task CopyObjectAsync(TestContext context)
    {
        var src = context.GetString("s3CopySrc") ?? throw new InvalidOperationException("s3CopySrc not set");
        var dst = context.GetString("s3CopyDst") ?? throw new InvalidOperationException("s3CopyDst not set");
        await clients.S3().CopyObjectAsync(new CopyObjectRequest
        {
            SourceBucket = src,
            SourceKey = "source.txt",
            DestinationBucket = dst,
            DestinationKey = "dest.txt",
        });
        using var response = await clients.S3().GetObjectAsync(new GetObjectRequest { BucketName = dst, Key = "dest.txt" });
        using var reader = new StreamReader(response.ResponseStream);
        var body = await reader.ReadToEndAsync();
        Assertions.Equal("copy me", body, "CopyObject: body mismatch");
    }

    private async Task CreateMultipartUploadAsync(TestContext context)
    {
        var bucket = context.GetString("s3MpBucket") ?? throw new InvalidOperationException("s3MpBucket not set");
        var resp = await clients.S3().InitiateMultipartUploadAsync(new InitiateMultipartUploadRequest { BucketName = bucket, Key = "large-file" });
        Assertions.True(!string.IsNullOrEmpty(resp.UploadId), "CreateMultipartUpload: UploadId is empty");
        context.Set("s3MpUploadId", resp.UploadId);
        context.Set("s3MpKey", "large-file");
    }

    private async Task UploadPartAsync(TestContext context)
    {
        var bucket = context.GetString("s3MpBucket") ?? throw new InvalidOperationException("s3MpBucket not set");
        var uploadId = context.GetString("s3MpUploadId") ?? throw new InvalidOperationException("s3MpUploadId not set");
        var key = context.GetString("s3MpKey") ?? throw new InvalidOperationException("s3MpKey not set");
        var bytes = new byte[5 * 1024 * 1024];
        Array.Fill(bytes, (byte)'A');
        var resp = await clients.S3().UploadPartAsync(new UploadPartRequest
        {
            BucketName = bucket,
            Key = key,
            UploadId = uploadId,
            PartNumber = 1,
            InputStream = new MemoryStream(bytes),
        });
        Assertions.True(!string.IsNullOrEmpty(resp.ETag), "UploadPart: ETag is empty");
        context.Set("s3MpEtag", resp.ETag);
    }

    private async Task CompleteMultipartUploadAsync(TestContext context)
    {
        var bucket = context.GetString("s3MpBucket") ?? throw new InvalidOperationException("s3MpBucket not set");
        var uploadId = context.GetString("s3MpUploadId") ?? throw new InvalidOperationException("s3MpUploadId not set");
        var key = context.GetString("s3MpKey") ?? throw new InvalidOperationException("s3MpKey not set");
        var etag = context.GetString("s3MpEtag") ?? throw new InvalidOperationException("s3MpEtag not set");
        await clients.S3().CompleteMultipartUploadAsync(new CompleteMultipartUploadRequest
        {
            BucketName = bucket,
            Key = key,
            UploadId = uploadId,
            PartETags = [new PartETag { PartNumber = 1, ETag = etag }],
        });
        var head = await clients.S3().GetObjectMetadataAsync(new GetObjectMetadataRequest { BucketName = bucket, Key = key });
        Assertions.GreaterThan(0, head.Headers.ContentLength, "CompleteMultipartUpload: ContentLength should be > 0");
    }

    private async Task AbortMultipartUploadAsync(TestContext context)
    {
        var bucket = context.GetString("s3MpBucket") ?? throw new InvalidOperationException("s3MpBucket not set");
        var create = await clients.S3().InitiateMultipartUploadAsync(new InitiateMultipartUploadRequest { BucketName = bucket, Key = "large-file" });
        var uploadId = create.UploadId;
        await clients.S3().AbortMultipartUploadAsync(new AbortMultipartUploadRequest { BucketName = bucket, Key = "large-file", UploadId = uploadId });
        var list = await clients.S3().ListMultipartUploadsAsync(new ListMultipartUploadsRequest { BucketName = bucket });
        Assertions.False(list.MultipartUploads.Any(u => u.UploadId == uploadId), "AbortMultipartUpload: upload still present after abort");
    }

    private async Task PutBucketVersioningAsync(TestContext context)
    {
        var bucket = context.GetString("s3VerBucket") ?? throw new InvalidOperationException("s3VerBucket not set");
        await clients.S3().PutBucketVersioningAsync(new PutBucketVersioningRequest
        {
            BucketName = bucket,
            VersioningConfig = new S3BucketVersioningConfig { Status = "Enabled" },
        });
        var resp = await clients.S3().GetBucketVersioningAsync(new GetBucketVersioningRequest { BucketName = bucket });
        Assertions.Equal("Enabled", resp.VersioningConfig?.Status?.Value ?? "", "PutBucketVersioning: status is not Enabled");
    }

    private async Task GetBucketVersioningAsync(TestContext context)
    {
        var bucket = context.GetString("s3VerBucket") ?? throw new InvalidOperationException("s3VerBucket not set");
        var resp = await clients.S3().GetBucketVersioningAsync(new GetBucketVersioningRequest { BucketName = bucket });
        Assertions.Equal("Enabled", resp.VersioningConfig?.Status?.Value ?? "", "GetBucketVersioning: status is not Enabled");
    }

    private async Task PutObjectTaggingAsync(TestContext context)
    {
        var bucket = context.GetString("s3TagBucket") ?? throw new InvalidOperationException("s3TagBucket not set");
        await clients.S3().PutObjectTaggingAsync(new PutObjectTaggingRequest
        {
            BucketName = bucket,
            Key = "test-key",
            Tagging = new Tagging { TagSet = [new Tag { Key = "env", Value = "test" }] },
        });
        var resp = await clients.S3().GetObjectTaggingAsync(new GetObjectTaggingRequest { BucketName = bucket, Key = "test-key" });
        Assertions.True(resp.Tagging.Any(t => t.Key == "env" && t.Value == "test"), "PutObjectTagging: env=test tag not found");
    }

    private async Task GetObjectTaggingAsync(TestContext context)
    {
        var bucket = context.GetString("s3TagBucket") ?? throw new InvalidOperationException("s3TagBucket not set");
        var resp = await clients.S3().GetObjectTaggingAsync(new GetObjectTaggingRequest { BucketName = bucket, Key = "test-key" });
        Assertions.True(resp.Tagging.Any(t => t.Key == "env" && t.Value == "test"), "GetObjectTagging: env=test tag not found");
    }

    private async Task PutBucketTaggingAsync(TestContext context)
    {
        var bucket = context.GetString("s3TagBucket") ?? throw new InvalidOperationException("s3TagBucket not set");
        await clients.S3().PutBucketTaggingAsync(new PutBucketTaggingRequest
        {
            BucketName = bucket,
            TagSet = [new Tag { Key = "project", Value = "overcast" }],
        });
        var resp = await clients.S3().GetBucketTaggingAsync(new GetBucketTaggingRequest { BucketName = bucket });
        Assertions.True(resp.TagSet.Any(t => t.Key == "project" && t.Value == "overcast"), "PutBucketTagging: project=overcast tag not found");
    }

    private async Task GetBucketTaggingAsync(TestContext context)
    {
        var bucket = context.GetString("s3TagBucket") ?? throw new InvalidOperationException("s3TagBucket not set");
        var resp = await clients.S3().GetBucketTaggingAsync(new GetBucketTaggingRequest { BucketName = bucket });
        Assertions.True(resp.TagSet.Any(t => t.Key == "project" && t.Value == "overcast"), "GetBucketTagging: project=overcast tag not found");
    }

    private async Task PutBucketWebsiteAsync(TestContext context)
    {
        var bucket = context.GetString("s3WebBucket") ?? throw new InvalidOperationException("s3WebBucket not set");
        await clients.S3().PutBucketWebsiteAsync(new PutBucketWebsiteRequest
        {
            BucketName = bucket,
            WebsiteConfiguration = new WebsiteConfiguration
            {
                IndexDocumentSuffix = "index.html",
                ErrorDocument = "error.html",
            },
        });
        var resp = await clients.S3().GetBucketWebsiteAsync(new GetBucketWebsiteRequest { BucketName = bucket });
        Assertions.Equal("index.html", resp.WebsiteConfiguration?.IndexDocumentSuffix ?? "", "PutBucketWebsite: index_document suffix is not index.html");
    }

    private async Task GetBucketWebsiteAsync(TestContext context)
    {
        var bucket = context.GetString("s3WebBucket") ?? throw new InvalidOperationException("s3WebBucket not set");
        var resp = await clients.S3().GetBucketWebsiteAsync(new GetBucketWebsiteRequest { BucketName = bucket });
        Assertions.Equal("index.html", resp.WebsiteConfiguration?.IndexDocumentSuffix ?? "", "GetBucketWebsite: index_document suffix is not index.html");
    }

    private async Task PutBucketCorsAsync(TestContext context)
    {
        var bucket = context.GetString("s3CorsBucket") ?? throw new InvalidOperationException("s3CorsBucket not set");
        await clients.S3().PutCORSConfigurationAsync(new PutCORSConfigurationRequest
        {
            BucketName = bucket,
            Configuration = new CORSConfiguration
            {
                Rules =
                [
                    new CORSRule
                    {
                        AllowedMethods = ["GET"],
                        AllowedOrigins = ["*"],
                        AllowedHeaders = ["*"],
                    },
                ],
            },
        });
        var resp = await clients.S3().GetCORSConfigurationAsync(new GetCORSConfigurationRequest { BucketName = bucket });
        Assertions.True((resp.Configuration?.Rules?.Count ?? 0) > 0, "PutBucketCors: no CORS rules found");
    }

    private async Task GetBucketCorsAsync(TestContext context)
    {
        var bucket = context.GetString("s3CorsBucket") ?? throw new InvalidOperationException("s3CorsBucket not set");
        var resp = await clients.S3().GetCORSConfigurationAsync(new GetCORSConfigurationRequest { BucketName = bucket });
        Assertions.True((resp.Configuration?.Rules?.Count ?? 0) > 0, "GetBucketCors: no CORS rules found");
    }

    private async Task TeardownMultipartAsync(TestContext context)
    {
        var bucket = context.GetString("s3MpBucket");
        if (string.IsNullOrWhiteSpace(bucket)) return;
        try
        {
            var uploads = await clients.S3().ListMultipartUploadsAsync(new ListMultipartUploadsRequest { BucketName = bucket });
            foreach (var upload in uploads.MultipartUploads)
            {
                try
                {
                    await clients.S3().AbortMultipartUploadAsync(new AbortMultipartUploadRequest
                    {
                        BucketName = bucket,
                        Key = upload.Key,
                        UploadId = upload.UploadId,
                    });
                }
                catch { }
            }
        }
        catch { }
        await EmptyAndDeleteBucketAsync(bucket);
    }

    private async Task TeardownVersioningAsync(TestContext context)
    {
        var bucket = context.GetString("s3VerBucket");
        if (string.IsNullOrWhiteSpace(bucket)) return;
        try
        {
            var versions = await clients.S3().ListVersionsAsync(new ListVersionsRequest { BucketName = bucket });
            foreach (var version in versions.Versions)
            {
                try
                {
                    await clients.S3().DeleteObjectAsync(new DeleteObjectRequest { BucketName = bucket, Key = version.Key, VersionId = version.VersionId });
                }
                catch { }
            }
        }
        catch { }
        try
        {
            await clients.S3().DeleteBucketAsync(new DeleteBucketRequest { BucketName = bucket });
        }
        catch { }
    }

    private async Task EmptyAndDeleteBucketAsync(string? bucket)
    {
        if (string.IsNullOrWhiteSpace(bucket))
        {
            return;
        }

        try
        {
            var objects = await clients.S3().ListObjectsV2Async(new ListObjectsV2Request { BucketName = bucket });
            foreach (var item in objects.S3Objects)
            {
                try
                {
                    await clients.S3().DeleteObjectAsync(new DeleteObjectRequest { BucketName = bucket, Key = item.Key });
                }
                catch
                {
                }
            }
        }
        catch
        {
        }

        try
        {
            await clients.S3().DeleteBucketAsync(new DeleteBucketRequest { BucketName = bucket });
        }
        catch
        {
        }
    }

    private static string RequireBucket(TestContext context)
    {
        return context.GetString("s3Bucket") ?? throw new InvalidOperationException("s3Bucket not set");
    }
}
