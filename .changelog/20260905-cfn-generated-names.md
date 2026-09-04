* [cloudformation] two unnamed resources of one type in a stack no longer share a generated name.
  31 handlers named a resource the template did not name from the stack name alone, so the second collided with, merged onto, or silently overwrote the first.
  covers ELBv2, EKS, MSK, RDS, ElastiCache, Cognito, AutoScaling, CloudTrail, Backup, Athena, Glue, Firehose, Pipes, WAFv2, Shield, SES, AppRegistry and EC2 security groups.
  an unnamed AWS::Glue::Database now deploys at all — the generated name never reached CreateDatabase, which requires it.
