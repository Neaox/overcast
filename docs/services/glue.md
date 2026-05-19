# Glue — AWS Glue Data Catalog

> AWS docs: https://docs.aws.amazon.com/glue/latest/webapi/

AWS Glue Data Catalog uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`AWSGlue.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AWSGlue.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Only the Data Catalog subset of Glue is emulated (databases and tables). ETL jobs, crawlers, and workflows are not supported.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category  | ✅ Supported |
| --------- | ------------ |
| Databases | 4            |
| Tables    | 4            |

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

<!-- END overcast:capabilities -->
