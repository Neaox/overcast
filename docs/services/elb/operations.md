---
title: "ELBv2 operations"
description: "Every ELBv2 operation Overcast declares — 20 of 22 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - elb
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# ELBv2 operations

20 of 22 listed operations are implemented. Back to [ELBv2](../elb.md).

## Summary

| Category       | ✅ Supported | ❌ Unsupported |
| -------------- | ------------ | -------------- |
| Load Balancers | 5            |                |
| Target Groups  | 6            |                |
| Listeners      | 3            |                |
| Targets        | 3            |                |
| Listener Rules |              | 2              |
| Tags           | 3            |                |

---

## Endpoints

### Load Balancers

| Operation                        | Status       | Notes                                                                                                                             | AWS Docs                                                                                                             |
| -------------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `CreateLoadBalancer`             | ✅ Supported | Threads Type, Scheme, IpAddressType, Subnets, SecurityGroups and Tags                                                             | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateLoadBalancer.html)             |
| `DescribeLoadBalancers`          | ✅ Supported |                                                                                                                                   | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeLoadBalancers.html)          |
| `DeleteLoadBalancer`             | ✅ Supported |                                                                                                                                   | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DeleteLoadBalancer.html)             |
| `ModifyLoadBalancerAttributes`   | ✅ Supported | Stores and echoes attributes such as idle_timeout.timeout_seconds and deletion_protection.enabled; not enforced by the data plane | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_ModifyLoadBalancerAttributes.html)   |
| `DescribeLoadBalancerAttributes` | ✅ Supported |                                                                                                                                   | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeLoadBalancerAttributes.html) |

### Target Groups

| Operation                       | Status       | Notes                                                                                                                                                                | AWS Docs                                                                                                            |
| ------------------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `CreateTargetGroup`             | ✅ Supported | Threads TargetType, ProtocolVersion, IpAddressType, the HealthCheck* family, Matcher and Tags; health checks are stored and echoed but not evaluated against targets | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateTargetGroup.html)             |
| `DescribeTargetGroups`          | ✅ Supported |                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTargetGroups.html)          |
| `DeleteTargetGroup`             | ✅ Supported |                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DeleteTargetGroup.html)             |
| `ModifyTargetGroup`             | ✅ Supported | Updates the HealthCheck* family and Matcher; TargetType/Protocol/Port/VpcId require replacement                                                                      | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_ModifyTargetGroup.html)             |
| `ModifyTargetGroupAttributes`   | ✅ Supported | Stores and echoes attributes such as deregistration_delay.timeout_seconds; not enforced by the data plane                                                            | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_ModifyTargetGroupAttributes.html)   |
| `DescribeTargetGroupAttributes` | ✅ Supported |                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTargetGroupAttributes.html) |

### Listeners

| Operation           | Status       | Notes                                                                                                                                                                                                                                        | AWS Docs                                                                                                |
| ------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `CreateListener`    | ✅ Supported | Forwards each DefaultActions member's Type, TargetGroupArn, Order, RedirectConfig and FixedResponseConfig; weighted ForwardConfig, Certificates/SslPolicy/AlpnPolicy/MutualAuthentication and the Cognito/OIDC auth actions are not modelled | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateListener.html)    |
| `DescribeListeners` | ✅ Supported |                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeListeners.html) |
| `DeleteListener`    | ✅ Supported |                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DeleteListener.html)    |

### Targets

| Operation              | Status       | Notes                                                                                    | AWS Docs                                                                                                   |
| ---------------------- | ------------ | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `RegisterTargets`      | ✅ Supported |                                                                                          | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_RegisterTargets.html)      |
| `DeregisterTargets`    | ✅ Supported |                                                                                          | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DeregisterTargets.html)    |
| `DescribeTargetHealth` | ✅ Supported | Always reports "healthy"; the stored HealthCheck* block is not evaluated against targets | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTargetHealth.html) |

### Listener Rules

| Operation       | Status         | Notes | AWS Docs                                                                                            |
| --------------- | -------------- | ----- | --------------------------------------------------------------------------------------------------- |
| `CreateRule`    | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateRule.html)    |
| `DescribeRules` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeRules.html) |

### Tags

| Operation      | Status       | Notes                                               | AWS Docs                                                                                           |
| -------------- | ------------ | --------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `AddTags`      | ✅ Supported | Adds tags to load balancers and target groups       | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_AddTags.html)      |
| `RemoveTags`   | ✅ Supported | Removes tags from load balancers and target groups  | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_RemoveTags.html)   |
| `DescribeTags` | ✅ Supported | Describes tags for load balancers and target groups | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTags.html) |

## Related

- [ELBv2](../elb.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
