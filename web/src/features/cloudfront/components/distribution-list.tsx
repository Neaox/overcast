import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Globe, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  cloudfrontDistributionsQueryOptions,
  cloudfrontKeys,
  createDistributionMutationOptions,
  deleteDistributionMutationOptions,
} from "@/features/cloudfront/data"
import { cloudfront } from "@/services/api"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
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
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { cn } from "@/lib/utils"

export function DistributionList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; etag: string } | undefined>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: distributions = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(cloudfrontDistributionsQueryOptions())

  const createMut = useResourceMutation({
    options: createDistributionMutationOptions(),
    invalidateKeys: [cloudfrontKeys.distributions()],
    successTitle: "Distribution created",
    successDescription: ({ comment }) => comment || "New distribution",
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteDistributionMutationOptions(),
    invalidateKeys: [cloudfrontKeys.distributions()],
    successTitle: "Distribution deleted",
    successDescription: ({ id }) => id,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  async function handleDelete(id: string) {
    try {
      const { etag } = await cloudfront.getDistribution(id)
      setDeleteTarget({ id, etag })
    } catch {
      setDeleteTarget({ id, etag: "" })
    }
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="CloudFront Distributions"
        description={`${distributions.length} distribution${distributions.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="cloudfront"
              label="CloudFront"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Distribution
            </Button>
          </>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : distributions.length === 0 ? (
        <EmptyState
          icon={<Globe className="h-10 w-10" />}
          title="No distributions yet"
          description="Create a CloudFront distribution to serve content from your origins."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Distribution
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Domain Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead>Origins</TableHead>
              <TableHead>Comment</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {distributions.map((d) => (
              <TableRow
                key={d.id}
                className="cursor-pointer"
                onClick={() =>
                  navigate({
                    to: "/cloudfront/$distributionId",
                    params: { distributionId: d.id },
                  })
                }
              >
                <TableCell className="font-mono text-xs">{d.id}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">{d.domainName}</TableCell>
                <TableCell>
                  <Badge variant={d.status === "Deployed" ? "success" : "warning"}>
                    {d.status}
                  </Badge>
                </TableCell>
                <TableCell>
                  <Badge variant={d.enabled ? "accent" : "default"}>
                    {d.enabled ? "Yes" : "No"}
                  </Badge>
                </TableCell>
                <TableCell className="text-fg-muted">{d.origins.length}</TableCell>
                <TableCell className="max-w-xs truncate text-fg-muted">{d.comment}</TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    onClick={(e) => {
                      e.stopPropagation()
                      void handleDelete(d.id)
                    }}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* ── Create distribution dialog ── */}
      <CreateDistributionDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(values) => createMut.mutate(values)}
      />

      {/* ── Delete confirm dialog ── */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Distribution"
        description={
          <>
            Permanently delete distribution <strong>{deleteTarget?.id}</strong>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── CreateDistributionDialog ─────────────────────────────────────────────────

const createDistSchema = z.object({
  comment: z.string().min(1, "Comment is required"),
  enabled: z.boolean(),
  originDomainName: z.string().min(1, "Origin domain name is required"),
  originId: z.string().min(1, "Origin ID is required"),
  defaultRootObject: z.string(),
})

type CreateDistValues = z.infer<typeof createDistSchema>

function CreateDistributionDialog({
  open,
  onClose,
  onSubmit,
  isPending,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (values: CreateDistValues) => void
  isPending: boolean
}) {
  const form = useForm({
    validators: { onChange: createDistSchema },
    defaultValues: {
      comment: "",
      enabled: true,
      originDomainName: "",
      originId: "origin-1",
      defaultRootObject: "",
    },
    onSubmit: ({ value }) => onSubmit(value),
  })

  function handleClose() {
    onClose()
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Distribution</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="flex flex-col gap-4">
            <form.Field name="comment">
              {(field) => (
                <FormField label="Comment" error={fieldError(field.state.meta.errors)}>
                  <Input
                    placeholder="My distribution"
                    value={field.state.value}
                    onBlur={field.handleBlur}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                </FormField>
              )}
            </form.Field>

            <form.Field name="originDomainName">
              {(field) => (
                <FormField label="Origin Domain Name" error={fieldError(field.state.meta.errors)}>
                  <Input
                    placeholder="my-bucket.s3.amazonaws.com"
                    value={field.state.value}
                    onBlur={field.handleBlur}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                </FormField>
              )}
            </form.Field>

            <form.Field name="originId">
              {(field) => (
                <FormField label="Origin ID" error={fieldError(field.state.meta.errors)}>
                  <Input
                    placeholder="origin-1"
                    value={field.state.value}
                    onBlur={field.handleBlur}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                </FormField>
              )}
            </form.Field>

            <form.Field name="defaultRootObject">
              {(field) => (
                <FormField label="Default Root Object" error={fieldError(field.state.meta.errors)}>
                  <Input
                    placeholder="index.html (optional)"
                    value={field.state.value}
                    onBlur={field.handleBlur}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                </FormField>
              )}
            </form.Field>

            <FormRow>
              <form.Field name="enabled">
                {(field) => (
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      className="accent-primary h-4 w-4 rounded"
                      checked={field.state.value}
                      onChange={(e) => field.handleChange(e.target.checked)}
                    />
                    Enabled
                  </label>
                )}
              </form.Field>
            </FormRow>
          </DialogBody>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={handleClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending && <Spinner className="mr-2" />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
