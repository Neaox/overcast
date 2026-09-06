* [iam] `ListRoles` and `ListUsers` omit `PermissionsBoundary`, and `ListPolicies` omits `Description`, matching AWS's documented listing subset
  the same notes that already exclude `Tags` name these too, and each points the caller at the matching `Get*`, which still returns the whole resource.
  `GetAccountAuthorizationDetails` keeps both: its members are `RoleDetail`, `UserDetail` and `ManagedPolicyDetail`, which AWS documents without the exclusion.
