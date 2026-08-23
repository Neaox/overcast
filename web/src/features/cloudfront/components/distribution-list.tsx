import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, Globe, Trash2 } from "lucide-react"
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
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Spinner } from "@/components/ui/primitives"
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

interface DistributionListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function DistributionList({ sort, onSortChange }: DistributionListProps) {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; etag: string } | undefined>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: distributions = [],
    isLoading,
    isFetching,
    refetch,
    error,
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
    <ResourceListPage
      title="CloudFront Distributions"
      count={distributions.length}
      actions={
        <>
          <ServiceDocsButton
            service="cloudfront"
            label="CloudFront"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create distribution</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: distributions, isLoading, error }}
        noun="distributions"
        emptyIcon={Globe}
        emptyTitle="No distributions yet"
        emptyDescription="Create a CloudFront distribution to serve content from your origins."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create distribution</CreateAction>
        }
        errorTitle="Failed to load distributions"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(d) => d.id}
        onRowClick={(d) =>
          navigate({ to: "/cloudfront/$distributionId", params: { distributionId: d.id } })
        }
        columns={[
          {
            id: "id",
            header: "ID",
            sortValue: (d) => d.id,
            cell: (d) => <ResourceName icon={Globe} name={d.id} />,
          },
          {
            id: "domain-name",
            header: "Domain name",
            cellClassName: "text-fg-muted",
            sortValue: (d) => d.domainName,
            cell: (d) => d.domainName,
          },
          {
            header: "Status",
            cell: (d) => (
              <Badge variant={d.status === "Deployed" ? "success" : "warning"}>{d.status}</Badge>
            ),
          },
          {
            header: "Enabled",
            cell: (d) => (
              <Badge variant={d.enabled ? "accent" : "default"}>{d.enabled ? "Yes" : "No"}</Badge>
            ),
          },
          {
            id: "origins",
            header: "Origins",
            cellClassName: "text-fg-muted",
            sortValue: (d) => d.origins.length,
            cell: (d) => d.origins.length,
          },
          {
            header: "Comment",
            prose: true,
            cellClassName: "max-w-xs truncate",
            cell: (d) => d.comment,
          },
        ]}
        // The delete flow needs the distribution's ETag, which only GetDistribution
        // returns — so it is an async lookup before the confirm, not the
        // `onDelete` shape (a synchronous row → id → `mutate(id)`). Both actions
        // therefore stay in `rowActions` with the page's own ConfirmDialog below.
        rowActions={(d) => (
          <>
            <RowAction
              label={`View ${d.id}`}
              onClick={() =>
                navigate({ to: "/cloudfront/$distributionId", params: { distributionId: d.id } })
              }
            >
              <Eye className="h-3.5 w-3.5" />
            </RowAction>
            <RowAction
              label={`Delete ${d.id}`}
              tone="danger"
              onClick={() => void handleDelete(d.id)}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </RowAction>
          </>
        )}
      />

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
    </ResourceListPage>
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
          <DialogTitle>Create distribution</DialogTitle>
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
                  <label className="flex items-center gap-2 text-xs">
                    <input
                      type="checkbox"
                      className="h-4 w-4 rounded accent-accent"
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
