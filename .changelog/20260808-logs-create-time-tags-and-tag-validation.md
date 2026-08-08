+ [cloudwatch-logs] `CreateLogGroup` accepts AWS's `tags` field and applies it as part of creating
  the log group, over AWS JSON and RPC v2 CBOR alike; `AWS::Logs::LogGroup` now passes its `Tags`
  straight to the create instead of following up with `TagLogGroup`, so a rejected tag map leaves
  nothing behind
*. [cloudwatch-logs] `CreateLogGroup` and `TagLogGroup` apply AWS's documented tag constraints —
  at most 50 tags, keys of 1–128 characters, values of up to 256, and no key beginning with the
  reserved `aws:` prefix — returning `InvalidParameterException` before any state changes, so a
  bad tag map no longer half-applies. Tag maps real AWS would have refused are refused here too
* [serviceutil] the shared tag validator checks keys in sorted order, so a map that violates more
  than one rule reports the same violation on every call instead of a random one
