* [backup] every AWS Backup operation is served at the binding AWS models — vaults under `/backup-vaults` and plans under `/backup/plans` — so an unmodified SDK, CDK construct or `aws backup …` call reaches it instead of answering 501; Overcast registered no routes for this service at all
- [backup] the invented `AWSBackup.` `X-Amz-Target` namespace and the Smithy RPC v2 CBOR surface it registered, neither of which the pinned model gives AWS Backup any trace of
  migration: use AWS's own bindings — `PUT`, `GET` and `DELETE /backup-vaults/{BackupVaultName}`, `GET /backup-vaults`, `PUT` and `GET /backup/plans`, and `GET`, `POST` and `DELETE /backup/plans/{BackupPlanId}`
~! [backup] timestamps are epoch seconds, the encoding REST JSON binds them to, in place of RFC 3339 strings that every AWS SDK rejected outright when deserialising a response
  migration: none for SDK callers; a hand-written client reading `CreationDate` or `DeletionDate` as a string must read a number
~! [backup] vaults and plans are scoped to the region they were created in, as on AWS, where one vault of a given name was shared by every region while its ARN named the creating region
  migration: vaults and plans recorded by an earlier version are not visible to this one; recreate them
~! [backup] `DeleteBackupVault` answers an empty 200, the `Unit` output AWS models, in place of an invented document
  migration: read the vault's ARN and name from the `CreateBackupVault` or `DescribeBackupVault` response instead of the delete's
~! [backup] `ResourceNotFoundException`, `AlreadyExistsException`, `MissingParameterValueException` and `InvalidParameterValueException` replace the unmodeled `ValidationException` and the 404 status; AWS Backup models no `ValidationException` and gives all four HTTP 400
  migration: match on the modeled error codes, and on HTTP 400 rather than 404 for a missing vault or plan
*! [backup] `CreateBackupPlan` mints a UUID for `BackupPlanId`, as AWS does, where the identifier was derived from the clock and two plans created in the same nanosecond collided — the second silently replaced the first
  migration: treat `BackupPlanId` as opaque; anything deriving a plan id from the creation time, or parsing the old `plan-<nanoseconds>` form, must read it from the create response instead
* [backup] `UpdateBackupPlan` mints a new `VersionId` per update and returns the modeled `CreationDate`; every update after the first used to report the same hardcoded version
+ [backup] `ListBackupVaults` and `ListBackupPlans` honour `maxResults` and `nextToken`, and `ListBackupVaults` also filters on `vaultType` and `shared`; a token that cannot be decoded gets an `InvalidParameterValueException` in place of a silent restart at the first page
+ [backup] `CreateBackupVault` validates the vault name against the pattern AWS documents, and `DescribeBackupVault` honours `backupVaultAccountId`
+ [backup] `GetBackupPlan` answers not-found for a `versionId` other than the plan's current one, rather than returning the current version under the requested version's name
* [backup/cloudformation] `AWS::Backup::BackupVault` and `AWS::Backup::BackupPlan` provision over the modeled REST bindings, and the plan handler addresses the plan by id when deleting it, where it sent the ARN it uses as the physical ID and matched nothing — so a stack delete left the plan behind
* [backup/router] AWS Backup requests are classified from their path prefixes, so they are logged and authorised as `backup` rather than falling through to S3 when unsigned
* [backup] a single undecodable vault or plan record is skipped and logged instead of failing a describe with a 500 or disappearing from a listing without trace
