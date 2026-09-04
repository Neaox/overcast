* [ec2/router] VPC networks are no longer deleted when Overcast restarts into a schema migration, stranding every ECS task and Lambda in the VPC
  The Docker reconcile ran on the daemon's connected event without waiting for the state store, so on an upgrade carrying a migration it read no VPCs at all.
  Every EC2 network then looked unclaimed and was swept as an orphan, and the completed pass vouched for the result, so nothing looked at the question again.
  Placements failed with `ResourceInitializationError: … connect to VPC network <id>: 404: network <id> not found` for the life of the process, through redeploys alike.
* [elbv2] `ModifyLoadBalancerAttributes` and `DescribeLoadBalancerAttributes` are emulated rather than answering 501
  CloudFormation treats a failed in-place update as a replacement, and CDK sets `deletion_protection.enabled` on every load balancer it creates.
  So each stack update tore the load balancer down and took its listener and the target group the ECS service was registered with along with it.
~ [web] Back out of a request trace returns to where you were in the list instead of the top
  Scroll offsets are cached per history entry, so a fresh navigation to the same page still starts at the top.
