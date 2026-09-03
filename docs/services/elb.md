---
title: "ELBv2 — Elastic Load Balancing v2 (ALB/NLB)"
description: "Quick start, how to reach a load balancer without DNS, what actually forwards to a target, and the listener features that are stored or refused."
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

A request addressed to a load balancer's DNS name is forwarded to a target
registered behind it, so a service deployed behind one is reachable at the URL
it was given.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
VPC=$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId' --output text)
SUBNET=$(aws ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC" --query 'Subnets[0].SubnetId' --output text)

TG=$(aws elbv2 create-target-group --name web --protocol HTTP --port 80 --vpc-id "$VPC" \
  --target-type ip --query 'TargetGroups[0].TargetGroupArn' --output text)
LB=$(aws elbv2 create-load-balancer --name web --subnets "$SUBNET" \
  --query 'LoadBalancers[0].LoadBalancerArn' --output text)
aws elbv2 create-listener --load-balancer-arn "$LB" --protocol HTTP --port 80 \
  --default-actions Type=forward,TargetGroupArn="$TG"
aws elbv2 register-targets --target-group-arn "$TG" --targets Id=172.17.0.4,Port=8080

DNS=$(aws elbv2 describe-load-balancers --load-balancer-arns "$LB" \
  --query 'LoadBalancers[0].DNSName' --output text)
curl -H "Host: $DNS" http://localhost:4566/
```

Register the address of something that actually listens — a container on
Overcast's network, or a port on your host. An ECS service with a
`loadBalancers` configuration does this for you.

The DNS name does not resolve on its own — nothing listens on port 80. Send it
as the `Host` header to Overcast's own port, or set `OVERCAST_HOSTNAME` to a
resolvable base, which puts the name in the split-horizon DNS zone where it
resolves like any other Overcast endpoint — see
[Hostnames and DNS](../networking/hostnames.md). A bare `localhost` base cannot;
the zone excludes it.

## What works

| Area | Behaviour |
| --- | --- |
| Forwarding | A request whose `Host` matches a load balancer's `DNSName` is proxied round-robin to a registered target of the target group its listener forwards to, preserving `Host` so the application builds its own links correctly |
| Empty pools | A load balancer with nothing registered behind it answers `503`, as ALB does |
| ECS integration | A service with a `loadBalancers` configuration registers and deregisters its tasks as it places and stops them, so scaling the service changes what the load balancer forwards to |
| Target groups | CRUD, attributes, and registration or deregistration by target |
| Tags | `AddTags`, `RemoveTags`, `DescribeTags` |

`DNSName` is `{name}-{id}.{region}.elb.{hostname}`, minted on the host you
reached Overcast on.

## Differences from AWS

| Area | Overcast |
| --- | --- |
| Health checks | `DescribeTargetHealth` reports every registered target `healthy`. A target group's `HealthCheck*` properties are stored and echoed, but nothing evaluates them |
| Listener actions | Only `forward` reaches the data plane |
| `RedirectConfig`, `FixedResponseConfig` | Round-trip through `DescribeListeners`, but are not applied — a listener carrying only one of these has no target group, so a request to it gets `503` |
| Listener rules | `CreateRule` and `DescribeRules` return `501`. Only the listener's `DefaultActions` route |
| `ModifyLoadBalancerAttributes` | Returns `501` |
| Not modelled | Weighted `ForwardConfig`, `Certificates`, `SslPolicy`, `AlpnPolicy`, `MutualAuthentication`, and the Cognito/OIDC authenticate actions |
| Listener selection | With several listeners, the port in the `Host` header picks one, defaulting to 80; otherwise the first listener is used |

## Gotchas

> [!WARNING]
> A CDK stack's usual HTTP→HTTPS listener pair does not work here. The port-80
> listener carries only a `RedirectConfig`, so it forwards to no target group
> and every request through it gets `503`. Point the client at the forwarding
> listener instead.

<!-- BEGIN overcast:capabilities -->

## Operations

18 of 21 listed operations are implemented.
Per-operation status, notes and AWS API links: [ELBv2 operations](elb/operations.md).

<!-- END overcast:capabilities -->

## Related

- [ECS](./ecs.md) — services register their tasks as targets
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/Welcome.html)
