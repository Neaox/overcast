import { dynamodb } from "@/services/api"
import type { DynamoTable } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<DynamoTable>({
  id: "dynamodb",
  // Must match dynamoKeys.tables() exactly — [baseUrl, region, "dynamodb",
  // "tables"] — or getQueryData never hits and every search refetches. The
  // region segment is load-bearing now that DynamoDB tables are region-scoped:
  // without it a cached list from one region could answer search while the
  // endpoint points at another.
  cacheKey: (ep) => [ep.baseUrl, ep.region, "dynamodb", "tables"] as const,
  fetchAll: () => dynamodb.listTables(),
  matchFields: (t) => [t.tableName, t.tableArn],
  toResult: (t) => ({
    id: `dynamodb:${t.tableName}`,
    label: t.tableName,
    sublabel: t.tableArn,
    service: "DynamoDB",
    serviceKey: "/dynamodb",
    type: "Table",
    href: `/dynamodb/${encodeURIComponent(t.tableName)}`,
  }),
})
