import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Database, Eye } from "lucide-react"
import {
  dynamoTablesQueryOptions,
  dynamoKeys,
  deleteTableMutationOptions,
} from "@/features/dynamodb/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { RegionElsewhereNotice } from "@/features/preflight/components/region-elsewhere-notice"
import type { DynamoTable } from "@/types"
import { CreateTableDialog } from "./create-table-dialog"

interface TableListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function TableList({ sort, onSortChange }: TableListProps = {}) {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DynamoTable>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: tables = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(dynamoTablesQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteTableMutationOptions(),
    invalidateKeys: [dynamoKeys.tables()],
    successTitle: "Table deleted",
    successDescription: (name) => name,
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const totalItems = tables.reduce((n, t) => n + t.itemCount, 0)

  return (
    <ResourceListPage
      title="DynamoDB Tables"
      count={tables.length}
      meta={tables.length > 0 ? `${totalItems.toLocaleString()} items` : undefined}
      actions={
        <>
          <ServiceDocsButton
            service="dynamodb"
            label="DynamoDB"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RawStateLink service="dynamodb" />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create table</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: tables, isLoading, error }}
        noun="tables"
        emptyIcon={Database}
        emptyTitle="No tables yet"
        emptyDescription="Create a table to start storing DynamoDB items."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create table</CreateAction>}
        emptyExtra={<RegionElsewhereNotice kind="dynamodb-tables" noun="tables" />}
        errorTitle="Failed to load tables"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(table) => table.tableName}
        onRowClick={(table) =>
          navigate({ to: "/dynamodb/$tableName", params: { tableName: table.tableName } })
        }
        columns={[
          {
            id: "name",
            header: "Table name",
            sortValue: (table) => table.tableName,
            cell: (table) => <ResourceName icon={Database} name={table.tableName} />,
          },
          {
            header: "Status",
            cell: (table) => (
              <Badge
                variant={table.tableStatus === "ACTIVE" ? "success" : "default"}
                className="text-xs"
              >
                {table.tableStatus}
              </Badge>
            ),
          },
          {
            header: "Key schema",
            cellClassName: "text-fg-muted",
            cell: (table) => {
              const hashKey = table.keySchema.find((k) => k.keyType === "HASH")
              const sortKey = table.keySchema.find((k) => k.keyType === "RANGE")
              return (
                <>
                  {hashKey?.attributeName ?? "—"}
                  {sortKey && <span className="text-fg-subtle"> / {sortKey.attributeName}</span>}
                </>
              )
            },
          },
          {
            id: "items",
            header: "Items",
            headerClassName: "text-right",
            cellClassName: "text-right tabular-nums",
            sortValue: (table) => table.itemCount,
            cell: (table) => table.itemCount.toLocaleString(),
          },
        ]}
        rowActions={(table) => (
          <RowAction
            label={`View ${table.tableName}`}
            onClick={() =>
              navigate({ to: "/dynamodb/$tableName", params: { tableName: table.tableName } })
            }
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (table) => table.tableName,
          label: (table) => table.tableName,
          noun: "table",
          title: "Delete table",
          description: (table) =>
            `Are you sure you want to delete ${table.tableName}? This will permanently delete all items in the table.`,
        }}
      />

      <CreateTableDialog open={showCreate} onOpenChange={setShowCreate} />
    </ResourceListPage>
  )
}
