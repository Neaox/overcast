import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { Mail, Plus, Trash2, RefreshCw } from "lucide-react"
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
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { cn } from "@/lib/utils"

export function SesPage() {
  const [showVerify, setShowVerify] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: identities = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(sesIdentitiesQueryOptions())

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
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="SES Identities"
        description="Simple Email Service — verified email addresses and domains"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="ses"
              label="SES"
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
            <Button size="sm" onClick={() => setShowVerify(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Verify identity
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : identities.length === 0 ? (
        <EmptyState
          icon={<Mail className="h-8 w-8 opacity-40" />}
          title="No verified identities"
          description="Verify an email address or domain to send from it."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Identity</TableHead>
              <TableHead>Type</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {identities.map((id) => (
              <TableRow key={id.IdentityName}>
                <TableCell className="font-mono font-medium">{id.IdentityName}</TableCell>
                <TableCell className="text-xs text-fg-muted uppercase">{id.IdentityType}</TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(id.IdentityName ?? "")}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

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

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete identity?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            <span className="font-mono font-medium">{deleteTarget}</span> will be removed.
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleteMut.isPending}
              onClick={() => deleteTarget && deleteMut.mutate(deleteTarget)}
            >
              {deleteMut.isPending ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
