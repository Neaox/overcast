import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Database, Eye, Trash2 } from "lucide-react"
import {
  dynamoTablesQueryOptions,
  dynamoKeys,
  deleteTableMutationOptions,
} from "@/features/dynamodb/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { CreateTableDialog } from "./create-table-dialog"

export function TableList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
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
      <ResourceListCard>
        {isLoading || tables.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={tables.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Database className="h-10 w-10" />}
                title="No tables yet"
                description="Create a table to start storing DynamoDB items."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create table</CreateAction>
                }
              />
            }
            errorTitle="Failed to load tables"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Table name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Key schema</TableHead>
                <TableHead className="text-right">Items</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tables.map((table) => {
                const hashKey = table.keySchema.find((k) => k.keyType === "HASH")
                const sortKey = table.keySchema.find((k) => k.keyType === "RANGE")
                return (
                  <TableRow
                    key={table.tableName}
                    onClick={() =>
                      navigate({
                        to: "/dynamodb/$tableName",
                        params: { tableName: table.tableName },
                      })
                    }
                  >
                    <TableCell>
                      <ResourceName icon={Database} name={table.tableName} />
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={table.tableStatus === "ACTIVE" ? "success" : "default"}
                        className="text-xs"
                      >
                        {table.tableStatus}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      <span className="font-mono">
                        {hashKey?.attributeName ?? "—"}
                        {sortKey && (
                          <span className="text-fg-subtle"> / {sortKey.attributeName}</span>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {table.itemCount.toLocaleString()}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <RowActions>
                        <RowAction
                          label={`View ${table.tableName}`}
                          onClick={() =>
                            navigate({
                              to: "/dynamodb/$tableName",
                              params: { tableName: table.tableName },
                            })
                          }
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </RowAction>
                        <RowAction
                          label={`Delete ${table.tableName}`}
                          tone="danger"
                          onClick={() => setDeleteTarget(table.tableName)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </RowAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      <CreateTableDialog open={showCreate} onOpenChange={setShowCreate} />

      {/* Delete confirmation dialog */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={() => setDeleteTarget(undefined)}
        title="Delete table"
        description={`Are you sure you want to delete ${deleteTarget}? This will permanently delete all items in the table.`}
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </ResourceListPage>
  )
}
