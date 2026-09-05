+ [backup] Backup Access Points — create, describe, list, delete and the two filtered listings, at AWS's `/backup-access-point` bindings.
  metadata only, like the rest of the service: `AccessPointPolicy` is dropped and no Amazon S3 access point is created, so `S3AccessPointArn`/`S3AccessPointAlias` are absent rather than invented.
  no backup job runs, so no recovery point exists for `ListBackupAccessPointsByRecoveryPoint` or `ListBackupAccessPointsByResource` to match — both filter real stored metadata and come back empty.
  `/backup-access-point` now classifies as `backup`; an unsigned request there was labelled, and IAM-authorised, as `s3`.
