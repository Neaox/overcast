---
title: "Glue operations"
description: "Every Glue operation Overcast declares — 11 of 11 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - glue
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# Glue operations

All 11 listed operations are implemented. Back to [Glue](../glue.md).

## Summary

| Category  | ✅ Supported |
| --------- | ------------ |
| Databases | 4            |
| Tables    | 4            |
| Tags      | 3            |

---

## Endpoints

### Databases

| Operation        | Status       | Notes                             | AWS Docs                                                                            |
| ---------------- | ------------ | --------------------------------- | ----------------------------------------------------------------------------------- |
| `CreateDatabase` | ✅ Supported | Creates a database in the catalog | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-CreateDatabase.html) |
| `GetDatabase`    | ✅ Supported | Returns database details          | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-GetDatabase.html)    |
| `GetDatabases`   | ✅ Supported | Lists all databases               | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-GetDatabases.html)   |
| `DeleteDatabase` | ✅ Supported | Deletes a database                | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-DeleteDatabase.html) |

### Tables

| Operation     | Status       | Notes                         | AWS Docs                                                                         |
| ------------- | ------------ | ----------------------------- | -------------------------------------------------------------------------------- |
| `CreateTable` | ✅ Supported | Creates a table in a database | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-CreateTable.html) |
| `GetTable`    | ✅ Supported | Returns table details         | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-GetTable.html)    |
| `GetTables`   | ✅ Supported | Lists tables in a database    | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-GetTables.html)   |
| `DeleteTable` | ✅ Supported | Deletes a table               | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-DeleteTable.html) |

### Tags

| Operation       | Status       | Notes                                           | AWS Docs                                                                           |
| --------------- | ------------ | ----------------------------------------------- | ---------------------------------------------------------------------------------- |
| `TagResource`   | ✅ Supported | Adds or overwrites tags on databases and tables | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-TagResource.html)   |
| `UntagResource` | ✅ Supported | Removes tags by key from databases and tables   | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-UntagResource.html) |
| `GetTags`       | ✅ Supported | Returns tags for databases and tables           | [docs](https://docs.aws.amazon.com/glue/latest/dg/aws-glue-api-GetTags.html)       |

<!-- END overcast:capabilities -->
