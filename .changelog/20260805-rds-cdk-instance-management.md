* [cloudformation/rds] an `AWS::RDS::DBInstance` or `AWS::RDS::DBCluster` resource completes only once the database reports `available`, as it does on AWS
  `cdk deploy` used to return green while the engine was still initialising —
  around half a minute for a MySQL container — so a migration or an app started
  off the back of a successful deploy was refused a connection. An instance that
  never came up settled into `failed` behind a stack that had already reported
  `CREATE_COMPLETE`; it now fails the resource with the reason RDS recorded
  against it and rolls the stack back.
* [cloudformation] a resource that is created and then fails to become usable is deleted when the stack rolls back
  Its physical ID was dropped with the error, so rollback skipped it and left a
  real resource behind under a name nothing recorded — which the next deploy
  then collided with. Affects RDS databases and ECS services, the two resource
  types whose creation is asynchronous.
