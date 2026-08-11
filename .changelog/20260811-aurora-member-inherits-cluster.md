* [rds] An Aurora member instance inherits its cluster's DB subnet group, and with it the cluster's VPC, when the instance names none of its own. CDK's rds.DatabaseCluster emits the member with no subnet group, so the writer previously landed outside the VPC its own application was in.

* [rds] CreateDBInstance no longer demands MasterUsername and MasterUserPassword from an instance that names a DBClusterIdentifier. RDS keeps those on the cluster and rejects them on a member, so the shape CDK emits was refused outright; the member now takes the cluster's. A standalone instance still requires both.

* [cloudformation] AWS::RDS::DBInstance forwards PubliclyAccessible. It was dropped at the template boundary, leaving a template's own opt-out from a subnet group's private default with no effect.
