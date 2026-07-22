import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Database, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  dynamoTablesQueryOptions,
  dynamoKeys,
  deleteTableMutationOptions,
} from "@/features/dynamodb/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { EmptyState, PageHeader, QueryListState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { CreateTableDialog } from "./create-table-dialog"
import { cn } from "@/lib/utils"

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

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="DynamoDB Tables"
        description={`${tables.length} table${tables.length !== 1 ? "s" : ""}`}
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
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Table
            </Button>
          </>
        }
      />

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
                <Button size="sm" onClick={() => setShowCreate(true)}>
                  <Plus className="mr-1.5 h-3.5 w-3.5" />
                  Create Table
                </Button>
              }
            />
          }
          errorTitle="Failed to load tables"
        />
      ) : (
        <div className="rounded-md border border-border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Table name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Key schema</TableHead>
                <TableHead className="text-right">Items</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {tables.map((table) => {
                const hashKey = table.keySchema.find((k) => k.keyType === "HASH")
                const sortKey = table.keySchema.find((k) => k.keyType === "RANGE")
                return (
                  <TableRow
                    key={table.tableName}
                    className="cursor-pointer"
                    onClick={() =>
                      navigate({
                        to: "/dynamodb/$tableName",
                        params: { tableName: table.tableName },
                      })
                    }
                  >
                    <TableCell className="font-mono text-sm font-medium">
                      {table.tableName}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={table.tableStatus === "ACTIVE" ? "success" : "default"}
                        className="text-xs"
                      >
                        {table.tableStatus}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      <span className="font-mono">
                        {hashKey?.attributeName ?? "—"}
                        {sortKey && (
                          <span className="text-fg-subtle"> / {sortKey.attributeName}</span>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="text-right text-sm tabular-nums">
                      {table.itemCount.toLocaleString()}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-7 w-7 p-0 text-fg-muted hover:text-danger"
                        onClick={(e) => {
                          e.stopPropagation()
                          setDeleteTarget(table.tableName)
                        }}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}

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
    </div>
  )
}
