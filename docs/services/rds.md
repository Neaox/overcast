---
title: "RDS — Relational Database Service"
description: "Quick start, the engines and Aurora shapes started for real, readiness and failure reporting, per-caller endpoints, and the backups, replicas and failover that are absent."
section: "Service Reference"
tags:
  - database
  - docs
  - rds
  - relational
  - service
  - services
---

# RDS — Relational Database Service

`CreateDBInstance` starts a real database container, and the instance reports
`available` only once that engine is genuinely accepting connections.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws rds create-db-instance --db-instance-identifier mydb \
  --engine postgres --db-instance-class db.t3.micro \
  --allocated-storage 20 --master-username app --master-user-password secret99

aws rds wait db-instance-available --db-instance-identifier mydb
aws rds describe-db-instances --db-instance-identifier mydb \
  --query 'DBInstances[0].Endpoint'
# → { "Address": "127.0.0.1", "Port": 33060 }  on the host
psql -h 127.0.0.1 -p 33060 -U app postgres
```

First boot takes roughly half a minute — the engine has to initialise its data
directory before it accepts anything.

## What works

| Area | Behaviour |
| --- | --- |
| Real engines | MySQL, PostgreSQL, MariaDB and both Aurora variants, in containers, with ports allocated from `RDS_PORT_BASE` (default 33060) |
| Honest readiness | `available` means Overcast opened a TCP connection, ran the credential initialisation, and the engine's own readiness client confirmed the final server is listening — on create and on start alike |
| Failure is terminal | A container that exits, cannot be created, or misses engine readiness within five minutes goes to `failed`. Nothing is left in `creating` or `starting` forever |
| Per-caller endpoints | `{id}.{region}.rds.{base}` resolves to the engine container from a Lambda or ECS task, on the engine's own port; the host gets the published port. One stack output works from both sides |
| Aurora | Clusters, member instances that inherit the cluster's placement and credentials, writer promotion on deleting the writer, and both cluster endpoint names |
| Master account | A real administrator account with the privileges AWS grants — `CREATEDB`/`CREATEROLE`/`rds_superuser` on PostgreSQL, the version-appropriate grant set on the MySQL family |
| Password rotation | `ModifyDBInstance` and `ModifyDBCluster` run the engine's own `ALTER USER`, so the old password really stops working |
| Reachability | `PubliclyAccessible` decides it, not just metadata — an instance in a subnet group is reachable only from that VPC |
| CloudFormation | A `DBInstance` or `DBCluster` is not `CREATE_COMPLETE` until the database is `available`, so anything downstream of it waits, as on AWS |
| Diagnostics | `DescribeEvents` records the lifecycle, including why an instance failed; the console's Logs tab reads the container's output |
| Persistence | Containers survive an Overcast restart, and one Overcast never reclaims another's — engine containers carry the identity of the state store that created them |

## Differences from AWS

| Area                                  | Overcast                                                                                                                                                                       |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| The reader endpoint serves the writer | Every member has its own storage, so a reader endpoint spread across replicas would answer from an empty database. Reads are not distributed; replica lag cannot be reproduced |
| Recorded, not enforced                | Backup windows, maintenance windows, parameter groups, security groups and log exports are stored and reported, never acted on                                                 |
| No backups or snapshots               | Snapshot, restore and point-in-time operations are not implemented                                                                                                             |
| Engine version substitution           | Any `EngineVersion` is accepted; one with no image of its own is served by the nearest in its family, and the substitution is logged                                           |
| Not every engine                      | SQL Server, Oracle and Db2 are not emulated — see [RDS limitations](./rds/limitations.md)                                                                                        |
| No failover API                       | There is no `FailoverDBCluster`; deleting the writer is the only way to trigger a promotion                                                                                    |

Full engine matrix, cluster-setting behaviour and master-account boundaries:
[RDS limitations](./rds/limitations.md). Symptoms and fixes:
[RDS troubleshooting](./rds/troubleshooting.md).

## Gotchas

`OVERCAST_RDS_MODE=mock` reaches `available` in a moment and starts no
container, while `DescribeDBInstances` still reports an address and port with
nothing listening on them. Use it when you are testing the control plane; keep
the default if your code connects.

> [!CAUTION]
> Promoting a new writer moves both cluster endpoint names onto its container,
> which means detaching and re-attaching it to its Docker networks. Connections
> held open to that container over those networks are dropped — the same
> interruption a real failover causes.

<!-- BEGIN overcast:capabilities -->

## Operations

24 of 34 listed operations are implemented.
Per-operation status, notes and AWS API links: [RDS operations](rds/operations.md).

<!-- END overcast:capabilities -->

## Related

- [RDS limitations](./rds/limitations.md)
- [RDS troubleshooting](./rds/troubleshooting.md)
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [Networking § data-plane endpoints](../networking/data-plane-endpoints.md)
- [AWS API reference](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/Welcome.html)
