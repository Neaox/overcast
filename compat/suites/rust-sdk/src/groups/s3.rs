use std::collections::HashMap;
use std::sync::Arc;

use aws_sdk_s3::primitives::ByteStream;
use aws_sdk_s3::types::{
    BucketVersioningStatus, CompletedMultipartUpload, CompletedPart, CorsConfiguration,
    CorsRule, Delete, ErrorDocument, IndexDocument, ObjectIdentifier, Tag, Tagging,
    VersioningConfiguration, WebsiteConfiguration,
};

use crate::clients::AwsClients;
use crate::groups::ServiceGroup;
use crate::harness::{TestContext, TestFn};

pub struct S3Group {
    clients: Arc<AwsClients>,
}

impl S3Group {
    pub fn new(clients: Arc<AwsClients>) -> Self {
        Self { clients }
    }
}

impl ServiceGroup for S3Group {
    fn impls(&self) -> HashMap<String, TestFn> {
        let mut impls: HashMap<String, TestFn> = HashMap::new();

        let clients = self.clients.clone();
        impls.insert(
            "CreateBucket".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3create", ctx.run_id.as_ref());
                    clients
                        .s3()
                        .create_bucket()
                        .bucket(&bucket)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .s3()
                        .list_buckets()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .buckets()
                        .iter()
                        .any(|item| item.name().unwrap_or_default() == bucket);
                    cleanup_bucket(&clients, &bucket).await;
                    found.then_some(()).ok_or_else(|| {
                        format!(
                            "CreateBucket: bucket {bucket} not found in ListBuckets (runId={})",
                            ctx.run_id
                        )
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutObject".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    clients
                        .s3()
                        .put_object()
                        .bucket(&bucket)
                        .key("test-key")
                        .body(ByteStream::from_static(b"hello world"))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let head = clients
                        .s3()
                        .head_object()
                        .bucket(&bucket)
                        .key("test-key")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    (head.content_length().unwrap_or_default() > 0)
                        .then_some(())
                        .ok_or_else(|| "PutObject: ContentLength should be > 0".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "HeadObject".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    let head = clients
                        .s3()
                        .head_object()
                        .bucket(&bucket)
                        .key("test-key")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    (head.content_length().unwrap_or_default() > 0)
                        .then_some(())
                        .ok_or_else(|| "HeadObject: ContentLength should be > 0".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetObject".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    let object = clients
                        .s3()
                        .get_object()
                        .bucket(&bucket)
                        .key("test-key")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let body = object.body.collect().await.map_err(|err| err.to_string())?;
                    (body.into_bytes().as_ref() == b"hello world")
                        .then_some(())
                        .ok_or_else(|| "GetObject: body mismatch".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListObjectsV2".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    let response = clients
                        .s3()
                        .list_objects_v2()
                        .bucket(&bucket)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .contents()
                        .iter()
                        .any(|item| item.key().unwrap_or_default() == "test-key");
                    found.then_some(()).ok_or_else(|| {
                        format!("ListObjectsV2: test-key not found (runId={})", ctx.run_id)
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutObjectMultipleKeys".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    clients
                        .s3()
                        .put_object()
                        .bucket(&bucket)
                        .key("prefix/a")
                        .body(ByteStream::from_static(b"a"))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    clients
                        .s3()
                        .put_object()
                        .bucket(&bucket)
                        .key("prefix/b")
                        .body(ByteStream::from_static(b"b"))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .s3()
                        .list_objects_v2()
                        .bucket(&bucket)
                        .prefix("prefix/")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    (response.contents().len() >= 2)
                        .then_some(())
                        .ok_or_else(|| {
                            "PutObjectMultipleKeys: expected >= 2 objects under prefix/".to_string()
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListObjectsV2Delimiter".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    let response = clients
                        .s3()
                        .list_objects_v2()
                        .bucket(&bucket)
                        .prefix("prefix/")
                        .delimiter("/")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    (response.contents().len() >= 2)
                        .then_some(())
                        .ok_or_else(|| {
                            "ListObjectsV2Delimiter: expected >= 2 objects under prefix/"
                                .to_string()
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteObject".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    clients
                        .s3()
                        .delete_object()
                        .bucket(&bucket)
                        .key("test-key")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .s3()
                        .list_objects_v2()
                        .bucket(&bucket)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .contents()
                        .iter()
                        .any(|item| item.key().unwrap_or_default() == "test-key");
                    (!found).then_some(()).ok_or_else(|| {
                        "DeleteObject: test-key still present after deletion".to_string()
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteObjects".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx
                        .get("s3Bucket")
                        .ok_or_else(|| "s3Bucket not set".to_string())?;
                    clients
                        .s3()
                        .put_object()
                        .bucket(&bucket)
                        .key("del/a")
                        .body(ByteStream::from_static(b"a"))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    clients
                        .s3()
                        .put_object()
                        .bucket(&bucket)
                        .key("del/b")
                        .body(ByteStream::from_static(b"b"))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let delete = Delete::builder()
                        .objects(
                            ObjectIdentifier::builder()
                                .key("del/a")
                                .build()
                                .map_err(|e| e.to_string())?,
                        )
                        .objects(
                            ObjectIdentifier::builder()
                                .key("del/b")
                                .build()
                                .map_err(|e| e.to_string())?,
                        )
                        .build()
                        .map_err(|e| e.to_string())?;
                    clients
                        .s3()
                        .delete_objects()
                        .bucket(&bucket)
                        .delete(delete)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .s3()
                        .list_objects_v2()
                        .bucket(&bucket)
                        .prefix("del/")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    (response.contents().is_empty())
                        .then_some(())
                        .ok_or_else(|| {
                            "DeleteObjects: objects still present after batch delete".to_string()
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteBucket".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3del", ctx.run_id.as_ref());
                    clients
                        .s3()
                        .create_bucket()
                        .bucket(&bucket)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    clients
                        .s3()
                        .delete_bucket()
                        .bucket(&bucket)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .s3()
                        .list_buckets()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .buckets()
                        .iter()
                        .any(|item| item.name().unwrap_or_default() == bucket);
                    (!found).then_some(()).ok_or_else(|| {
                        format!("DeleteBucket: bucket {bucket} still present after deletion")
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "CreateSourceBucket".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let src = format!("{}-s3-copy-src", ctx.run_id.as_ref());
                    let dst = format!("{}-s3-copy-dst", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&src).send().await.map_err(|err| err.to_string())?;
                    clients.s3().create_bucket().bucket(&dst).send().await.map_err(|err| err.to_string())?;
                    let response = clients.s3().list_buckets().send().await.map_err(|err| err.to_string())?;
                    let found_src = response.buckets().iter().any(|b| b.name().unwrap_or_default() == src);
                    let found_dst = response.buckets().iter().any(|b| b.name().unwrap_or_default() == dst);
                    cleanup_bucket(&clients, &src).await;
                    cleanup_bucket(&clients, &dst).await;
                    (found_src && found_dst).then_some(()).ok_or_else(|| {
                        format!("CreateSourceBucket: buckets not found in ListBuckets (runId={})", ctx.run_id)
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutSourceObject".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3CopySrc").ok_or_else(|| "s3CopySrc not set".to_string())?;
                    clients.s3().put_object().bucket(&bucket).key("source.txt").body(ByteStream::from_static(b"copy me")).send().await.map_err(|err| err.to_string())?;
                    let head = clients.s3().head_object().bucket(&bucket).key("source.txt").send().await.map_err(|err| err.to_string())?;
                    (head.content_length().unwrap_or_default() > 0).then_some(()).ok_or_else(|| "PutSourceObject: ContentLength should be > 0".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "CopyObject".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let src = ctx.get("s3CopySrc").ok_or_else(|| "s3CopySrc not set".to_string())?;
                    let dst = ctx.get("s3CopyDst").ok_or_else(|| "s3CopyDst not set".to_string())?;
                    clients.s3().copy_object()
                        .bucket(&dst)
                        .key("dest.txt")
                        .copy_source(format!("{}/source.txt", src))
                        .send().await.map_err(|err| err.to_string())?;
                    let object = clients.s3().get_object().bucket(&dst).key("dest.txt").send().await.map_err(|err| err.to_string())?;
                    let body = object.body.collect().await.map_err(|err| err.to_string())?;
                    (body.into_bytes().as_ref() == b"copy me").then_some(()).ok_or_else(|| "CopyObject: body mismatch".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "CreateMultipartUpload".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3MpBucket").ok_or_else(|| "s3MpBucket not set".to_string())?;
                    let resp = clients.s3().create_multipart_upload()
                        .bucket(&bucket)
                        .key("large-file")
                        .send().await.map_err(|err| err.to_string())?;
                    let upload_id = resp.upload_id().ok_or_else(|| "no upload_id".to_string())?;
                    (!upload_id.is_empty()).then_some(()).ok_or_else(|| "upload_id is empty".to_string())?;
                    ctx.set("s3MpUploadId", upload_id.to_string());
                    ctx.set("s3MpKey", "large-file".to_string());
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "UploadPart".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3MpBucket").ok_or_else(|| "s3MpBucket not set".to_string())?;
                    let upload_id = ctx.get("s3MpUploadId").ok_or_else(|| "s3MpUploadId not set".to_string())?;
                    let key = ctx.get("s3MpKey").ok_or_else(|| "s3MpKey not set".to_string())?;
                    let resp = clients.s3().upload_part()
                        .bucket(&bucket)
                        .key(&key)
                        .upload_id(&upload_id)
                        .part_number(1)
                        .body(ByteStream::from(vec![b'A'; 5_242_880]))
                        .send().await.map_err(|err| err.to_string())?;
                    let etag = resp.e_tag().ok_or_else(|| "no ETag".to_string())?;
                    (!etag.is_empty()).then_some(()).ok_or_else(|| "ETag is empty".to_string())?;
                    ctx.set("s3MpEtag", etag.to_string());
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "CompleteMultipartUpload".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3MpBucket").ok_or_else(|| "s3MpBucket not set".to_string())?;
                    let upload_id = ctx.get("s3MpUploadId").ok_or_else(|| "s3MpUploadId not set".to_string())?;
                    let key = ctx.get("s3MpKey").ok_or_else(|| "s3MpKey not set".to_string())?;
                    let etag = ctx.get("s3MpEtag").ok_or_else(|| "s3MpEtag not set".to_string())?;
                    let part = CompletedPart::builder().part_number(1).e_tag(&etag).build();
                    let completed = CompletedMultipartUpload::builder().parts(part).build();
                    clients.s3().complete_multipart_upload()
                        .bucket(&bucket)
                        .key(&key)
                        .upload_id(&upload_id)
                        .multipart_upload(completed)
                        .send().await.map_err(|err| err.to_string())?;
                    let head = clients.s3().head_object().bucket(&bucket).key(&key).send().await.map_err(|err| err.to_string())?;
                    (head.content_length().unwrap_or_default() > 0).then_some(()).ok_or_else(|| "CompleteMultipartUpload: ContentLength should be > 0".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "AbortMultipartUpload".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3MpBucket").ok_or_else(|| "s3MpBucket not set".to_string())?;
                    let create = clients.s3().create_multipart_upload()
                        .bucket(&bucket)
                        .key("large-file")
                        .send().await.map_err(|err| err.to_string())?;
                    let upload_id = create.upload_id().ok_or_else(|| "no upload_id".to_string())?;
                    clients.s3().abort_multipart_upload()
                        .bucket(&bucket)
                        .key("large-file")
                        .upload_id(upload_id)
                        .send().await.map_err(|err| err.to_string())?;
                    let list = clients.s3().list_multipart_uploads()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let found = list.uploads().iter().any(|u| u.upload_id().unwrap_or_default() == upload_id);
                    (!found).then_some(()).ok_or_else(|| "AbortMultipartUpload: upload still present after abort".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutBucketVersioning".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3VerBucket").ok_or_else(|| "s3VerBucket not set".to_string())?;
                    let vc = VersioningConfiguration::builder()
                        .status(BucketVersioningStatus::Enabled)
                        .build();
                    clients.s3().put_bucket_versioning()
                        .bucket(&bucket)
                        .versioning_configuration(vc)
                        .send().await.map_err(|err| err.to_string())?;
                    let resp = clients.s3().get_bucket_versioning()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let status = resp.status().map(|s| s.as_str().to_string()).unwrap_or_default();
                    (status == "Enabled").then_some(()).ok_or_else(|| "PutBucketVersioning: status is not Enabled".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetBucketVersioning".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3VerBucket").ok_or_else(|| "s3VerBucket not set".to_string())?;
                    let resp = clients.s3().get_bucket_versioning()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let status = resp.status().map(|s| s.as_str().to_string()).unwrap_or_default();
                    (status == "Enabled").then_some(()).ok_or_else(|| "GetBucketVersioning: status is not Enabled".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutObjectTagging".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3TagBucket").ok_or_else(|| "s3TagBucket not set".to_string())?;
                    let tag = Tag::builder().key("env").value("test").build().map_err(|e| e.to_string())?;
                    let tagging = Tagging::builder().tag_set(tag).build().map_err(|e| e.to_string())?;
                    clients.s3().put_object_tagging()
                        .bucket(&bucket)
                        .key("test-key")
                        .tagging(tagging)
                        .send().await.map_err(|err| err.to_string())?;
                    let resp = clients.s3().get_object_tagging()
                        .bucket(&bucket)
                        .key("test-key")
                        .send().await.map_err(|err| err.to_string())?;
                    let found = resp.tag_set().iter().any(|t| t.key() == "env" && t.value() == "test");
                    found.then_some(()).ok_or_else(|| "PutObjectTagging: env=test tag not found".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetObjectTagging".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3TagBucket").ok_or_else(|| "s3TagBucket not set".to_string())?;
                    let resp = clients.s3().get_object_tagging()
                        .bucket(&bucket)
                        .key("test-key")
                        .send().await.map_err(|err| err.to_string())?;
                    let found = resp.tag_set().iter().any(|t| t.key() == "env" && t.value() == "test");
                    found.then_some(()).ok_or_else(|| "GetObjectTagging: env=test tag not found".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutBucketTagging".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3TagBucket").ok_or_else(|| "s3TagBucket not set".to_string())?;
                    let tag = Tag::builder().key("project").value("overcast").build().map_err(|e| e.to_string())?;
                    let tagging = Tagging::builder().tag_set(tag).build().map_err(|e| e.to_string())?;
                    clients.s3().put_bucket_tagging()
                        .bucket(&bucket)
                        .tagging(tagging)
                        .send().await.map_err(|err| err.to_string())?;
                    let resp = clients.s3().get_bucket_tagging()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let found = resp.tag_set().iter().any(|t| t.key() == "project" && t.value() == "overcast");
                    found.then_some(()).ok_or_else(|| "PutBucketTagging: project=overcast tag not found".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetBucketTagging".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3TagBucket").ok_or_else(|| "s3TagBucket not set".to_string())?;
                    let resp = clients.s3().get_bucket_tagging()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let found = resp.tag_set().iter().any(|t| t.key() == "project" && t.value() == "overcast");
                    found.then_some(()).ok_or_else(|| "GetBucketTagging: project=overcast tag not found".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutBucketWebsite".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3WebBucket").ok_or_else(|| "s3WebBucket not set".to_string())?;
                    let website_cfg = WebsiteConfiguration::builder()
                        .index_document(IndexDocument::builder().suffix("index.html").build().map_err(|e| e.to_string())?)
                        .error_document(ErrorDocument::builder().key("error.html").build().map_err(|e| e.to_string())?)
                        .build();
                    clients.s3().put_bucket_website()
                        .bucket(&bucket)
                        .website_configuration(website_cfg)
                        .send().await.map_err(|err| err.to_string())?;
                    let resp = clients.s3().get_bucket_website()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let suffix = resp.index_document().map(|d| d.suffix()).unwrap_or_default();
                    (suffix == "index.html").then_some(()).ok_or_else(|| "PutBucketWebsite: index_document suffix is not index.html".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetBucketWebsite".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3WebBucket").ok_or_else(|| "s3WebBucket not set".to_string())?;
                    let resp = clients.s3().get_bucket_website()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    let suffix = resp.index_document().map(|d| d.suffix()).unwrap_or_default();
                    (suffix == "index.html").then_some(()).ok_or_else(|| "GetBucketWebsite: index_document suffix is not index.html".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutBucketCors".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3CorsBucket").ok_or_else(|| "s3CorsBucket not set".to_string())?;
                    let rule = CorsRule::builder()
                        .allowed_methods("GET")
                        .allowed_origins("*")
                        .allowed_headers("*")
                        .build()
                        .map_err(|e| e.to_string())?;
                    let cors_cfg = CorsConfiguration::builder()
                        .cors_rules(rule)
                        .build()
                        .map_err(|e| e.to_string())?;
                    clients.s3().put_bucket_cors()
                        .bucket(&bucket)
                        .cors_configuration(cors_cfg)
                        .send().await.map_err(|err| err.to_string())?;
                    let resp = clients.s3().get_bucket_cors()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    (!resp.cors_rules().is_empty()).then_some(()).ok_or_else(|| "PutBucketCors: no CORS rules found".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetBucketCors".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = ctx.get("s3CorsBucket").ok_or_else(|| "s3CorsBucket not set".to_string())?;
                    let resp = clients.s3().get_bucket_cors()
                        .bucket(&bucket)
                        .send().await.map_err(|err| err.to_string())?;
                    (!resp.cors_rules().is_empty()).then_some(()).ok_or_else(|| "GetBucketCors: no CORS rules found".to_string())
                })
            }),
        );

        impls
    }

    fn setups(&self) -> HashMap<String, TestFn> {
        let mut setups: HashMap<String, TestFn> = HashMap::new();
        let clients = self.clients.clone();
        setups.insert(
            "s3-crud".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3crud", ctx.run_id.as_ref());
                    clients
                        .s3()
                        .create_bucket()
                        .bucket(&bucket)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    ctx.set("s3Bucket", bucket);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "s3-copy".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let src = format!("{}-s3-copy-src", ctx.run_id.as_ref());
                    let dst = format!("{}-s3-copy-dst", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&src).send().await.map_err(|err| err.to_string())?;
                    clients.s3().create_bucket().bucket(&dst).send().await.map_err(|err| err.to_string())?;
                    ctx.set("s3CopySrc", src);
                    ctx.set("s3CopyDst", dst);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "s3-multipart".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3-mp", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&bucket).send().await.map_err(|err| err.to_string())?;
                    ctx.set("s3MpBucket", bucket);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "s3-versioning".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3-ver", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&bucket).send().await.map_err(|err| err.to_string())?;
                    ctx.set("s3VerBucket", bucket);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "s3-tagging".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3-tag", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&bucket).send().await.map_err(|err| err.to_string())?;
                    clients.s3().put_object()
                        .bucket(&bucket)
                        .key("test-key")
                        .body(ByteStream::from_static(b"tagged"))
                        .send().await.map_err(|err| err.to_string())?;
                    ctx.set("s3TagBucket", bucket);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "s3-website".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3-web", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&bucket).send().await.map_err(|err| err.to_string())?;
                    ctx.set("s3WebBucket", bucket);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "s3-cors".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let bucket = format!("{}-s3-cors", ctx.run_id.as_ref());
                    clients.s3().create_bucket().bucket(&bucket).send().await.map_err(|err| err.to_string())?;
                    ctx.set("s3CorsBucket", bucket);
                    Ok(())
                })
            }),
        );

        setups
    }

    fn teardowns(&self) -> HashMap<String, TestFn> {
        let mut teardowns: HashMap<String, TestFn> = HashMap::new();
        let clients = self.clients.clone();
        teardowns.insert(
            "s3-crud".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3Bucket") {
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "s3-copy".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3CopySrc") {
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    if let Some(bucket) = ctx.get("s3CopyDst") {
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "s3-multipart".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3MpBucket") {
                        if let Ok(resp) = clients.s3().list_multipart_uploads().bucket(&bucket).send().await {
                            for upload in resp.uploads() {
                                if let (Some(key), Some(upload_id)) = (upload.key(), upload.upload_id()) {
                                    let _ = clients.s3().abort_multipart_upload()
                                        .bucket(&bucket)
                                        .key(key)
                                        .upload_id(upload_id)
                                        .send().await;
                                }
                            }
                        }
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "s3-versioning".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3VerBucket") {
                        cleanup_versioned_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "s3-tagging".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3TagBucket") {
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "s3-website".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3WebBucket") {
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "s3-cors".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(bucket) = ctx.get("s3CorsBucket") {
                        cleanup_bucket(&clients, &bucket).await;
                    }
                    Ok(())
                })
            }),
        );

        teardowns
    }
}

async fn cleanup_bucket(clients: &AwsClients, bucket: &str) {
    if let Ok(response) = clients.s3().list_objects_v2().bucket(bucket).send().await {
        for item in response.contents() {
            if let Some(key) = item.key() {
                let _ = clients
                    .s3()
                    .delete_object()
                    .bucket(bucket)
                    .key(key)
                    .send()
                    .await;
            }
        }
    }
    let _ = clients.s3().delete_bucket().bucket(bucket).send().await;
}

async fn cleanup_versioned_bucket(clients: &AwsClients, bucket: &str) {
    if let Ok(resp) = clients.s3().list_object_versions().bucket(bucket).send().await {
        for v in resp.versions() {
            if let (Some(key), Some(version_id)) = (v.key(), v.version_id()) {
                let _ = clients.s3().delete_object().bucket(bucket).key(key).version_id(version_id).send().await;
            }
        }
        for dm in resp.delete_markers() {
            if let (Some(key), Some(version_id)) = (dm.key(), dm.version_id()) {
                let _ = clients.s3().delete_object().bucket(bucket).key(key).version_id(version_id).send().await;
            }
        }
    }
    let _ = clients.s3().delete_bucket().bucket(bucket).send().await;
}
