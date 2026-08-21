+ [compat] an `rds-cluster-members` group covering an Aurora cluster with an
  instance in it — `DBClusterMembers`, writer election, the settings a member
  inherits from its cluster, and the membership pruning `DeleteDBInstance` does
  — in the Go, Python, Node and Java SDK suites and the CLI suite. Nothing
  outside the emulator's own Go tests had looked at any of it.
