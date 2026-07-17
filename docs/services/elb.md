---
title: "ELBv2 — Elastic Load Balancing v2 (ALB/NLB)"
description: "ELBv2 is served as an AWS Query API (Action=..., Version=2015-12-01). This implementation is metadata-only and does not proxy real traffic."
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
This implementation is metadata-only and does not proxy real traffic.

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
- No real traffic forwarding is implemented.
- DNS names are synthetic and intended for predictable local testing behavior, not production traffic.
- Listener and target-group relationships are stored as metadata only.
