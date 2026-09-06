* [iam] `ListRoles` and `ListUsers` omit `PermissionsBoundary`, and `ListPolicies` omits `Description`, matching AWS's documented listing subset
  the same notes that already exclude `Tags` name these too, and each points the caller at the matching `Get*`, which still returns the whole resource.
  `GetAccountAuthorizationDetails` keeps both: its members are `RoleDetail`, `UserDetail` and `ManagedPolicyDetail`, which AWS documents without the exclusion.
*! [mcp] `runtime_iam_create_role` defaults to a real `sts:AssumeRole` trust policy, and both IAM create tools refuse a malformed document
  the placeholder default carried an empty `Statement` list, which grants nothing and which the IAM API itself will not store, so a role created through MCP could not be assumed.
  the check is `iampolicy.ValidateDocument`, the same one `CreateRole` and `CreatePolicy` apply, so the two write paths agree on what a well-formed document is.
  migration: pass a document with at least one statement to `runtime_iam_create_role` and `runtime_iam_create_policy`, or leave the trust policy out and take the default.
