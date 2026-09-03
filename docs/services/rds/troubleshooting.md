---
title: "RDS troubleshooting"
description: "Symptoms and fixes for RDS: an instance stuck creating or in failed, a refused connection, a rejected password change, and a slow cdk deploy."
section: "Service Reference"
tags:
  - database
  - docs
  - rds
  - services
  - troubleshooting
---

# RDS troubleshooting

Symptom, cause, fix. Back to [RDS](../rds.md).

## The instance sits in `creating` for ~30 seconds

**Cause.** That is the engine initialising its data directory before it accepts
connections, and `CreateDBInstance` is asynchronous exactly as it is on AWS.

**Fix.** Poll `DescribeDBInstances` — or `aws rds wait db-instance-available` —
until it reports `available`, and the first connection attempt will succeed.
Nothing is left in `creating` indefinitely: five minutes without engine
readiness fails the instance instead.

## The instance went to `failed` and `DescribeDBInstances` does not say why

**Cause.** AWS's `DBInstance` shape has no `StatusReason` field, and
`StatusInfos` is documented as read-replica-only, so the reason goes where AWS
puts it: RDS events.

**Fix.**

```bash
aws rds describe-events --source-identifier mydb --source-type db-instance \
  --duration 1440
```

`DescribeEvents` records creation, start, stop, deletion and both failure
events (RDS-EVENT-0278 while being created, RDS-EVENT-0035 while being
started), each carrying the reason. Events are kept 14 days, at most 1000 per
region, and are returned oldest-first. A call with neither `StartTime` nor
`Duration` returns the past 60 minutes — AWS's default, and the usual reason
one comes back empty.

For the engine's own output, the console's Logs tab reads
`GET /_overcast/rds/instances/{id}/logs`, an emulator-only endpoint. It returns
the live container's output when there is one, and otherwise the bounded tail
captured when that container died — a database that failed to start usually has
no container left by the time anyone looks.

## A connection that used to work is refused

**Cause.** Placement. An instance created with a `DBSubnetGroupName` lands on
that VPC's network and nothing else, so a caller outside the VPC — including
your own machine — cannot reach it.

**Fix.** Put the caller in the same VPC, or create the instance with
`PubliclyAccessible=true`, which keeps it on the default plane as well. See
[Lambda, ECS and VPCs](../../networking/vpcs.md) and the
[reachability defaults](./limitations.md#reachability-defaults).

## The endpoint resolves but nothing answers

**Cause.** `OVERCAST_RDS_MODE=mock`, or no reachable Docker daemon. Both are
metadata-only: `DescribeDBInstances` still reports an address and port because
the record has them, and no engine exists behind either.

**Fix.** Unset `OVERCAST_RDS_MODE` and make sure Docker is running. `live` is
the default and starts nothing when Docker is absent, so an unset variable is
not a guarantee that a container exists.

## `ModifyDBInstance` refuses the new password

**Cause.** One of three, and the error says which: the instance is not
`available` (`InvalidDBInstanceState`); the password breaks the engine's RDS
constraints; or the engine itself refused the `ALTER USER`, which fails the
whole call so nothing partial is recorded.

**Fix.** Start the instance first, and check the password against the
[per-engine rules](./limitations.md#password-rules) — in particular that
`GetRandomPassword` and CDK's `Credentials.fromGeneratedSecret` can generate
characters RDS forbids.

A stopped or failed instance is refused rather than remembered: a container
reads `MYSQL_ROOT_PASSWORD` (or its equivalent) once, when it initialises its
data directory, so there is no later moment at which a pending password could be
applied.

**Clusters rotate member by member.** `ModifyDBCluster` applies the password to
each member through the same path. If one refuses, the call fails and names it,
and the members already rotated keep the new password.

## `Start` reports success but the database is gone

**Cause.** The container was removed out from under Overcast — a `docker prune`,
or a container deleted by hand.

**Fix.** Nothing: starting an instance whose container Docker no longer has
rebuilds it rather than reporting a start that started nothing. The **data**
does not come back, though, because an engine container has no volume and no
bind mount — the database lives in the container's writable layer and goes with
it.

## Orphaned containers pile up

**Cause.** With the default `memory` state backend, the instance identity that
scopes the startup sweep is minted afresh on every start, so a crashed run's
containers are not reclaimed at the next startup. An orderly shutdown still
cleans up after itself.

**Fix.**

```bash
docker rm $(docker ps -aq --filter label=overcast.managed=true)
```

## `cdk deploy` blocks for a long time on the database

**Cause.** Intended. An `AWS::RDS::DBInstance` is not `CREATE_COMPLETE` until
the instance reports `available`, and `AWS::RDS::DBCluster` behaves the same
way, so a `DependsOn`, a `Fn::GetAtt` on `Endpoint.Address` or a migration task
waits behind it, as it does on AWS. A deploy that returns success is a deploy
whose database accepts connections. An instance that fails takes the resource
down with the reason RDS recorded, and the stack rolls back rather than leaving
the database behind.

**Fix.** If you want the control plane without the wait, set
`OVERCAST_RDS_MODE=mock` — accepting that nothing will be listening on the
endpoint.

## Related

- [RDS](../rds.md) — quick start and what works
- [RDS limitations](./limitations.md) — what is enforced and what is only recorded
- [RDS operations](./operations.md) — per-operation status
- [Data-plane endpoints](../../networking/data-plane-endpoints.md) — when an endpoint name resolves nowhere
