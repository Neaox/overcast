* [s3] `GetObject`, `GetObjectTagging` and `CopyObject`'s source lookup return `NoSuchBucket` for a bucket that does not exist (#1635)
  previously both answered `NoSuchKey`, indistinguishable from a missing key in an existing bucket
