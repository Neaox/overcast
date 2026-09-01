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

RDS uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2014-10-31`.

### Instance status is what actually happened

An instance only reports `available` once Overcast has opened a TCP connection
to the engine, all credential initialization scripts have completed, and the
engine's own readiness client confirms that the final server is listening — on
creation as well as on start. This distinguishes the temporary initialization
server from the final database without authenticating as the master user. If the
container exits, cannot be started or created, or never reaches engine readiness
within five minutes, the instance goes to AWS's `failed` status
rather than being declared available anyway. An instance is never left in
`creating` or `starting` indefinitely.

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

They also survive another Overcast. Engine containers carry the identity of the
state store that created them, and the startup and shutdown sweeps only remove
containers bearing their own — so two Overcasts sharing a Docker daemon keep
separate databases rather than deleting each other's. This matters more for RDS
than elsewhere: an engine container has no volume and no bind mount, so the
database lives in the container's writable layer and goes with it. With the
default `memory` state backend the identity is minted afresh on every start, so
a crashed run's containers are no longer reclaimed at the next startup — an
orderly shutdown still cleans up after itself, and what is left behind clears
with `docker rm $(docker ps -aq --filter label=overcast.managed=true)`.

Nor does one Overcast manage another's engine. A DB instance identifier is a
name you choose, so both can hold one called `mydb`, and the container labels
that name — startup reconciliation and the Docker event stream now match on the
creating instance's identity as well, rather than stopping or restarting a
database the other one is serving. The container name is derived from the
identifier and Docker requires it to be unique per daemon, so the second
Overcast to start a DB instance of that name fails it with a reason saying so
instead of quietly sharing the first one's database. Give them different
identifiers, or a Docker daemon each.

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
[issue #614](https://github.com/overcast-sh/overcast/issues/614) tracks undoing. If
your code connects to the database, keep the default.

### Master account and initial database

The requested master account is the database administrator your application
connects as. On MySQL, MariaDB, and Aurora MySQL, it can create databases and
users and grant privileges across the instance. On PostgreSQL and Aurora
PostgreSQL it is a non-superuser with `CREATEDB`, `CREATEROLE`, and membership
in the emulated `rds_superuser` role, matching the boundary AWS exposes rather
than the stock image's unrestricted superuser.

The MySQL-family grants follow the selected engine version. RDS MySQL 8.0.36+
and Aurora MySQL 3 use the `rds_superuser_role` model; MySQL 8.4 and Aurora
MySQL 8.4 use their revised dynamic privileges and `caching_sha2_password`;
Aurora MySQL 3 adds `SHOW_ROUTINE` from 3.04 and the documented `FLUSH_*`
privileges from 3.09. Older MySQL, Aurora MySQL 2, and MariaDB receive the
documented direct instance-wide grants, including MariaDB 11.4's
`SHOW CREATE ROUTINE`.

The container's maintenance account is separate. Overcast uses it during
initialization and password recovery, but its generated credential is never
returned by the RDS API. MySQL-family maintenance access is local-only;
PostgreSQL exposes the AWS-shaped `rdsadmin` role in its catalog. Neither is an
alternative application credential.

`DBName` follows the engine's AWS behavior. MySQL and MariaDB create no
application database when it is omitted. PostgreSQL always has `postgres`; an
explicit `DBName` creates an additional database owned by the master account.
An Aurora member inherits `DatabaseName` from its cluster.

This emulates the privileges applications use to bootstrap databases and users,
not every managed-engine guardrail. The stock engines don't provide AWS's full
catalog of protected internal accounts and `rds_*` procedures. PostgreSQL
extension availability follows the backing image rather than the RDS extension
allowlist, and reserved-word validation covers the engine system schemas and
common SQL keywords rather than every version-specific reserved word. Code that
depends on those administrative edges still needs testing against AWS.

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

The change also keeps the container's maintenance credential synchronized so a
later API reset remains possible even if the master changed its own password
through SQL. Lifecycle readiness never uses the stored master password, so that
SQL-side change also survives a stop/start cycle. `DescribeDBInstances` never
returns either password, on AWS or here.

Passwords are held to the engine's RDS constraints on create and modify. MySQL,
MariaDB, and Aurora MySQL accept 8–41 characters; RDS PostgreSQL accepts 8–128;
Aurora PostgreSQL accepts 8–99. All accept printable ASCII except `/`, `"`, `@`,
and space. A single quote is valid and is escaped before it reaches the engine.

That includes passwords Overcast itself can generate. `GetRandomPassword`'s
default punctuation set contains characters RDS forbids — as AWS's does, which
is why CDK's `Credentials.fromGeneratedSecret` excludes them by default. If a
`{{resolve:secretsmanager:…}}` password is refused, set `ExcludeCharacters` on
the generated secret, as you would for AWS.

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
| Without `DBSubnetGroupName` | `true` for RDS, `false` for Aurora | RDS instances use the default VPC; AWS documents a private default for Aurora instances |
| With `DBSubnetGroupName` | `false` | A named subnet group is a chosen placement, and AWS treats a subnet group's instances as private unless their subnets are public |

AWS writes the non-Aurora rule in terms of internet gateways: public if the VPC
the instance landed in has one. Overcast's default VPC has one, so an RDS
instance in the first row is public; Aurora keeps AWS's explicit private
default. A named subnet group's gateway state cannot currently be resolved from
RDS, so it takes the private outcome. Send `PubliclyAccessible=true` to override
it; that is what the field is for.

An instance created before Overcast had the field reports the value its create
would have been given, so upgrading does not change what any existing database
says about itself.

`PubliclyAccessible` is an instance-level field on AWS, and so it is here: an
Aurora cluster does not carry one, and `CreateDBCluster` does not accept one.
Set it on the cluster's instances.

**This field decides reachability, not just metadata.** An instance in a subnet
group lands on that VPC's network and nothing else, so a Lambda or ECS task
outside the VPC cannot reach it — as on AWS. `PubliclyAccessible=true` also
keeps the instance on the default plane, which is the way out. If a connection
that used to work has started being refused, that is what changed; see
[Networking § Lambda, ECS and VPCs](../networking.md).

### What an Aurora member instance inherits

On AWS, an Aurora cluster owns the placement, credentials, engine version,
port, and initial database. `CreateDBInstance` with a `DBClusterIdentifier` is
the member's create call. The shared request shape still contains some of these
fields, but AWS documents them as not applying to Aurora instances.

Overcast follows that. A `CreateDBInstance` naming a `DBClusterIdentifier`:

| Field | Effective member value |
| --- | --- |
| `DBSubnetGroupName` | The cluster's, and with it the cluster's VPC |
| `MasterUsername` | The cluster's |
| `MasterUserPassword` | The cluster's |
| `EngineVersion` | The cluster's |
| `Port` | The cluster's |
| `DBName` | The cluster's `DatabaseName` |

Conflicting instance-level values are ignored for these cluster-owned settings,
so they cannot silently change which engine or credentials the endpoint serves.
The instance engine itself must match the cluster engine.

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
| `BackupRetentionPeriod` | **Validated**, then recorded only: held to AWS's documented 1–35 and defaulting to 1, but Overcast takes no backups |
| `PreferredBackupWindow`, `PreferredMaintenanceWindow` | Recorded only — Overcast takes no backups and runs no maintenance |
| `DBClusterParameterGroupName` | Recorded only — engine parameters are not applied to the container, and the name is **not** checked against an existing group, because Overcast implements no cluster parameter group operations |
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

### Instance logs — `GET /_overcast/rds/instances/{id}/logs`

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

### Connecting to a cluster

`Endpoint` and `ReaderEndpoint` on an Aurora cluster follow the same table as an
instance endpoint, and for the same reason: a cluster has no container of its
own, so both point at the writer member's engine. `DescribeDBClusters` therefore
answers with that instance's address and port, per caller, and both names are
registered on the writer's container — so the value CDK gives you as
`cluster.clusterEndpoint.hostname` resolves from inside a task exactly as the
instance endpoint does.

```
{dbClusterIdentifier}.cluster.{region}.rds.{base}      # writer
{dbClusterIdentifier}.cluster-ro.{region}.rds.{base}   # reader
```

These drop AWS's account-specific hash, as every Overcast endpoint name does:
`{cluster}.cluster-{hash}.…` and `{cluster}.cluster-ro-{hash}.…` reduce to
exactly the two above.

One deliberate difference from AWS: **the reader endpoint serves the writer**,
always. On AWS it load-balances across the Aurora Replicas and falls back to the
writer only when there are none. Overcast gives every member its own engine
container with its own storage — there is no shared Aurora volume — so a reader
endpoint spread across the replicas would answer from an empty database. Reads
are not distributed here; they do return what was written. One consequence worth
knowing: replica lag cannot be reproduced locally, so a read-after-write race
against a reader endpoint will pass here and can still fail on AWS.

> **Changed in 0.0.1-alpha.37.** The writer endpoint was
> `{cluster}.cluster-rw.…`, a label AWS has never used. Re-read the name from
> `DescribeDBClusters` rather than reconstructing it — anything holding the old
> spelling will stop resolving once the cluster is recreated.
>
> Names are attached to the engine container when Overcast creates it, and
> Docker treats a second connect for an already-attached container as a no-op.
> So a container that predates the upgrade and is still running keeps the name
> set it was created with: cluster endpoints begin resolving once that
> container is recreated, not the moment Overcast restarts. A cluster whose
> container *is* recreated also keeps answering to whatever its stored record
> advertises, which is what carries a pre-alpha.37 `cluster-rw` name forward.

Deleting a member removes it from `DBClusterMembers`, and deleting the **writer**
promotes one of the survivors — AWS treats losing the primary as a failover, and
so does Overcast. The replacement is chosen by `PromotionTier` (0 is the highest
priority, 15 the lowest, 1 the default), with ties going to the oldest surviving
member; AWS breaks the same tie on instance size and then arbitrarily, which
Overcast has no equivalent of because every member runs the same container
whatever its `DBInstanceClass`.

Because both cluster endpoint names live on the writer's container, a promotion
has to move them, and that means detaching and re-attaching the new writer to its
Docker networks. **Connections held open to that container over those networks
are dropped** when it happens — the same interruption a real failover causes.
Overcast has no `FailoverDBCluster`, so deleting the writer is the only way to
trigger one.

Deleting the *last* member leaves a cluster with no writer. That is a legitimate
state — CloudFormation deletes a cluster's instances before the cluster itself —
and the endpoints keep their names and the cluster's own port rather than
pointing anywhere.

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

## Operations

24 of 34 listed operations are implemented.
Per-operation status, notes and AWS API links: [RDS operations](rds/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
