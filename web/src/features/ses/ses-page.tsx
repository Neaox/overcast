import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { Mail } from "lucide-react"
import {
  sesIdentitiesQueryOptions,
  sesKeys,
  deleteIdentityMutationOptions,
  verifyIdentityMutationOptions,
} from "@/features/ses/data"
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
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

export function SesPage() {
  const [showVerify, setShowVerify] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: identities = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(sesIdentitiesQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof identities)[number]>()

  const verifyMut = useResourceMutation({
    options: verifyIdentityMutationOptions(),
    invalidateKeys: [sesKeys.identities()],
    successTitle: "Identity verified",
    successDescription: (identity) => identity,
    errorTitle: "Verify failed",
    onSuccess: () => setShowVerify(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteIdentityMutationOptions(),
    invalidateKeys: [sesKeys.identities()],
    successTitle: "Identity deleted",
    successDescription: (identity) => identity,
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const form = useForm({
    validators: {
      onChange: z.object({
        identity: z.string().min(1, "Required").max(320, "Max 320 chars"),
      }),
    },
    defaultValues: { identity: "" },
    onSubmit: ({ value }) => verifyMut.mutate(value.identity),
  })

  return (
    <ResourceListPage
      title="SES Identities"
      description="Simple Email Service — verified email addresses and domains"
      actions={
        <>
          <ServiceDocsButton
            service="ses"
            label="SES"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowVerify(true)}>Verify identity</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: identities, isLoading, error }}
        noun="verified identities"
        emptyIcon={Mail}
        emptyTitle="No verified identities"
        emptyDescription="Verify an email address or domain to send from it."
        rowKey={(id) => id.IdentityName ?? ""}
        columns={[
          {
            header: "Identity",
            cellClassName: "font-medium",
            sortValue: (id) => id.IdentityName,
            cell: (id) => id.IdentityName,
          },
          {
            header: "Type",
            cellClassName: "text-fg-muted uppercase",
            cell: (id) => id.IdentityType,
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (id) => id.IdentityName ?? "",
          label: (id) => id.IdentityName ?? "",
          noun: "identity",
          description: (id) => (
            <>
              <span className="font-mono font-medium">{id.IdentityName}</span> will be removed.
            </>
          ),
        }}
      />

      {/* Verify identity dialog */}
      <Dialog
        open={showVerify}
        onOpenChange={(open) => {
          setShowVerify(open)
          if (!open) form.reset()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Verify identity</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void form.handleSubmit()
            }}
          >
            <div className="flex flex-col gap-4 py-2">
              <form.Field name="identity">
                {(field) => (
                  <FormRow>
                    <FormField
                      label="Email address or domain"
                      error={fieldError(field.state.meta.errors)}
                      required
                    >
                      <Input
                        placeholder="user@example.com or example.com"
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
                  setShowVerify(false)
                  form.reset()
                }}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={verifyMut.isPending}>
                {verifyMut.isPending ? "Verifying…" : "Verify"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </ResourceListPage>
  )
}
