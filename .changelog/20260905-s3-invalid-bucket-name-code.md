*! [s3] `CreateBucket` rejects an invalid bucket name with S3's documented `InvalidBucketName` code instead of `InvalidArgument`.
  the reserved `-an` suffix follows it; an unrecognised `x-amz-bucket-namespace` value stays `InvalidArgument`, which is what S3 documents for a bad request argument.
  migration: a test asserting `InvalidArgument` from `CreateBucket` on a malformed name should expect `InvalidBucketName` — the code real S3 returns.
