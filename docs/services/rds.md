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
to the engine — on creation as well as on start. If the container exits, cannot
be started or created, or never accepts a connection within five minutes, the
instance goes to AWS's `failed` status rather than being declared available
anyway — a database you cannot connect to is not available, and discovering
that by trying to connect is worse than being told. An instance is never left
in `creating` or `starting` indefinitely.

That means `CreateDBInstance` leaves the instance in `creating` for as long as
the engine takes to come up, which for a first boot is roughly half a minute:
the data directory has to be initialised before the server accepts anything.
Poll `DescribeDBInstances` until it reports `available`, exactly as you would
against AWS, and the connection will succeed on the first attempt.

### CloudFormation and CDK wait for the database

`CreateDBInstance` is asynchronous, but CloudFormation is not: an
`AWS::RDS::DBInstance` resource is not `CREATE_COMPLETE` until the instance
reports `available`, and an `AWS::RDS::DBCluster` behaves the same way. So a
`cdk deploy` blocks for as long as the engine takes to come up, and anything
downstream of the database — a `DependsOn`, a `Fn::GetAtt` on
`Endpoint.Address`, a task that runs migrations — waits behind it. This is what
AWS does, and the point of it: a deploy that returns success is a deploy whose
database accepts connections.

An instance that goes to `failed` fails the resource with the reason RDS
recorded against it, and the stack rolls back — deleting the database it
created rather than leaving it behind. Updates wait too: `ModifyDBInstance`
puts the instance into `modifying`, and the resource is not `UPDATE_COMPLETE`
until it settles.

If you want the control plane without the wait, `OVERCAST_RDS_MODE=mock` reaches
`available` immediately — see below.

Starting an instance whose container Docker no longer has (a `docker prune`, a
container removed by hand) rebuilds it rather than reporting a start that
started nothing. Containers belonging to a stopped instance survive an Overcast
restart, so a stopped database can always be started again.

When Docker is available, `CreateDBInstance` starts a real database container
(mysql, postgres, mariadb, aurora-mysql, aurora-postgresql) with automatic port
allocation from `RDS_PORT_BASE` (default 33060). When Docker is unavailable,
operations are metadata-only.

### Modes — `OVERCAST_RDS_MODE`

- `live` (default): a real engine container per DB instance, as described
  above. It asks nothing of a machine that cannot provide it — without a
  reachable Docker daemon it starts nothing and behaves exactly like `mock`.
- `mock` (opt out with `OVERCAST_RDS_MODE=mock`): metadata-only. Instances move
  through the status model on a timer and reach `available` in a moment, and
  RDS touches Docker for nothing.

The trade in `mock` is the endpoint: `DescribeDBInstances` still reports an
address and port, and nothing is listening on them. Reach for it when you are
testing the control plane and the tens of seconds a real first boot costs are
the thing in your way — Overcast's own compatibility suites run this way, which
[issue #614](https://github.com/Neaox/overcast/issues/614) tracks undoing. If
your code connects to the database, keep the default.

### Changing the master password — `ModifyDBInstance` and `ModifyDBCluster`

`MasterUserPassword` is applied to the database that is running, not just to
the record describing it. The engine's own password statement is run inside the
container — `ALTER USER` through `mysql`, `mariadb` or `psql`, authenticated
with the password the engine still has — so the old password stops working and
the new one starts, as on AWS. Rotating a password in a CloudFormation template
therefore takes effect, and never replaces the instance.

This is not the same mechanism as creation. A container reads
`MYSQL_ROOT_PASSWORD` (or `POSTGRES_PASSWORD`, or the MariaDB equivalent) once,
when it initialises its data directory, and never again: restarting it with a
different value changes nothing, and recreating it would apply the new password
by discarding the database. Two consequences follow, and both are deliberate:

- **The instance must be `available`.** A stopped or failed instance is
  refused with `InvalidDBInstanceState` rather than accepted and remembered.
  There is no later moment at which Overcast could apply a pending password —
  the container keeps the credentials it was created with — so a modification
  that claimed to have queued one would be a promise nothing keeps.
- **An engine that refuses the change fails the whole call.** The engine's own
  error comes back in the response, and neither the password nor anything else
  in the same `ModifyDBInstance` request is recorded. Storing a password the
  database does not honour would report a rotation that leaves every connection
  using the new password refused, which is worse than the change not happening.

With Docker unavailable the instance is metadata-only, and there the new
password is simply stored: nothing is serving connections to disagree with the
record, and a container built for that instance later is seeded from it.

For MySQL and MariaDB the change covers the container's `root` account as well
as the master user. `root` is how Overcast seeds a container, so leaving it on
the old password would make the record wrong about any container rebuilt later.
`DescribeDBInstances` never returns the password, on AWS or here.

Passwords are held to RDS's constraints, on create as well as on modify: 8–128
printable ASCII characters, and none of `/`, `"`, `@`, `'` or space. A password
RDS would refuse is refused here, so you find out locally instead of on deploy.

That includes passwords Overcast itself can generate. `GetRandomPassword`'s
default punctuation set contains four of the five forbidden characters — as
AWS's does, which is why CDK's `Credentials.fromGeneratedSecret` excludes them
by default. If a `{{resolve:secretsmanager:…}}` password is refused, the fix is
the same one real AWS needs: set `ExcludeCharacters` on the generated secret.

`ModifyDBCluster` rotates a cluster's password the same way, through the same
code, once per member. A `DBCluster` is a logical record with no container of
its own — the engines belong to its `DBClusterMembers` — so reaching them is
the whole of what a cluster-level rotation can mean, and a cluster with no
members yet is the metadata-only case above. If one member refuses, the call
fails and names it; the members already rotated keep the new password, because
rolling them back would need the old one to still work on engines that have
stopped accepting it.

### Reachability — `PubliclyAccessible`

`CreateDBInstance` accepts `PubliclyAccessible`, `ModifyDBInstance` changes it,
and `DescribeDBInstances` reports it. It is the field that says whether a
database is meant to be reachable from outside the VPC it was placed in — the
answer when something in a VPC still has to be dialable from your machine.

Omit it and it defaults the way AWS defaults it:

| Created | Default | Why |
| --- | --- | --- |
| Without `DBSubnetGroupName` | `true` | The instance lands in the region's default VPC, and Overcast seeds that VPC with an internet gateway, exactly as AWS does |
| With `DBSubnetGroupName` | `false` | A named subnet group is a chosen placement, and AWS treats a subnet group's instances as private unless their subnets are public |

AWS writes the rule in terms of internet gateways: public if the VPC the
instance landed in has one. The first row resolves to `true` here and can
resolve to nothing else. The second cannot be resolved from RDS at all —
nothing on that side can see whether a subnet group's VPC has a gateway — so it
takes AWS's other documented outcome, private. Send `PubliclyAccessible=true`
to override it; that is what the field is for.

An instance created before Overcast had the field reports the value its create
would have been given, so upgrading does not change what any existing database
says about itself.

`PubliclyAccessible` is an instance-level field on AWS, and so it is here: an
Aurora cluster does not carry one, and `CreateDBCluster` does not accept one.
Set it on the cluster's instances.

### What an Aurora member instance inherits

On AWS, an Aurora cluster owns the placement and the credentials, and its member
instances take them. `CreateDBInstance` with a `DBClusterIdentifier` is the
member's create call, and it does not carry either — RDS rejects master
credentials on a member, and the subnet group belongs to the cluster.

Overcast follows that. A `CreateDBInstance` naming a `DBClusterIdentifier`:

| Field | If the request omits it | If the request sets it |
| --- | --- | --- |
| `DBSubnetGroupName` | Takes the cluster's, and with it the cluster's VPC | The instance's own wins |
| `MasterUsername` | Takes the cluster's | The instance's own wins |
| `MasterUserPassword` | Takes the cluster's | The instance's own wins |

This is what makes the CDK shape work. `rds.DatabaseCluster` synthesises
`AWS::RDS::DBInstance` with a `DBClusterIdentifier` and nothing else — no subnet
group, no credentials — because on AWS the cluster supplies both. Read only the
instance's own fields and the writer lands outside the VPC its application is
in, and is refused for a missing `MasterUsername` before it gets that far.

An inherited subnet group counts as the instance's own for the
`PubliclyAccessible` defaults under [Reachability](#reachability-publiclyaccessible),
so a member of a cluster in a subnet group is private by default.
Set `PubliclyAccessible=true` on the member — not the cluster — to reach it from
outside.

### What a cluster records and what it enforces

`ModifyDBCluster` accepts the settings CloudFormation sends, and
`DescribeDBClusters` reports them back. What sits behind each one differs, and
the difference matters if you are relying on it:

| Setting | Behaviour |
| --- | --- |
| `MasterUserPassword` | Applied to every member's engine — see above |
| `EngineVersion`, `Port` | Recorded and reported |
| `DeletionProtection` | **Enforced**: `DeleteDBCluster` refuses a protected cluster, and a stack delete fails rather than removing it |
| `BackupRetentionPeriod`, `PreferredBackupWindow`, `PreferredMaintenanceWindow` | Recorded only — Overcast takes no backups and runs no maintenance |
| `DBClusterParameterGroupName` | Recorded only — engine parameters are not applied to the container |
| `VpcSecurityGroupIds` | Recorded only — security groups are not enforced against a database |
| `EnableCloudwatchLogsExports` | Recorded only — no engine log is shipped to CloudWatch Logs |

"Recorded only" is still the difference between an update that lands and one
that vanishes: before this, every one of these was dropped between the wire and
the handler, so a stack update that changed any of them reported
`UPDATE_COMPLETE` having changed nothing at all.

On the CloudFormation side, `Engine`, `MasterUsername`, `DatabaseName` and
`DBSubnetGroupName` carry "Update requires: Replacement" on AWS and force
replacement here too, rather than being quietly applied in place.

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
[networking.md](../networking.md#data-plane-endpoints-rds-and-anything-else-that-is-a-container).

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
| General          | 3            |                |

---

## Endpoints

### DB instances

| Operation                         | Status         | Notes                                                                                                                                                                                                                                                                                                         | AWS Docs                                                                                                   |
| --------------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `CreateDBInstance`                | ✅ Supported   | Docker-backed when available; async creating→available; mysql/postgres/mariadb/aurora-mysql/aurora-postgresql; accepts `DBClusterIdentifier` for Aurora, and a member inherits the cluster's subnet group and credentials; `PubliclyAccessible` defaults to true without a DB subnet group and false with one | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBInstance.html)                |
| `DescribeDBInstances`             | ✅ Supported   | List all or filter by DBInstanceIdentifier                                                                                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBInstances.html)             |
| `DeleteDBInstance`                | ✅ Supported   | Sets status to "deleting"; stops+removes Docker container                                                                                                                                                                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBInstance.html)                |
| `StopDBInstance`                  | ✅ Supported   | Stops Docker container; available→stopping→stopped                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StopDBInstance.html)                  |
| `StartDBInstance`                 | ✅ Supported   | Starts Docker container; stopped→starting→available                                                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StartDBInstance.html)                 |
| `ModifyDBInstance`                | ✅ Supported   | Metadata updates (class, storage, engine version, multi-AZ, public accessibility); `MasterUserPassword` is applied to the running engine, requires an `available` instance, and is held to RDS's 8–128 character and forbidden-character rules                                                                | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_ModifyDBInstance.html)                |
| `RebootDBInstance`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_RebootDBInstance.html)                |
| `CreateDBSnapshot`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBSnapshot.html)                |
| `DeleteDBSnapshot`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBSnapshot.html)                |
| `DescribeDBSnapshots`             | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBSnapshots.html)             |
| `RestoreDBInstanceFromDBSnapshot` | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_RestoreDBInstanceFromDBSnapshot.html) |
| `DescribeDBLogFiles`              | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBLogFiles.html)              |
| `DownloadDBLogFilePortion`        | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DownloadDBLogFilePortion.html)        |

### Aurora clusters

| Operation                    | Status         | Notes                                                                                                                                                                                                       | AWS Docs                                                                                              |
| ---------------------------- | -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `CreateDBCluster`            | ✅ Supported   | aurora-mysql and aurora-postgresql only; logical cluster, Docker started on first instance                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBCluster.html)            |
| `DescribeDBClusters`         | ✅ Supported   | List all or filter by DBClusterIdentifier; returns cluster members                                                                                                                                          | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBClusters.html)         |
| `DeleteDBCluster`            | ✅ Supported   | Sets status to "deleting"; async removal; refuses a cluster with `DeletionProtection` enabled                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBCluster.html)            |
| `ModifyDBCluster`            | ✅ Supported   | `MasterUserPassword` applied to every member's engine; engine version, port and `DeletionProtection` applied; backup/maintenance windows, cluster parameter group, security groups and log exports recorded | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_ModifyDBCluster.html)            |
| `StartDBCluster`             | ✅ Supported   | stopped→starting→available                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StartDBCluster.html)             |
| `StopDBCluster`              | ✅ Supported   | available→stopping→stopped                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_StopDBCluster.html)              |
| `CreateDBClusterSnapshot`    | ❌ Unsupported | stub; returns 501                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBClusterSnapshot.html)    |
| `DeleteDBClusterSnapshot`    | ❌ Unsupported | stub; returns 501                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DeleteDBClusterSnapshot.html)    |
| `DescribeDBClusterSnapshots` | ❌ Unsupported | stub; returns 501                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBClusterSnapshots.html) |

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

| Operation                | Status       | Notes                                                              | AWS Docs                                                                                          |
| ------------------------ | ------------ | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------- |
| `AddTagsToResource`      | ✅ Supported | Tags stored per-ARN in `rds:tags` namespace; shared tag validation | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_AddTagsToResource.html)      |
| `ListTagsForResource`    | ✅ Supported | Returns tag list for any RDS resource ARN                          | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_ListTagsForResource.html)    |
| `RemoveTagsFromResource` | ✅ Supported | Removes specified tag keys from a resource                         | [docs](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_RemoveTagsFromResource.html) |

<!-- END overcast:capabilities -->
