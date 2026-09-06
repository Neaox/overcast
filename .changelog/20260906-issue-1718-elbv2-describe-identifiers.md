*! [elbv2] `Describe*` raises the documented not-found error for an ARN or name it cannot resolve, instead of an empty 200.
  DescribeLoadBalancers, DescribeTargetGroups and DescribeListeners, by ARN and by name; a value that is not an ARN for the resource raises `ValidationError`.
  a `Describe*` naming no identifier still lists the region, and `DeleteListener` now names `ListenerNotFound` rather than `LoadBalancerNotFound`.
  `CreateLoadBalancer` requires at least one subnet, `CreateTargetGroup` validates `Protocol` and `Port`, and new ARNs end in AWS's sixteen hex characters.
  migration: code that named a load balancer or target group and read the empty list as "it is gone" must catch the error, as it already has to against AWS.
