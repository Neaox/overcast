* [docs/cdk] corrected `docs/cdk.md`'s stale claims about stack provisioning and resource-type counts.
  describes provisioning as asynchronous (background goroutine, bounded `OVERCAST_CFN_SYNC_WAIT_MS` wait) instead of the stale "synchronously" wording in the `CREATE_IN_PROGRESS` troubleshooting entry
  fixed the resource-type counts (127 real handlers / 9 stubs, `AWS::DynamoDB::GlobalTable` moved from stub to real) to match the current `resourceHandlers` map
~ [docs/cdk] tightened the `docs/cdk/local-vpc.md` intro to the content charter's two-sentence budget
