---
title: "ELBv2 — Elastic Load Balancing v2 (ALB/NLB)"
description: "ELBv2 is served as an AWS Query API (Action=..., Version=2015-12-01). A load balancer forwards requests arriving on its DNS name to the targets registered behind it."
section: "Service Reference"
tags:
  - alb
  - balancing
  - docs
  - elastic
  - elb
  - elbv2
  - load
  - nlb
  - services
---

# ELBv2 — Elastic Load Balancing v2 (ALB/NLB)

> AWS docs: https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/Welcome.html

ELBv2 is served as an AWS Query API (`Action=...`, `Version=2015-12-01`).
A load balancer is not metadata alone: a request addressed to its DNS name is
forwarded to a target registered behind it, which is what makes a service
deployed behind one reachable at the URL it was given.

## Behavior Notes

- Service name in Overcast is `elbv2`.
- Version ownership is `2015-12-01`.
- **Requests are forwarded.** A request whose `Host` is a load balancer's
  `DNSName` is proxied round robin to a registered target of the target group
  its listener's `DefaultActions` forward to, preserving the `Host` so an
  application behind it builds its own links correctly. A load balancer with
  nothing registered behind it answers `503`, as ALB does.
- The DNS name does not resolve on its own — nothing listens on port 80. Reach
  a load balancer by sending its `DNSName` as the `Host` header to Overcast's
  own port: `curl -H "Host: <DNSName>" http://localhost:4566/`. Setting
  `OVERCAST_HOSTNAME` to a resolvable base puts the name in the split-horizon
  zone, where it resolves like any other Overcast endpoint.
- An ECS service with a `loadBalancers` configuration registers and deregisters
  its tasks as it places and stops them, so scaling the service changes what the
  load balancer forwards to.
- `DescribeTargetHealth` reports every registered target `healthy`. A target
  group's `HealthCheck*` properties (path, port, protocol, interval, timeout,
  thresholds, `Matcher`) are stored and echoed back by `DescribeTargetGroups`,
  but nothing evaluates them against a target's actual state.
- Only `forward` listener actions have a data-plane effect. `RedirectConfig`
  and `FixedResponseConfig` round-trip through `DescribeListeners`, but a
  request against such a listener is still forwarded rather than redirected
  or answered with the fixed body. Weighted `ForwardConfig`,
  `Certificates`/`SslPolicy`/`AlpnPolicy`/`MutualAuthentication`, the
  Cognito/OIDC auth actions, and listener rules (`CreateRule`/`DescribeRules`)
  are not modelled at all.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category       | ✅ Supported | ❌ Unsupported |
| -------------- | ------------ | -------------- |
| Load Balancers | 3            | 1              |
| Target Groups  | 6            |                |
| Listeners      | 3            |                |
| Targets        | 3            |                |
| Listener Rules |              | 2              |
| Tags           | 3            |                |

---

## Endpoints

### Load Balancers

| Operation                      | Status         | Notes                                                                 | AWS Docs                                                                                                           |
| ------------------------------ | -------------- | --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `CreateLoadBalancer`           | ✅ Supported   | Threads Type, Scheme, IpAddressType, Subnets, SecurityGroups and Tags | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateLoadBalancer.html)           |
| `DescribeLoadBalancers`        | ✅ Supported   |                                                                       | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeLoadBalancers.html)        |
| `DeleteLoadBalancer`           | ✅ Supported   |                                                                       | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DeleteLoadBalancer.html)           |
| `ModifyLoadBalancerAttributes` | ❌ Unsupported |                                                                       | [docs](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_ModifyLoadBalancerAttributes.html) |

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

<!-- END overcast:capabilities -->
