~ [ec2] The remaining forty-eight EC2 operations answer from the typed
  operation registry, closing out the migration wave 1 (#1217) started — every
  one of EC2's 69 registered typed operations now answers from it, none from
  the legacy handler map. Twelve mutations whose typed body validated a
  parameter and returned success without touching the store (`TerminateInstances`,
  `StartInstances`, `StopInstances`, `DeleteTags`, `DeleteVpcEndpoints`,
  `ModifyInstanceAttribute`, the four `Authorize`/`RevokeSecurityGroup*`,
  `ModifySubnetAttribute`, `ModifyVpcAttribute`) now perform the write. Five
  creates that dropped part of their input (`RunInstances`' security groups and
  tags, `CreateVpc`'s DNS-support default, `CreateSubnet`/`CreateSecurityGroup`'s
  lifecycle events, `CreateTags`) now carry all of it. `DeleteVpc`/`DeleteSubnet`/
  `DeleteSecurityGroup` run the dependency checks #1233 added on both dispatch
  paths, so a DependencyViolation answer is the same whichever one served the
  request. A second differential test (mutation cases, seeded on two
  independent handlers since a mutation cannot be replayed against the same
  store the way a read can) holds every one of the 48 to the same bytes and
  the same store effect as its legacy twin. No response changes for an
  existing caller: legacy answered these calls before and answers the
  identical bytes now — the typed registry was never dispatched to for any of
  them until this PR.
* [ec2] `AllocateAddress`'s typed body ignored `Domain` and always allocated a
  `vpc`-domain address; found by the new differential test rather than by a
  caller, since the typed path had never been reachable.
