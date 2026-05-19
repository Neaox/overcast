import { dynamodb } from "@/services/api"
import type { DynamoTable } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<DynamoTable>({
  id: "dynamodb",
  cacheKey: (ep) => ["dynamodb", "tables", ep.baseUrl],
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
