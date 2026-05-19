# Athena — Amazon Athena

> AWS docs: https://docs.aws.amazon.com/athena/latest/APIReference/

Amazon Athena uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`AmazonAthena.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AmazonAthena.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Queries immediately succeed with status `SUCCEEDED` and return empty result sets — no actual query execution is performed.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | ✅ Supported |
| ---------- | ------------ |
| Queries    | 4            |
| WorkGroups | 4            |

---

## Endpoints

### Queries

| Operation             | Status       | Notes                                    | AWS Docs                                                                                    |
| --------------------- | ------------ | ---------------------------------------- | ------------------------------------------------------------------------------------------- |
| `StartQueryExecution` | ✅ Supported | Starts a query; immediately succeeds     | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_StartQueryExecution.html) |
| `GetQueryExecution`   | ✅ Supported | Returns query execution details          | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_GetQueryExecution.html)   |
| `GetQueryResults`     | ✅ Supported | Returns query results (empty result set) | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_GetQueryResults.html)     |
| `ListQueryExecutions` | ✅ Supported | Lists all query execution IDs            | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_ListQueryExecutions.html) |

### WorkGroups

| Operation         | Status       | Notes                     | AWS Docs                                                                                |
| ----------------- | ------------ | ------------------------- | --------------------------------------------------------------------------------------- |
| `CreateWorkGroup` | ✅ Supported | Creates a workgroup       | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_CreateWorkGroup.html) |
| `GetWorkGroup`    | ✅ Supported | Returns workgroup details | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_GetWorkGroup.html)    |
| `ListWorkGroups`  | ✅ Supported | Lists all workgroups      | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_ListWorkGroups.html)  |
| `DeleteWorkGroup` | ✅ Supported | Deletes a workgroup       | [docs](https://docs.aws.amazon.com/athena/latest/APIReference/API_DeleteWorkGroup.html) |

<!-- END overcast:capabilities -->
