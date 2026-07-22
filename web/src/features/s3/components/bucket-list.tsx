import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { HardDrive, Plus, Trash2, RefreshCw } from "lucide-react"
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
  Table,
  TableBody,
  TableCell,
  TableEmpty,
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
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { EmptyState, PageHeader, QueryListState } from "@/components/ui/primitives"
import { formatDate } from "@/lib/format"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { cn } from "@/lib/utils"

export function BucketList() {
  const endpoint = useEndpoint()
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
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
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="S3 Buckets"
        description={`${buckets.length} bucket${buckets.length !== 1 ? "s" : ""}`}
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
            <Button
              variant="ghost"
              size="icon"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="md" onClick={() => setShowCreate(true)}>
              <Plus className="h-4 w-4" /> New bucket
            </Button>
          </>
        }
      />

      <div className="overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {isLoading || buckets.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={buckets.length === 0}
            error={error}
            empty={
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  <TableEmpty>
                    <EmptyState
                      icon={<HardDrive className="h-8 w-8" />}
                      title="No buckets yet"
                      description="Create a bucket to get started."
                      action={
                        <Button size="sm" onClick={() => setShowCreate(true)}>
                          <Plus className="h-3.5 w-3.5" />
                          New bucket
                        </Button>
                      }
                    />
                  </TableEmpty>
                </TableBody>
              </Table>
            }
            errorTitle="Failed to load buckets"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {buckets.map((b) => (
                <TableRow
                  key={b.name}
                  className="group"
                  onClick={() => navigate({ to: "/s3/$bucket", params: { bucket: b.name } })}
                >
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <HardDrive className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
                      <span className="font-medium text-accent hover:underline">{b.name}</span>
                    </div>
                  </TableCell>
                  <TableCell className="text-fg-muted">{formatDate(b.creationDate)}</TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-fg-subtle opacity-0 group-hover:opacity-100 hover:text-danger"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteTarget(b.name)
                      }}
                      title="Delete bucket"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      {/* Create dialog */}
      <CreateBucketDialog
        open={showCreate}
        defaultRegion={endpoint.region}
        onClose={() => setShowCreate(false)}
        onSubmit={(name, region) => createMutation.mutate({ name, region })}
        loading={createMutation.isPending}
      />

      {/* Delete confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(o) => !o && setDeleteTarget(undefined)}
        title="Delete bucket?"
        description={
          <>
            This will permanently delete <span className="font-medium text-fg">{deleteTarget}</span>{" "}
            and all objects inside it. This cannot be undone.
          </>
        }
        isPending={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
      />
    </div>
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
