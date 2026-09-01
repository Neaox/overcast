---
title: "Glue — AWS Glue Data Catalog"
description: "The Glue Data Catalog — databases, tables and their tags, stored as metadata. ETL jobs, crawlers, triggers, workflows and partitions are not emulated."
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
aws glue create-table --database-name analytics --table-input '{
  "Name": "events",
  "StorageDescriptor": {
    "Columns": [{"Name": "id", "Type": "string"}],
    "Location": "s3://data/events/"
  }
}'
aws glue get-tables --database-name analytics
```

## What works

| Area | Behaviour |
| --- | --- |
| Databases | `CreateDatabase`, `GetDatabase`, `GetDatabases`, `DeleteDatabase` |
| Tables | `CreateTable`, `GetTable`, `GetTables`, `DeleteTable`, scoped to a database |
| Table input | The whole `TableInput` — columns, partition keys, `StorageDescriptor`, `Parameters` — is stored and returned unchanged |
| Catalog id | Defaults to the account id when the request omits `CatalogId` |
| Tags | `TagResource`, `UntagResource` and `GetTags` on database and table ARNs |

## Differences from AWS

| Difference | Detail |
| --- | --- |
| Data Catalog only | Jobs, crawlers, triggers, workflows, connections, the schema registry and Data Quality are not implemented |
| No partitions API | Partition keys are stored on the table, but there are no partition rows and no `GetPartitions` |
| No update operations | There is no `UpdateDatabase` or `UpdateTable`; re-create through `CreateTable` to change a definition |
| Schemas are not validated | Column types are stored as strings; nothing checks them against the data at `Location` |
| One catalog | `CatalogId` is echoed, never used to separate catalogs |

## Gotchas

> [!NOTE]
> [Athena](athena.md) does not read this catalog. It records queries and
> returns empty result sets, so a table defined here changes nothing about
> what a query answers.

<!-- BEGIN overcast:capabilities -->

## Operations

All 11 listed operations are implemented.
Per-operation status, notes and AWS API links: [Glue operations](glue/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Athena](athena.md)
- [AWS API reference](https://docs.aws.amazon.com/glue/latest/webapi/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
