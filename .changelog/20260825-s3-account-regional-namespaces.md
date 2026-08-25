+ [s3] `CreateBucket` supports account regional namespaces: `x-amz-bucket-namespace: account-regional` with a `<prefix>-<accountId>-<region>-an` name creates the bucket in the account's per-region namespace instead of the global one, and re-creating it returns `BucketAlreadyOwnedByYou` in every region, including us-east-1
+ [cloudformation/s3] `AWS::S3::Bucket` gains `BucketNamespace` and `BucketNamePrefix`, both Replacement on update; `BucketNamePrefix` appends the account/region suffix the way the console does
+ [iam] the `s3:x-amz-bucket-namespace` condition key is populated from the request header, so AWS's published `Deny`+`StringNotEquals` account-regional enforcement pattern works
~! [s3] `CreateBucket` now rejects a global-namespace bucket name ending in the reserved `-an` suffix
  migration: rename a bucket ending in `-an`, or create it with `x-amz-bucket-namespace: account-regional` instead
