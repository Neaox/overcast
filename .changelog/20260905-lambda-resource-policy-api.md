+ [lambda] `GetResourcePolicy`, `PutResourcePolicy` and `DeleteResourcePolicy` manage a function's resource-based policy as one JSON document.
  the document is the policy `AddPermission`, `GetPolicy` and `RemovePermission` already maintain, addressed by function, version or alias ARN.
  `PutResourcePolicy` replaces it wholesale — explicit `Deny`, list-valued `Action`/`Resource`/`Principal` and arbitrary condition keys all survive a round trip.
  a stale `RevisionId` answers `PreconditionFailedException`; a statement allowing every principal with no condition answers `PublicPolicyException`, as Lambda's default does.
