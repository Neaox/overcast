import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { KeyRound, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  secretsListQueryOptions,
  smKeys,
  createSecretMutationOptions,
  deleteSecretMutationOptions,
} from "@/features/secretsmanager/data"
import type { SecretSummary } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow, fieldError } from "@/components/ui/form"
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
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

export function SecretsManagerPage() {
  const qc = useQueryClient()
  const { toast } = useToast()
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<SecretSummary>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const { data: secrets = [], isLoading, isFetching, refetch } = useQuery(secretsListQueryOptions())

  const createMut = useMutation({
    ...createSecretMutationOptions(),
    onSuccess: (_, vars) => {
      void qc.invalidateQueries({ queryKey: smKeys.secrets() })
      setShowCreate(false)
      toast({ title: "Secret created", description: vars.Name, variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteSecretMutationOptions(),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: smKeys.secrets() })
      setDeleteTarget(undefined)
      toast({ title: "Secret deleted", description: name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const form = useForm({
    validators: {
      onChange: z.object({
        name: z.string().min(1, "Required").max(512, "Max 512 chars"),
        secretString: z.string(),
        description: z.string(),
      }),
    },
    defaultValues: { name: "", secretString: "", description: "" },
    onSubmit: ({ value }) =>
      createMut.mutate({
        Name: value.name,
        SecretString: value.secretString || undefined,
        Description: value.description || undefined,
      }),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Secrets Manager"
        description="Store, manage, and retrieve secrets"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="secretsmanager"
              label="Secrets Manager"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create secret
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : secrets.length === 0 ? (
        <EmptyState
          icon={<KeyRound className="h-8 w-8 opacity-40" />}
          title="No secrets"
          description="Create a secret to store sensitive data."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Last changed</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {secrets.map((sec) => (
              <TableRow
                key={sec.Name}
                className="cursor-pointer"
                onClick={() =>
                  navigate({
                    to: "/secretsmanager/$secretName",
                    params: { secretName: sec.Name ?? "" },
                  })
                }
              >
                <TableCell className="font-mono font-medium">{sec.Name}</TableCell>
                <TableCell className="text-sm text-fg-muted">{sec.Description || "—"}</TableCell>
                <TableCell className="text-sm text-fg-muted">
                  {formatDate(sec.LastChangedDate)}
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-danger hover:text-danger"
                    title="Delete"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget(sec)
                    }}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* Create secret dialog */}
      <Dialog
        open={showCreate}
        onOpenChange={(open) => {
          setShowCreate(open)
          if (!open) form.reset()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create secret</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void form.handleSubmit()
            }}
          >
            <div className="flex flex-col gap-4 py-2">
              <form.Field name="name">
                {(field) => (
                  <FormRow>
                    <FormField
                      label="Secret name"
                      error={fieldError(field.state.meta.errors)}
                      required
                    >
                      <Input
                        placeholder="my-app/db-password"
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
              <form.Field name="description">
                {(field) => (
                  <FormRow>
                    <FormField label="Description" error={fieldError(field.state.meta.errors)}>
                      <Input
                        placeholder="Optional description"
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
              <form.Field name="secretString">
                {(field) => (
                  <FormRow>
                    <FormField label="Secret value" error={fieldError(field.state.meta.errors)}>
                      <textarea
                        className="flex min-h-20 w-full rounded-md border border-border bg-bg px-3 py-2 font-mono text-sm placeholder:text-fg-subtle focus-visible:ring-1 focus-visible:outline-none"
                        placeholder='{"username":"admin","password":"s3cret"}'
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setShowCreate(false)
                  form.reset()
                }}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={createMut.isPending}>
                {createMut.isPending ? "Creating…" : "Create"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete secret"
        description={
          <>
            Permanently delete <strong>{deleteTarget?.Name}</strong>? This cannot be undone.
          </>
        }
        confirmLabel="Delete secret"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.Name ?? "")}
      />
    </div>
  )
}
