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

## Summary

| Operation                      | Status         | Notes                                       |
| ------------------------------ | -------------- | ------------------------------------------- |
| `CreateLoadBalancer`           | ✅ Supported   | Creates load balancer metadata and DNS name |
| `DescribeLoadBalancers`        | ✅ Supported   | Lists load balancers                        |
| `DeleteLoadBalancer`           | ✅ Supported   | Deletes load balancer metadata              |
| `CreateTargetGroup`            | ✅ Supported   | Creates target group metadata               |
| `DescribeTargetGroups`         | ✅ Supported   | Lists target groups                         |
| `DeleteTargetGroup`            | ✅ Supported   | Deletes target group metadata               |
| `CreateListener`               | ✅ Supported   | Creates listener metadata                   |
| `DescribeListeners`            | ✅ Supported   | Lists listeners                             |
| `DeleteListener`               | ✅ Supported   | Deletes listener metadata                   |
| `RegisterTargets`              | ✅ Supported   | Registers targets in a target group         |
| `DeregisterTargets`            | ✅ Supported   | Deregisters targets from a target group     |
| `DescribeTargetHealth`         | ✅ Supported   | Returns synthetic healthy target state      |
| `CreateRule`                   | ❌ Unsupported | Not implemented                             |
| `DescribeRules`                | ❌ Unsupported | Not implemented                             |
| `ModifyLoadBalancerAttributes` | ❌ Unsupported | Not implemented                             |

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
- `DescribeTargetHealth` reports every registered target `healthy`; there are no
  health checks behind it.
- Only `forward` listener actions have a data-plane effect. Others are stored
  and echoed back.
