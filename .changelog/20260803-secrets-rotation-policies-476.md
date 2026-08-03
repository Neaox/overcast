+ [secretsmanager] `RotateSecret` now runs AWS's four-step rotation protocol against the configured Lambda function — `createSecret`, `setSecret`, `testSecret`, `finishSecret` — and a single clock-driven loop fires rotations that come due on a `RotationRules` schedule
+ [secretsmanager] `UpdateSecretVersionStage`, staging labels and `ClientRequestToken` on `PutSecretValue`, and `GetSecretValue` by `VersionStage` — the version machinery a rotation function drives
+ [secretsmanager] resource policies: `PutResourcePolicy`, `GetResourcePolicy`, `DeleteResourcePolicy` and `ValidateResourcePolicy` store, return and validate a secret's policy instead of returning 501. Nothing evaluates it — a stored policy grants and denies nothing (issue #496)
+ [web] the Secrets Manager detail page shows rotation status, schedule, version staging labels, the last rotation attempt including which of the four steps failed, and the stored resource policy
~! [secretsmanager] `RotateSecret` rejects a secret with no rotation function configured, as AWS does, rather than saving the schedule and rotating nothing
  migration: pass `RotationLambdaARN` on the call or configure it first, and add `RotateImmediately: false` if you only want the schedule
~ [secretsmanager] secret ARNs carry AWS's six-character random suffix; the partial ARN without it still resolves, as on AWS
