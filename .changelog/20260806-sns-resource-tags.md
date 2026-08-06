+ [sns] TagResource, UntagResource, and ListTagsForResource operations
+ [lambda] TagResource, UntagResource, and ListTags operations
  CloudFormation tag updates now apply
+ [dynamodb] TagResource, ListTagsOfResource, and UntagResource operations; CreateTable accepts Tags
+ [cognito] TagResource, UntagResource, and ListTagsForResource for user pools
+ [appconfig] TagResource, UntagResource, and ListTagsForResource for applications, environments, and configuration profiles
+ [athena] TagResource, UntagResource, and ListTagsForResource for workgroups
+ [firehose] TagDeliveryStream, UntagDeliveryStream, and ListTagsForDeliveryStream
+ [glue] TagResource, UntagResource, and ListTagsForResource for databases and tables
+ [shield] TagResource, UntagResource, and ListTagsForResource for protections
+ [ssm] AddTagsToResource, RemoveTagsFromResource, and ListTagsForResource for parameters
+ [stepfunctions] TagResource, UntagResource, and ListTagsForResource for state machines
+ [eventbridge] TagResource, UntagResource, and ListTagsForResource for event buses
+ [elbv2] AddTags, RemoveTags, and DescribeTags for load balancers and target groups
+ [ecs] TagResource, UntagResource, and ListTagsForResource with tag validation
+ [eks] TagResource, UntagResource, and ListTagsForResource with tag validation
+ [elasticache] AddTagsToResource, ListTagsForResource, and RemoveTagsForResource with tag validation
+ [msk] TagResource, UntagResource, and ListTagsForResource
+ [rds] AddTagsToResource, ListTagsForResource, and RemoveTagsForResource
+ [scheduler] TagResource, UntagResource, and ListTagsForResource
+ [pipes] TagResource, UntagResource, and ListTagsForResource
+ [waf] TagResource, UntagResource, and ListTagsForResource for WebACLs
+ [iam] TagRole, UntagRole, ListRoleTags, TagUser, UntagUser, and ListUserTags use shared tag accessors
+ [kms] TagResource, UntagResource, and ListResourceTags use shared tag accessors
+ [secretsmanager] TagResource uses shared tag accessors
+ [sqs] TagQueue, UntagQueue, and ListQueueTags use shared tag accessors
+ [serviceutil] shared Taggable interface, generic ApplyTags/RemoveTags/ListTags helpers, and TagStore + NSStore for separate-namespace tag storage
