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

## Operations

18 of 21 listed operations are implemented.
Per-operation status, notes and AWS API links: [ELBv2 operations](elb/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
