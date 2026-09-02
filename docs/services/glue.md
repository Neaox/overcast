---
title: "Glue — AWS Glue Data Catalog"
description: "Quick start, the Data Catalog operations that work, the table and database fields dropped on write, and everything outside the catalog that is not emulated."
section: "Service Reference"
tags:
  - aws
  - catalog
  - data
  - docs
  - glue
  - services
---

# Glue — AWS Glue Data Catalog

Only the Data Catalog is emulated — databases and tables held as metadata. ETL
jobs, crawlers and workflows are not.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws glue create-database --database-input Name=analytics
aws glue create-table --database-name analytics \
  --table-input 'Name=events,TableType=EXTERNAL_TABLE'
aws glue get-tables --database-name analytics
```

## What works

| Area | Behaviour |
| --- | --- |
| Databases | `CreateDatabase`, `GetDatabase`, `GetDatabases`, `DeleteDatabase` |
| Tables | `CreateTable`, `GetTable`, `GetTables`, `DeleteTable`, scoped to a database |
| Stored fields | A database keeps `Name`, `Description` and `CatalogId`; a table keeps those plus `DatabaseName` and `TableType` |
| Catalog id | Defaults to the account id when the request omits `CatalogId` |
| Tags | `TagResource`, `UntagResource` and `GetTags` on database and table ARNs |

## Differences from AWS

| Area                          | Overcast                                                                                                                                                                    |
| ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Data Catalog only             | Jobs, crawlers, triggers, workflows, connections, the schema registry and Data Quality are not implemented                                                                  |
| Schemas are dropped           | A table's `StorageDescriptor` — its columns, serde and `Location` — and its `PartitionKeys` and `Parameters` are accepted and discarded, so `GetTable` returns none of them |
| Database input is dropped too | `LocationUri`, `Parameters` and `TargetDatabase` are not stored                                                                                                             |
| No partitions API             | There are no partition rows and no `GetPartitions`                                                                                                                          |
| No update operations          | There is no `UpdateDatabase` or `UpdateTable`; re-create through `CreateTable` to change a definition                                                                       |
| One catalog                   | `CatalogId` is echoed, never used to separate catalogs                                                                                                                      |

## Gotchas

> [!WARNING]
> A table is a name, not a schema. `CreateTable` accepts a full `TableInput`
> and stores only `Name`, `TableType`, `Description` and `CatalogId` — code
> that reads columns or an S3 `Location` back out of `GetTable` will find
> neither.

> [!NOTE]
> [Athena](./athena.md) does not read this catalog. It records queries and
> returns empty result sets, so a table defined here changes nothing about
> what a query answers.

<!-- BEGIN overcast:capabilities -->

## Operations

All 11 listed operations are implemented.
Per-operation status, notes and AWS API links: [Glue operations](glue/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Athena](./athena.md)
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/glue/latest/webapi/)
