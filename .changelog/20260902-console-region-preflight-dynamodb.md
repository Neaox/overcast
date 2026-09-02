* [web/dynamodb/sns/kinesis] an empty DynamoDB, SNS or Kinesis list in the console now says when the resources are in another region
  A CLI deploying into the region `AWS_REGION` names while the console lists the server default read as "ListTables returns []"; the empty state now names the region that has them and offers to switch, as the stack, queue and function pages already did.
  Credentials never partition state: a signed CLI client and the console, in the same region, see the same tables, queues and buckets, and integration tests now pin that.
