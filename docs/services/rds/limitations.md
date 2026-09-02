---
title: "RDS limitations"
description: "Which RDS engines Overcast runs and which it cannot, what an Aurora member inherits from its cluster, which cluster settings are enforced, and where the master account's privileges stop."
section: "Service Reference"
tags:
  - database
  - docs
  - limitations
  - rds
  - services
---

# RDS limitations

The full divergence list behind [RDS](../rds.md). Everything here applies to
`live` mode, the default; `mock` mode adds only that no engine exists at all.

## Engines

| Engine | AWS value | Default version | Images |
| --- | --- | --- | --- |
| PostgreSQL | `postgres` | 16.1 | `postgres:16` (16.1), `postgres:15` (15.5), `postgres:14` (14.11) |
| MySQL | `mysql` | 8.0 | `mysql:8.0`, `mysql:8.4`, `mysql:5.7` |
| MariaDB | `mariadb` | 11.4 | `mariadb:11` (11.4), `mariadb:10.11` |
| Aurora MySQL | `aurora-mysql` | 3.04 | `mysql:8.0` (3.04), `mysql:8.4` (4.0), `mysql:5.7` (2.11) |
| Aurora PostgreSQL | `aurora-postgresql` | 15.4 | `postgres:15` (15.4), `postgres:14` (14.11) |

Any `EngineVersion` is accepted. A version with no image of its own is served by
the nearest one in its family — `8.0.39` runs `mysql:8.0`, `16.3` runs
`postgres:16` — and the substitution is logged. Real stacks send precise
versions (CDK's `MysqlEngineVersion.VER_8_0_39`), and refusing them used to
leave the instance `available` with no database behind it.

Aurora uses its open-source counterpart's image because both speak the same wire
protocol. The Aurora resource model — `CreateDBCluster`, then
`CreateDBInstance` with a `DBClusterIdentifier` — is fully supported, and a
container starts per member instance.

### Engines that are not emulated

| Engine | Why |
| --- | --- |
| `sqlserver-*` | Not yet implemented. A free image exists (`mcr.microsoft.com/mssql/server`), so this is planned |
| `db2-ae`, `db2-se` | Not yet implemented. A community image exists (`icr.io/db2_community/db2`), but Db2 is rare in local development |
| `oracle-*`, `custom-oracle-*` | Not feasible. Oracle's images need a commercial licence and cannot be redistributed |

## What an Aurora member inherits

A `CreateDBInstance` naming a `DBClusterIdentifier` takes these from the cluster,
and conflicting instance-level values are ignored so they cannot silently change
which engine or credentials the endpoint serves. The instance engine must match
the cluster engine.

| Field | Effective member value |
| --- | --- |
| `DBSubnetGroupName` | The cluster's, and with it the cluster's VPC |
| `MasterUsername`, `MasterUserPassword` | The cluster's |
| `EngineVersion`, `Port` | The cluster's |
| `DBName` | The cluster's `DatabaseName` |

This is what makes the CDK shape work: `rds.DatabaseCluster` synthesises an
`AWS::RDS::DBInstance` with a `DBClusterIdentifier` and nothing else, because on
AWS the cluster supplies the rest.

An inherited subnet group counts as the instance's own for the
`PubliclyAccessible` defaults below, so a member of a cluster in a subnet group
is private by default. Set `PubliclyAccessible=true` on the **member**, not the
cluster — AWS has no cluster-level field, and neither does Overcast.

## What a cluster records and what it enforces

`ModifyDBCluster` accepts the settings CloudFormation sends and
`DescribeDBClusters` reports them back. What sits behind each one differs.

| Setting | Behaviour |
| --- | --- |
| `MasterUserPassword` | Applied to every member's engine |
| `EngineVersion`, `Port` | Recorded and reported |
| `DeletionProtection` | **Enforced** — `DeleteDBCluster` refuses a protected cluster, and a stack delete fails rather than removing it |
| `BackupRetentionPeriod` | **Validated** to AWS's 1–35, defaulting to 1, then recorded only — Overcast takes no backups |
| `PreferredBackupWindow`, `PreferredMaintenanceWindow` | Recorded only |
| `DBClusterParameterGroupName` | Recorded only, and not checked against an existing group — Overcast implements no cluster parameter group operations |
| `VpcSecurityGroupIds` | Recorded only — security groups are not enforced against a database |
| `EnableCloudwatchLogsExports` | Recorded only — no engine log is shipped to CloudWatch Logs |

On the CloudFormation side, `Engine`, `MasterUsername`, `DatabaseName` and
`DBSubnetGroupName` carry "Update requires: Replacement" on AWS and force
replacement here too.

## Reachability defaults

`PubliclyAccessible` says whether a database is reachable from outside the VPC it
was placed in. Omit it and it defaults the way AWS defaults it:

| Created | Default | Why |
| --- | --- | --- |
| Without `DBSubnetGroupName` | `true` for RDS, `false` for Aurora | RDS instances use the default VPC, which has an internet gateway; AWS documents a private default for Aurora |
| With `DBSubnetGroupName` | `false` | A named subnet group is a chosen placement, and its gateway state cannot currently be resolved from RDS |

Send `PubliclyAccessible=true` to override it. An instance created before
Overcast had the field reports the value its create would have been given.

## Master account boundaries

The requested master account is the administrator your application connects as.
On MySQL, MariaDB and Aurora MySQL it can create databases and users and grant
privileges across the instance; the grants follow the selected engine version
(the `rds_superuser_role` model on RDS MySQL 8.0.36+ and Aurora MySQL 3, the
revised dynamic privileges and `caching_sha2_password` on 8.4). On PostgreSQL
and Aurora PostgreSQL it is a non-superuser with `CREATEDB`, `CREATEROLE` and
membership in the emulated `rds_superuser` role, matching the boundary AWS
exposes rather than the stock image's unrestricted superuser.

What this does **not** emulate: AWS's full catalog of protected internal
accounts and `rds_*` procedures. PostgreSQL extension availability follows the
backing image rather than the RDS extension allowlist, and reserved-word
validation covers the engine system schemas and common SQL keywords rather than
every version-specific reserved word. Code that depends on those administrative
edges still needs testing against AWS.

The container's maintenance account is separate: Overcast uses it during
initialisation and password recovery, its generated credential is never returned
by the API, and it is not an alternative application credential.

`DBName` follows the engine's AWS behaviour. MySQL and MariaDB create no
application database when it is omitted; PostgreSQL always has `postgres`, and
an explicit `DBName` creates an additional database owned by the master account.

## Password rules

| Engine | Length |
| --- | --- |
| MySQL, MariaDB, Aurora MySQL | 8–41 |
| RDS PostgreSQL | 8–128 |
| Aurora PostgreSQL | 8–99 |

All accept printable ASCII except `/`, `"`, `@` and space. A single quote is
valid and is escaped before it reaches the engine.

> [!IMPORTANT]
> `GetRandomPassword`'s default punctuation set contains characters RDS forbids,
> as AWS's does — which is why CDK's `Credentials.fromGeneratedSecret` excludes
> them by default. If a `{{resolve:secretsmanager:…}}` password is refused, set
> `ExcludeCharacters` on the generated secret, as you would for AWS.

## Changing a password on a live engine

`MasterUserPassword` is applied to the running database, not just the record, so
rotating one in a CloudFormation template takes effect and never replaces the
instance. A container reads `MYSQL_ROOT_PASSWORD` (or its equivalent) once, when
it initialises its data directory, so two rules follow:

- **The instance must be `available`.** A stopped or failed one is refused with
  `InvalidDBInstanceState` rather than accepted and remembered — there is no
  later moment at which a pending password could be applied.
- **An engine that refuses the change fails the whole call.** Neither the
  password nor anything else in the same request is recorded. Storing a password
  the database does not honour would report a rotation that leaves every
  connection refused.

With Docker unavailable the new password is simply stored, and a container built
for that instance later is seeded from it.

`ModifyDBCluster` rotates a cluster's password once per member through the same
code. If one member refuses, the call fails and names it; the members already
rotated keep the new password, because rolling them back would need the old one
to still work on engines that have stopped accepting it.

## Cluster endpoint names

```
{dbClusterIdentifier}.cluster.{region}.rds.{base}      # writer
{dbClusterIdentifier}.cluster-ro.{region}.rds.{base}   # reader
```

Both drop AWS's account-specific hash, as every Overcast endpoint name does.
The writer label was `cluster-rw` until 0.0.1-alpha.37 — a spelling AWS has
never used. Read the name from `DescribeDBClusters` rather than reconstructing
it: a container that predates the upgrade keeps the alias set it was created
with, so the current names begin resolving when that container is recreated.

Deleting the **writer** promotes a survivor, because AWS treats losing the
primary as a failover. The replacement is chosen by `PromotionTier` (0 is the
highest priority, 15 the lowest, 1 the default), ties going to the oldest
surviving member; AWS breaks the same tie on instance size, which has no
equivalent here because every member runs the same container whatever its
`DBInstanceClass`.

Deleting the last member leaves a cluster with no writer. That is a legitimate
state — CloudFormation deletes a cluster's instances before the cluster itself —
and the endpoints keep their names and the cluster's own port.
