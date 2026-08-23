import { useState } from "react"
import { useQuery, useMutation } from "@tanstack/react-query"
import {
  apiKeysQueryOptions,
  apigwKeys,
  createApiKeyMutationOptions,
  deleteApiKeyMutationOptions,
} from "@/features/apigateway/data"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/primitives"
import { CreateAction, RefreshAction, ResourceListPage } from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { useToast } from "@/components/ui/toast"
import { formatDate } from "@/lib/format"
import { fieldLabel } from "@/lib/typography"
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
    <ResourceListPage
      title="API Keys"
      className="max-w-5xl"
      actions={
        <>
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create API Key</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: apiKeys, isLoading, error }}
        noun="API keys"
        emptyTitle="No API keys yet"
        errorTitle="Failed to load API keys"
        rowKey={(key) => key.id}
        columns={[
          {
            header: "Name",
            cellClassName: "font-medium",
            sortValue: (key) => key.name,
            cell: (key) => key.name,
          },
          { header: "ID", cellClassName: "text-fg-muted", cell: (key) => key.id },
          { header: "Value", cell: (key) => <ApiKeyValue value={key.value} /> },
          {
            header: "Enabled",
            cell: (key) => (
              <Badge variant={key.enabled ? "success" : "default"}>
                {key.enabled ? "Enabled" : "Disabled"}
              </Badge>
            ),
          },
          {
            header: "Created",
            cellClassName: "text-fg-muted",
            sortValue: (key) => new Date(key.createdDate),
            cell: (key) => formatDate(new Date(key.createdDate)),
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (key) => key.id,
          label: (key) => key.name,
          noun: "API key",
          title: "Delete API Key",
          description: (key) => (
            <>
              Delete API key <span className="font-mono font-semibold">{key.name}</span>?
            </>
          ),
        }}
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
              <label className={cn(fieldLabel, "mb-1 block")} htmlFor="key-name">
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
              <label className={fieldLabel} htmlFor="key-enabled">
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
    </ResourceListPage>
  )
}
