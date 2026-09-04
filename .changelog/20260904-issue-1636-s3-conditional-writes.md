* [s3] `PutObject` and `CompleteMultipartUpload` honour `If-None-Match` and `If-Match`, so conditional writes no longer overwrite.
  a guarded write against a key that already has a current object answers 412 `PreconditionFailed`; `If-Match` against a key with none answers 404.
  an `If-None-Match` carrying an ETag rather than `*` answers 501 `NotImplemented` instead of being ignored.
