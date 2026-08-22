* [sts] `AssumeRole` and `AssumeRoleWithWebIdentity` no longer repeat
  `RoleSessionName` in both segments of `AssumedRoleUser.Arn`. The first
  segment is now the role name parsed out of the request's `RoleArn`, matching
  AWS's `arn:aws:sts::<account>:assumed-role/<RoleName>/<SessionName>` shape —
  fixed identically on the legacy Query XML and the typed JSON/CBOR paths.
