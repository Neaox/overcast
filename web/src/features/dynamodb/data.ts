/**
 * DynamoDB query/mutation definitions.
 *
 * Key factory:
 *   dynamoKeys.all()                              -> ["dynamodb"]
 *   dynamoKeys.tables()                         -> ["dynamodb", "tables"]
 *   dynamoKeys.tableList(baseUrl)               -> ["dynamodb", "tables", baseUrl]
 *   dynamoKeys.table(baseUrl, name)             -> ["dynamodb", "table", baseUrl, name]
 *   dynamoKeys.items()                          -> ["dynamodb", "items"]
 *   dynamoKeys.itemList(baseUrl, name)          -> ["dynamodb", "items", baseUrl, name]
 */

import { queryOptions, infiniteQueryOptions, mutationOptions } from "@tanstack/react-query"
import { dynamodb } from "@/services/api"
import type { DynamoItem } from "@/types"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const dynamoKeys = {
  all: () => [...endpointStore.getKeys(), "dynamodb"] as const,
  tables: () => [...dynamoKeys.all(), "tables"] as const,
  table: (name: string) => [...dynamoKeys.all(), "table", name] as const,
  items: () => [...dynamoKeys.all(), "items"] as const,
  itemList: (name: string) => [...dynamoKeys.items(), name] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function dynamoTablesQueryOptions() {
  return queryOptions({
    queryKey: dynamoKeys.tables(),
    queryFn: () => dynamodb.listTables(),
  })
}

export function dynamoTableQueryOptions(name: string) {
  return queryOptions({
    queryKey: dynamoKeys.table(name),
    queryFn: () => dynamodb.describeTable(name),
  })
}

export function dynamoItemsQueryOptions(
  name: string,
  opts: { limit?: number; indexName?: string } = {},
) {
  return infiniteQueryOptions({
    queryKey: [...dynamoKeys.itemList(name), opts],
    queryFn: ({ pageParam }) => dynamodb.scanItems(name, { ...opts, token: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextToken ?? undefined,
  })
}

export function dynamoQueryItemsOptions(
  tableName: string,
  params: {
    indexName?: string
    keyConditionExpression: string
    expressionAttributeValues: DynamoItem
    expressionAttributeNames?: Record<string, string>
    filterExpression?: string
    limit?: number
    scanIndexForward?: boolean
  },
) {
  return infiniteQueryOptions({
    queryKey: [...dynamoKeys.all(), "query", tableName, params],
    queryFn: ({ pageParam }) => dynamodb.queryItems(tableName, { ...params, token: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextToken ?? undefined,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createTableMutationOptions() {
  return mutationOptions({
    mutationKey: [...dynamoKeys.tables(), "create"] as const,
    mutationFn: (opts: {
      tableName: string
      hashKeyName: string
      hashKeyType: "S" | "N" | "B"
      sortKeyName?: string
      sortKeyType?: "S" | "N" | "B"
      billingMode?: "PAY_PER_REQUEST" | "PROVISIONED"
    }) => dynamodb.createTable(opts),
  })
}

export function deleteTableMutationOptions() {
  return mutationOptions({
    mutationKey: [...dynamoKeys.tables(), "delete"] as const,
    mutationFn: (name: string) => dynamodb.deleteTable(name),
  })
}

export function putItemMutationOptions(tableName: string) {
  return mutationOptions({
    mutationKey: [...dynamoKeys.all(), "items", tableName, "put"] as const,
    mutationFn: (item: DynamoItem) => dynamodb.putItem(tableName, item),
  })
}

export function deleteItemMutationOptions(tableName: string) {
  return mutationOptions({
    mutationKey: [...dynamoKeys.all(), "items", tableName, "delete"] as const,
    mutationFn: (key: DynamoItem) => dynamodb.deleteItem(tableName, key),
  })
}

export function updateItemMutationOptions(tableName: string) {
  return mutationOptions({
    mutationKey: [...dynamoKeys.all(), "items", tableName, "update"] as const,
    mutationFn: ({ key, item }: { key: DynamoItem; item: DynamoItem }) =>
      dynamodb.updateItem(tableName, key, item),
  })
}

export function updateStreamMutationOptions(tableName: string) {
  return mutationOptions({
    mutationKey: [...dynamoKeys.all(), "stream", tableName] as const,
    mutationFn: (opts: { streamEnabled: boolean; streamViewType?: string }) =>
      dynamodb.updateTableStream(tableName, opts),
  })
}

export function bulkDeleteItemsMutationOptions(tableName: string) {
  return mutationOptions({
    mutationKey: [...dynamoKeys.all(), "items", tableName, "bulk-delete"] as const,
    mutationFn: (keys: DynamoItem[]) =>
      Promise.all(keys.map((key) => dynamodb.deleteItem(tableName, key))),
  })
}
