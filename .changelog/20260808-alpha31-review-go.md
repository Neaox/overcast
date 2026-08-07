* [router] debug tracing no longer pins an oversized request body's full backing array in memory when truncating it into the trace ring buffer, and internal CloudFormation dispatch hop bodies are capped at 1 MiB and flagged truncated
* [router] traces of responses written without an explicit status code record 200 instead of appearing in-flight forever in the debug UI
* [cloudformation/kms] `AWS::KMS::Key` updates dispatch `PutKeyPolicy` only when `KeyPolicy` actually changed, so an unchanged caller-locking policy created with `BypassPolicyLockoutSafetyCheck` survives unrelated stack updates; a `KeyPolicy` given as a JSON string is forwarded verbatim instead of double-encoded; `Description` changes are applied through `UpdateKeyDescription` instead of being ignored
+ [kms] `UpdateKeyDescription`
*! [logs] `PutRetentionPolicy` validates `retentionInDays` against AWS's fixed value set and returns `InvalidParameterException` otherwise, matching real CloudWatch Logs
  migration: use one of the AWS-documented retention values (1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288 or 3653 days)
* [sns] `TagResource` and `UntagResource` responses include the empty result element botocore requires, so the AWS CLI no longer fails client-side after a successful tagging call
