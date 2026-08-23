import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, HardDrive } from "lucide-react"
import {
  s3BucketsQueryOptions,
  s3Keys,
  createBucketMutationOptions,
  deleteBucketMutationOptions,
} from "@/features/s3/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
import { RegionSelect } from "@/components/ui/region-select"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { formatDate } from "@/lib/format"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { CopyUrlButton } from "@/components/ui/copy-url-button"
import { s3CopyFormats } from "@/features/s3/copy-formats"
import type { S3Bucket } from "@/types"

interface BucketListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function BucketList({ sort, onSortChange }: BucketListProps = {}) {
  const endpoint = useEndpoint()
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<S3Bucket>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: buckets = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(s3BucketsQueryOptions())

  const createMutation = useResourceMutation({
    options: createBucketMutationOptions(),
    invalidateKeys: [s3Keys.buckets()],
    successTitle: "Bucket created",
    successDescription: ({ name }) => name,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMutation = useResourceMutation({
    options: deleteBucketMutationOptions(),
    invalidateKeys: [s3Keys.buckets()],
    successTitle: "Bucket deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <ResourceListPage
      title="S3 Buckets"
      count={buckets.length}
      actions={
        <>
          <ServiceDocsButton
            service="s3"
            label="S3"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RawStateLink service="s3" />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create bucket</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: buckets, isLoading, error }}
        noun="buckets"
        emptyIcon={HardDrive}
        emptyTitle="No buckets yet"
        emptyDescription="Create a bucket to get started."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create bucket</CreateAction>}
        errorTitle="Failed to load buckets"
        sort={sort}
        onSortChange={onSortChange}
        // Three columns, one of which is the copy-URL control — nothing worth hiding.
        columnToggle={false}
        rowKey={(b) => b.name}
        onRowClick={(b) => navigate({ to: "/s3/$bucket", params: { bucket: b.name } })}
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (b) => b.name,
            cell: (b) => <ResourceName icon={HardDrive} name={b.name} />,
          },
          {
            // The row navigates on click and `ResourceTable` stops propagation
            // only for the row-actions cell — but `CopyUrlButton`'s trigger
            // stops it itself, so opening the format menu does not also open
            // the bucket.
            header: "URL",
            cell: (b) => (
              <CopyUrlButton formats={s3CopyFormats(endpoint.baseUrl, b.name)} noun="bucket URL" />
            ),
          },
          {
            id: "created",
            header: "Created",
            cellClassName: "text-fg-muted",
            // A `Date`, not the ISO string it arrives as: a string column sorts
            // A→Z on the first click, and "newest first" is what someone
            // clicking a Created header is after.
            sortValue: (b) => new Date(b.creationDate),
            cell: (b) => formatDate(b.creationDate),
          },
        ]}
        rowActions={(b) => (
          <RowAction
            label={`View ${b.name}`}
            onClick={() => navigate({ to: "/s3/$bucket", params: { bucket: b.name } })}
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (o) => !o && setDeleteTarget(undefined),
          mutation: deleteMutation,
          getId: (b) => b.name,
          label: (b) => b.name,
          noun: "bucket",
          title: "Delete bucket?",
          description: (b) => (
            <>
              This will permanently delete <span className="font-medium text-fg">{b.name}</span> and
              all objects inside it. This cannot be undone.
            </>
          ),
        }}
      />

      {/* Create dialog */}
      <CreateBucketDialog
        open={showCreate}
        defaultRegion={endpoint.region}
        onClose={() => setShowCreate(false)}
        onSubmit={(name, region) => createMutation.mutate({ name, region })}
        loading={createMutation.isPending}
      />
    </ResourceListPage>
  )
}

// ─── Create bucket dialog ──────────────────────────────────────────────────
const bucketSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .regex(
      /^[a-z0-9][a-z0-9\-.]{1,61}[a-z0-9]$/,
      "Must be 3–63 lowercase chars, numbers, hyphens or dots",
    ),
  region: z.string(),
})

interface CreateBucketDialogProps {
  open: boolean
  defaultRegion: string
  onClose: () => void
  onSubmit: (name: string, region: string) => void
  loading: boolean
}

function CreateBucketDialog({
  open,
  defaultRegion,
  onClose,
  onSubmit,
  loading,
}: CreateBucketDialogProps) {
  const form = useForm({
    validators: { onChange: bucketSchema },
    defaultValues: { name: "", region: defaultRegion },
    onSubmit: ({ value }) => onSubmit(value.name, value.region),
  })

  function handleClose() {
    onClose()
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create bucket</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          <form.Field name="name" validators={{ onChange: bucketSchema.shape.name }}>
            {(field) => (
              <FormField
                label="Bucket name"
                htmlFor="bname"
                required
                error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
              >
                <Input
                  id="bname"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value.toLowerCase())}
                  onBlur={field.handleBlur}
                  placeholder="my-bucket"
                  spellCheck={false}
                />
              </FormField>
            )}
          </form.Field>
          <form.Field name="region">
            {(field) => (
              <FormField label="Region" htmlFor="bregion">
                <RegionSelect
                  id="bregion"
                  value={field.state.value}
                  onChange={(v) => field.handleChange(v)}
                />
              </FormField>
            )}
          </form.Field>
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <form.Subscribe selector={(s) => s.canSubmit}>
              {(canSubmit) => (
                <Button type="submit" disabled={!canSubmit || loading}>
                  {loading ? "Creating…" : "Create"}
                </Button>
              )}
            </form.Subscribe>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
