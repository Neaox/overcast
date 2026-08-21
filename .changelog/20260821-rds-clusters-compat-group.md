* [rds] `StopDBCluster` and `StartDBCluster` answer in their own response
  envelopes instead of `CreateDBClusterResponse`, so an AWS SDK can deserialize
  them. The state change was always correct, so nothing that read the stored
  record noticed.
+ [compat] an `rds-clusters` group covering the Aurora DB cluster lifecycle —
  create, describe, modify, stop, start, delete — in the Go, Python, Node and
  Java SDK suites and the CLI suite. It is what found the envelope fix above.
