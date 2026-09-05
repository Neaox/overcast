+ [lambda] `OVERCAST_ENFORCE_LAMBDA_RESOURCE_POLICY` makes a service-originated invoke check the function's resource-based policy first.
  off by default, so a stack that never called `AddPermission` keeps working; direct client `Invoke` calls are never gated, because credentials are not validated.
  S3, SNS, API Gateway and EventBridge each fail the way AWS does — a refused notification configuration, a dead-lettered delivery, a 500, a failed invocation.
  statements are evaluated as AWS evaluates them: `Principal.Service`, qualifier-aware `Resource`, `ArnLike`/`StringEquals` on `AWS:SourceArn` and `AWS:SourceAccount`, explicit `Deny` winning.
