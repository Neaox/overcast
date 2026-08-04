---
title: "RDS — Relational Database Service"
description: "RDS uses the AWS Query protocol (form-encoded POST, XML responses). Operations are identified by the Action parameter with API version 2014-10-31."
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

> AWS docs: https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/Welcome.html

RDS uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2014-10-31`.

### Instance status is what actually happened

An instance only reports `available` once Overcast has opened a TCP connection
to the engine. If the container exits, cannot be started, or never accepts a
connection within five minutes, the instance goes to AWS's `failed` status
rather than being declared available anyway — a database you cannot connect to
is not available, and discovering that by trying to connect is worse than being
told. An instance is never left in `starting` indefinitely.

Starting an instance whose container Docker no longer has (a `docker prune`, a
container removed by hand) rebuilds it rather than reporting a start that
started nothing. Containers belonging to a stopped instance survive an Overcast
restart, so a stopped database can always be started again.

When Docker is available, `CreateDBInstance` starts a real database container
(mysql, postgres, mariadb, aurora-mysql, aurora-postgresql) with automatic port
allocation from `RDS_PORT_BASE` (default 33060). When Docker is unavailable,
operations are metadata-only.

### Finding out why an instance failed — `DescribeEvents`

A `failed` instance records why, but that reason is not in the
`DescribeDBInstances` response and deliberately so: the real `DBInstance` has
no `StatusReason` field, and `StatusInfos` is documented as read-replica-only.
AWS's general channel for "why did that happen to my database" is RDS events,
so that is where Overcast puts it.

`DescribeEvents` returns `db-instance` events for the instance lifecycle
Overcast observes:

| Event | Categories | Message |
| --- | --- | --- |
| Instance created | `creation` | `DB instance created.` |
| Instance started | `notification` | `DB instance started.` (RDS-EVENT-0088) |
| Instance stopped | `notification` | `DB instance stopped.` (RDS-EVENT-0087) |
| Instance deleted | `deletion` | `DB instance deleted.` |
| Failed while being created | `failure` | `The DB instance creation failed. {reason}.` (RDS-EVENT-0278) |
| Failed while being started | `failure` | `Database instance put into failed state. {reason}.` (RDS-EVENT-0035) |

Events are kept for 14 days, as on AWS, and the store holds at most 1000 per
region. Calling `DescribeEvents` with neither `StartTime` nor `Duration`
returns the past 60 minutes — AWS's default, and the usual reason a call comes
back empty. `SourceIdentifier`, `SourceType`, `EventCategories`,
`StartTime`/`EndTime`/`Duration` and `Marker`/`MaxRecords` all apply; events are
returned oldest-first. Overcast only records `db-instance` events, so a query
for another source type is an empty list rather than an error.

### Instance logs — `GET /_rds/instances/{id}/logs`

An emulator-only endpoint (not part of the AWS API) that the web UI's Logs tab
renders. It returns the container's live output when there is a container, and
otherwise the bounded tail Overcast captured when that container died — which
is the case that matters, because a database that failed to start usually has
no container left by the time anyone looks. The response also carries the
instance's status and failure reason, so the tab can explain an empty log
rather than just showing one. A DB instance with nothing at all to report
answers `404`; a missing container is never a `500`.

---

## Engine support

Overcast emulates the open-source RDS engines that have freely available Docker images, including both Aurora variants.

### Supported engines

| Engine            | AWS value           | Default version | Underlying Docker image                    |
| ----------------- | ------------------- | --------------- | ------------------------------------------ |
| PostgreSQL        | `postgres`          | 16.1            | `postgres:16`                              |
| MySQL             | `mysql`             | 8.0             | `mysql:8.0`                                |
| MariaDB           | `mariadb`           | 11.4            | `mariadb:11`                               |
| Aurora MySQL      | `aurora-mysql`      | 3.04            | `mysql:8.0` (3.x), `mysql:5.7` (2.x)       |
| Aurora PostgreSQL | `aurora-postgresql` | 15.4            | `postgres:15` (15.x), `postgres:14` (14.x) |

Any `EngineVersion` is accepted. A version with no image of its own is served by
the nearest one in its family — `8.0.39` runs `mysql:8.0`, `16.3` runs
`postgres:16` — and the substitution is logged. Real stacks send precise
versions (CDK's `MysqlEngineVersion.VER_8_0_39`), and refusing them left the
instance `available` with no database behind it.

### Connecting to an instance

`Endpoint.Address` is an AWS-shaped hostname,
`{dbInstanceIdentifier}.{region}.rds.{base}`, where `{base}` is
`OVERCAST_HOSTNAME` when set and otherwise the host the request arrived on. It
resolves — and the port you are given is the one that works — from both sides of
the container boundary:

| Caller | Address | Port |
| --- | --- | --- |
| A Lambda function or ECS task | the endpoint hostname, resolved to the engine container by Docker's embedded DNS | the engine's own port (3306 / 5432) |
| The host | the same hostname (or `127.0.0.1` when `{base}` has no wildcard DNS) | the published port, from `RDS_PORT_BASE` |

So a value read from a stack output on the host and a value read inside a task
name the same database and both connect. Full mechanism, and the one caveat
about `Endpoint.Port` crossing into container environment, in
[networking.md](../networking.md#data-plane-endpoints--rds-and-anything-else-that-is-a-container).

### Aurora emulation

`aurora-mysql` and `aurora-postgresql` are emulated using the underlying MySQL and PostgreSQL Docker
images respectively — both Aurora variants use the same wire protocol as their open-source counterparts.
The Aurora cluster/instance resource model (`CreateDBCluster` → `CreateDBInstance` with `DBClusterIdentifier`)
is fully supported. Docker containers are started when instances are added to the cluster.

### SQL Server — not yet implemented

`sqlserver-ee`, `sqlserver-se`, `sqlserver-ex`, and `sqlserver-web` are not yet implemented. A free Docker image is available (`mcr.microsoft.com/mssql/server`) so SQL Server emulation is planned for a future release.

### Oracle — not feasible

`oracle-ee`, `oracle-ee-cdb`, `oracle-se2`, `oracle-se2-cdb`, and their `custom-oracle-*` variants all require an Oracle Technology Network (OTN) commercial license and Oracle-provided database images that cannot be freely redistributed. Overcast cannot bundle or pull Oracle images, so these engines cannot be emulated.

### IBM Db2 — not yet implemented

`db2-ae` and `db2-se` are not yet implemented. A community Docker image exists (`icr.io/db2_community/db2`) but Db2 is rarely used in local development workflows, so this is low priority.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category         | ✅ Supported | ❌ Unsupported |
| ---------------- | ------------ | -------------- |
| DB instances     | 6            | 7              |
| Aurora clusters  | 6            | 3              |
| Events           | 1            |                |
| Engine metadata  | 2            |                |
| Subnet groups    | 3            |                |
| Parameter groups | 3            |                |
| General          |              | 3              |

---

## Endpoints

### DB instances

| Operation                         | Status         | Notes                                                                                                                                                   | AWS Docs                                                                                                   |
| --------------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `CreateDBInstance`                | ✅ Supported   | Docker-backed when available; async creating→available; mysql/postgres/mariadb/aurora-mysql/aurora-postgresql; accepts `DBClusterIdentifier` for Aurora | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBInstance.html)                |
| `DescribeDBInstances`             | ✅ Supported   | List all or filter by DBInstanceIdentifier                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBInstances.html)             |
| `DeleteDBInstance`                | ✅ Supported   | Sets status to "deleting"; stops+removes Docker container                                                                                               | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBInstance.html)                |
| `StopDBInstance`                  | ✅ Supported   | Stops Docker container; available→stopping→stopped                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StopDBInstance.html)                  |
| `StartDBInstance`                 | ✅ Supported   | Starts Docker container; stopped→starting→available                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StartDBInstance.html)                 |
| `ModifyDBInstance`                | ✅ Supported   | Metadata updates (class, storage, engine version, multi-AZ)                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_ModifyDBInstance.html)                |
| `RebootDBInstance`                | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_RebootDBInstance.html)                |
| `CreateDBSnapshot`                | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBSnapshot.html)                |
| `DeleteDBSnapshot`                | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBSnapshot.html)                |
| `DescribeDBSnapshots`             | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBSnapshots.html)             |
| `RestoreDBInstanceFromDBSnapshot` | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_RestoreDBInstanceFromDBSnapshot.html) |
| `DescribeDBLogFiles`              | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBLogFiles.html)              |
| `DownloadDBLogFilePortion`        | ❌ Unsupported | stub; returns 501                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DownloadDBLogFilePortion.html)        |

### Aurora clusters

| Operation                    | Status         | Notes                                                                                      | AWS Docs                                                                                              |
| ---------------------------- | -------------- | ------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------- |
| `CreateDBCluster`            | ✅ Supported   | aurora-mysql and aurora-postgresql only; logical cluster, Docker started on first instance | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBCluster.html)            |
| `DescribeDBClusters`         | ✅ Supported   | List all or filter by DBClusterIdentifier; returns cluster members                         | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBClusters.html)         |
| `DeleteDBCluster`            | ✅ Supported   | Sets status to "deleting"; async removal                                                   | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBCluster.html)            |
| `ModifyDBCluster`            | ✅ Supported   | Engine version update                                                                      | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_ModifyDBCluster.html)            |
| `StartDBCluster`             | ✅ Supported   | stopped→starting→available                                                                 | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StartDBCluster.html)             |
| `StopDBCluster`              | ✅ Supported   | available→stopping→stopped                                                                 | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StopDBCluster.html)              |
| `CreateDBClusterSnapshot`    | ❌ Unsupported | stub; returns 501                                                                          | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBClusterSnapshot.html)    |
| `DeleteDBClusterSnapshot`    | ❌ Unsupported | stub; returns 501                                                                          | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBClusterSnapshot.html)    |
| `DescribeDBClusterSnapshots` | ❌ Unsupported | stub; returns 501                                                                          | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBClusterSnapshots.html) |

### Events

| Operation        | Status       | Notes                                                                                                                                                                                                             | AWS Docs                                                                                  |
| ---------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `DescribeEvents` | ✅ Supported | db-instance events for create/start/stop/delete/failure; 14-day retention, 60-minute default window; `SourceIdentifier`, `SourceType`, `EventCategories`, `StartTime`/`EndTime`/`Duration`, `Marker`/`MaxRecords` | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeEvents.html) |

### Engine metadata

| Operation                            | Status       | Notes                                                                                                                             | AWS Docs                                                                                                      |
| ------------------------------------ | ------------ | --------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `DescribeDBEngineVersions`           | ✅ Supported | mysql (8.0, 5.7), postgres (16.1, 15.5, 14.11), mariadb (11.4, 10.11), aurora-mysql (3.04, 2.11), aurora-postgresql (15.4, 14.11) | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBEngineVersions.html)           |
| `DescribeOrderableDBInstanceOptions` | ✅ Supported | Static list of engine + instance class combos for mysql/postgres/mariadb                                                          | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeOrderableDBInstanceOptions.html) |

### Subnet groups

| Operation                | Status       | Notes                                       | AWS Docs                                                                                          |
| ------------------------ | ------------ | ------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `CreateDBSubnetGroup`    | ✅ Supported | Metadata-only; stores subnet IDs and VPC ID | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBSubnetGroup.html)    |
| `DescribeDBSubnetGroups` | ✅ Supported | List all or filter by name                  | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBSubnetGroups.html) |
| `DeleteDBSubnetGroup`    | ✅ Supported |                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBSubnetGroup.html)    |

### Parameter groups

| Operation                   | Status       | Notes                                                   | AWS Docs                                                                                             |
| --------------------------- | ------------ | ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateDBParameterGroup`    | ✅ Supported | Validates family against known engines; stores in state | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBParameterGroup.html)    |
| `DescribeDBParameterGroups` | ✅ Supported | List all or filter by name                              | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBParameterGroups.html) |
| `DeleteDBParameterGroup`    | ✅ Supported |                                                         | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBParameterGroup.html)    |

### General

| Operation                | Status         | Notes             | AWS Docs                                                                                          |
| ------------------------ | -------------- | ----------------- | ------------------------------------------------------------------------------------------------- |
| `AddTagsToResource`      | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_AddTagsToResource.html)      |
| `ListTagsForResource`    | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_ListTagsForResource.html)    |
| `RemoveTagsFromResource` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_RemoveTagsFromResource.html) |

<!-- END overcast:capabilities -->
