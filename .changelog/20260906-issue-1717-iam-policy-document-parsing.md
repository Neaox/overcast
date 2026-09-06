*! [iam] a malformed policy document is refused with `MalformedPolicyDocument`, as AWS does, instead of being stored unparsed
  covers `CreatePolicy`, `CreatePolicyVersion`, `CreateRole`, `UpdateAssumeRolePolicy` and the three `Put*Policy` writers, through the parser the simulator already evaluates with.
  structural only: valid JSON, a `Statement` that is an object or a non-empty array, each statement an exact `Allow`/`Deny` `Effect` and an `Action` or `NotAction`. ARN syntax stays unchecked.
  migration: give any placeholder document a real statement — `{}` and an empty `Statement` list were never accepted by AWS either.
