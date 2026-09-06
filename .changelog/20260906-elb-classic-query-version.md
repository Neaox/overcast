* [elbv2/router] an ELB Classic call (API version `2012-06-01`) now gets a 501 instead of an ELBv2-shaped answer
  Classic and ELBv2 share the `elasticloadbalancing` signing name and most action names, so `DescribeTags` was answered 200 by ELBv2 and `CreateLoadBalancer` 400, naming an ELBv2 member.
  a Query request is resolved by the API version the pinned models attribute it to before any action-name match, so the log, trace and IAM labels name the service that answered.
