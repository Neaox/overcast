import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { KeyRound } from "lucide-react"
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
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { CreateAction, RefreshAction, ResourceListPage } from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { useToast } from "@/components/ui/toast"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { formatDate } from "@/lib/format"

export function SecretsManagerPage() {
  const qc = useQueryClient()
  const { toast } = useToast()
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<SecretSummary>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: secrets = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(secretsListQueryOptions())

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

  const deleteMut = useResourceMutation({
    options: deleteSecretMutationOptions(),
    invalidateKeys: [smKeys.secrets()],
    successTitle: "Secret deleted",
    successDescription: (name) => name,
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
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
    <ResourceListPage
      title="Secrets Manager"
      description="Store, manage, and retrieve secrets"
      actions={
        <>
          <ServiceDocsButton
            service="secretsmanager"
            label="Secrets Manager"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create secret</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: secrets, isLoading, error }}
        noun="secrets"
        emptyIcon={KeyRound}
        emptyTitle="No secrets"
        emptyDescription="Create a secret to store sensitive data."
        rowKey={(sec) => sec.Name ?? ""}
        onRowClick={(sec) =>
          navigate({
            to: "/secretsmanager/$secretName",
            params: { secretName: sec.Name ?? "" },
          })
        }
        columns={[
          {
            header: "Name",
            cellClassName: "font-medium",
            sortValue: (sec) => sec.Name,
            cell: (sec) => sec.Name,
          },
          {
            header: "Description",
            prose: true,
            cell: (sec) => sec.Description || "—",
          },
          {
            header: "Last changed",
            cellClassName: "text-fg-muted",
            sortValue: (sec) => sec.LastChangedDate,
            cell: (sec) => formatDate(sec.LastChangedDate),
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (sec) => sec.Name ?? "",
          label: (sec) => sec.Name ?? "",
          noun: "secret",
          title: "Delete secret",
          confirmLabel: "Delete secret",
          description: (sec) => (
            <>
              Permanently delete <strong>{sec.Name}</strong>? This cannot be undone.
            </>
          ),
        }}
      />

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
                        className="flex min-h-20 w-full rounded-md border border-border bg-bg px-3 py-2 font-mono text-sm placeholder:text-fg-subtle focus-visible:border-accent focus-visible:ring-1 focus-visible:ring-accent focus-visible:outline-none"
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
    </ResourceListPage>
  )
}
