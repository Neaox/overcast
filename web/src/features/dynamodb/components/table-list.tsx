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
        emptyAction={
          <div className="flex flex-col items-center gap-4">
            <CreateAction onClick={() => setShowCreate(true)}>Create table</CreateAction>
            {/* Same arrangement as the CloudFormation stack list: `ResourceTable`
                owns its empty state, so the cross-region notice rides in the
                action slot, with its sibling margins dropped and the wrapper
                collapsed when the notice has nothing to say. */}
            <div className="empty:hidden [&>div]:mt-0 [&>div]:mb-0">
              <RegionElsewhereNotice kind="dynamodb-tables" noun="tables" />
            </div>
          </div>
        }
        errorTitle="Failed to load tables"
        sort={sort}
        onSortChange={onSortChange}
        // Four columns, all of them the reason someone opens this page.
        columnToggle={false}
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
            // A sortable header is a full-width flex button inside the `<th>`,
            // so `text-right` alone leaves the label hanging on the left of a
            // right-aligned column; the child selector aligns the button too.
            headerClassName: "text-right [&>button]:justify-end",
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
          getId: (table) => table.tableName,
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
