import { useState } from "react"
import { useQuery, useMutation } from "@tanstack/react-query"
import { Plus, Trash2, RefreshCw } from "lucide-react"
import {
  apiKeysQueryOptions,
  apigwKeys,
  createApiKeyMutationOptions,
  deleteApiKeyMutationOptions,
} from "@/features/apigateway/data"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, QueryListState, Spinner } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ApiKeyValue } from "@/features/apigateway/components/api-key-value"
import type { ApiKey } from "@/features/apigateway/data"

export function ApiKeysPage() {
  const { toast } = useToast()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ApiKey>()

  // Form state
  const [newKeyName, setNewKeyName] = useState("")
  const [newKeyEnabled, setNewKeyEnabled] = useState(true)

  const {
    data: apiKeys = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(apiKeysQueryOptions())

  const createMut = useMutation({
    ...createApiKeyMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.apiKeys() })
      setShowCreate(false)
      setNewKeyName("")
      setNewKeyEnabled(true)
      toast({ title: "API key created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteApiKeyMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.apiKeys() })
      setDeleteTarget(undefined)
      toast({ title: "API key deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  return (
    <div className="flex w-full max-w-5xl flex-col gap-4">
      <PageHeader
        title="API Keys"
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => void refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create API Key
            </Button>
          </>
        }
      />

      {isLoading || apiKeys.length === 0 ? (
        <QueryListState
          isLoading={isLoading}
          isEmpty={apiKeys.length === 0}
          error={error}
          emptyTitle="No API keys yet"
          errorTitle="Failed to load API keys"
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>Value</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead>Created</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {apiKeys.map((key) => (
              <TableRow key={key.id}>
                <TableCell className="font-medium">{key.name}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">{key.id}</TableCell>
                <TableCell>
                  <ApiKeyValue value={key.value} />
                </TableCell>
                <TableCell>
                  <Badge variant={key.enabled ? "success" : "default"}>
                    {key.enabled ? "Enabled" : "Disabled"}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm text-fg-muted">
                  {formatDate(new Date(key.createdDate))}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(key)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* Delete confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete API Key"
        description={
          <>
            Delete API key <span className="font-mono font-semibold">{deleteTarget?.name}</span>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />

      {/* Create dialog */}
      <Dialog
        open={showCreate}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreate(false)
            setNewKeyName("")
            setNewKeyEnabled(true)
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create API Key</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createMut.mutate({ name: newKeyName, enabled: newKeyEnabled })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="key-name">
                Name <span className="text-danger">*</span>
              </label>
              <input
                id="key-name"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newKeyName}
                onChange={(e) => setNewKeyName(e.target.value)}
                placeholder="my-api-key"
                autoFocus
                required
              />
            </div>
            <div className="flex items-center gap-3">
              <input
                id="key-enabled"
                type="checkbox"
                className="h-4 w-4 rounded border"
                checked={newKeyEnabled}
                onChange={(e) => setNewKeyEnabled(e.target.checked)}
              />
              <label className="text-sm font-medium" htmlFor="key-enabled">
                Enabled
              </label>
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!newKeyName || createMut.isPending}>
                {createMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
