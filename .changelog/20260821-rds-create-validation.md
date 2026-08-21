*! [rds] `CreateDBCluster` and `CreateDBInstance` enforce the identifier shape
  AWS documents — 1 to 63 letters, numbers or hyphens, starting with a letter,
  no trailing or doubled hyphen — and store identifiers lowercase, so a describe
  finds what a create made whatever case it was sent in.
  migration: an identifier real RDS would have refused is now refused here too;
  rename it to match the documented shape. A mixed-case identifier keeps
  working and comes back lowercased, as it does on AWS.
*! [rds] a `DBSubnetGroupName` naming no existing subnet group is refused by
  `CreateDBCluster` and `CreateDBInstance`, and `DBSubnetGroupNotFoundFault`
  carries AWS's documented 404 rather than a 400.
  migration: create the subnet group before the database that references it, as
  AWS requires. Code branching on the 400 status rather than the error code
  should read the code, which has not changed.
*! [rds] cluster `BackupRetentionPeriod` defaults to 1 and is held to AWS's
  documented 1–35 on create and modify; `Port` is held to 1150–65535 on both.
  migration: a cluster created without `BackupRetentionPeriod` now reports 1
  rather than 0. `BackupRetentionPeriod=0` was never a value AWS accepts for a
  cluster and is now refused; send 1 to mean the minimum.
* [cloudformation] a generated physical ID no longer contains two consecutive
  hyphens when truncation lands on a separator, which RDS and several other
  services refuse.
