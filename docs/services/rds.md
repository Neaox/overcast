# RDS — Relational Database Service

> AWS docs: https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/Welcome.html

RDS uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2014-10-31`.

When Docker is available, `CreateDBInstance` starts a real database container
(mysql, postgres, mariadb, aurora-mysql, aurora-postgresql) with automatic port
allocation from `RDS_PORT_BASE` (default 33060). When Docker is unavailable,
operations are metadata-only.

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
