import { useMemo, useState } from "react"
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
import { stsCallerIdentityQueryOptions } from "@/features/sts/data"
import { accountRegionalBucketName, isAccountRegionalBucketName } from "@/features/s3/namespace"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Badge } from "@/components/ui/badge"
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
import { cn } from "@/lib/utils"
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
            cell: (b) => (
              <ResourceName icon={HardDrive} name={b.name}>
                {/* Overcast-side enhancement, not AWS parity: ListBuckets carries no
                    field naming a bucket's namespace, and the AWS console shows
                    nothing for an existing bucket either. This is inferred purely
                    from the name's reserved suffix — see features/s3/namespace.ts. */}
                {isAccountRegionalBucketName(b.name) && (
                  <Badge
                    variant="info"
                    title="Inferred from the name — AWS reports no namespace for an existing bucket"
                  >
                    Account regional
                  </Badge>
                )}
              </ResourceName>
            ),
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
        onSubmit={(name, region, namespace) => createMutation.mutate({ name, region, namespace })}
        loading={createMutation.isPending}
      />
    </ResourceListPage>
  )
}

// ─── Create bucket dialog ──────────────────────────────────────────────────

/** The base bucket-naming rule S3 applies regardless of namespace: 3–63
 * lowercase letters, digits, hyphens or dots, beginning and ending
 * alphanumeric (internal/serviceutil/validation.go's BucketName). In
 * account-regional mode this is checked against the *full* name — prefix
 * plus the reserved suffix — so the 63-char ceiling counts the suffix too. */
const BUCKET_NAME_PATTERN = /^[a-z0-9][a-z0-9\-.]{1,61}[a-z0-9]$/

/** default_account_id in *config.Config — what an unconfigured Overcast
 * reports for GetCallerIdentity, and what the preview falls back to before
 * that request resolves. */
const DEFAULT_ACCOUNT_ID = "000000000000"

type BucketNamespaceChoice = "global" | "account-regional"

const NAMESPACE_CHOICES: { value: BucketNamespaceChoice; label: string; detail: string }[] = [
  {
    value: "global",
    label: "Global",
    detail: "The classic S3 namespace — names must be unique across all of AWS.",
  },
  {
    value: "account-regional",
    label: "Account regional",
    detail:
      "Names only need to be unique within your account and this region. The account id, " +
      "region, and reserved -an suffix are appended to your prefix automatically.",
  },
]

/**
 * Builds the create-bucket form schema. A function rather than a module-level
 * constant because the account-regional full-name check needs the caller's
 * account id, which is only known once GetCallerIdentity resolves — see
 * CreateBucketDialog's `useMemo`.
 */
function createBucketSchema(accountId: string) {
  return z
    .object({
      namespace: z.enum(["global", "account-regional"]),
      // The full bucket name in global mode; just the prefix ahead of the
      // reserved suffix in account-regional mode — validated below, since
      // which one applies (and thus what "3–63 chars" is measured against)
      // depends on `namespace`.
      name: z.string(),
      region: z.string(),
    })
    .superRefine((val, ctx) => {
      if (val.name.length === 0) {
        ctx.addIssue({
          code: "custom",
          path: ["name"],
          message: val.namespace === "account-regional" ? "Prefix is required" : "Name is required",
        })
        return
      }
      const fullName =
        val.namespace === "account-regional"
          ? accountRegionalBucketName(val.name, accountId, val.region)
          : val.name
      if (!BUCKET_NAME_PATTERN.test(fullName)) {
        ctx.addIssue({
          code: "custom",
          path: ["name"],
          message:
            val.namespace === "account-regional"
              ? `Prefix + suffix ("${fullName}") must be 3–63 lowercase chars, numbers, hyphens or dots`
              : "Must be 3–63 lowercase chars, numbers, hyphens or dots",
        })
      }
    })
}

interface CreateBucketDialogProps {
  open: boolean
  defaultRegion: string
  onClose: () => void
  onSubmit: (name: string, region: string, namespace?: "account-regional") => void
  loading: boolean
}

function CreateBucketDialog({
  open,
  defaultRegion,
  onClose,
  onSubmit,
  loading,
}: CreateBucketDialogProps) {
  // Only fetched while the dialog can show it — the preview and the
  // full-name validation both need the real account id, but nothing here
  // needs it before the user opens the dialog.
  const { data: callerIdentity } = useQuery({
    ...stsCallerIdentityQueryOptions(),
    enabled: open,
  })
  const accountId = callerIdentity?.Account ?? DEFAULT_ACCOUNT_ID
  const bucketSchema = useMemo(() => createBucketSchema(accountId), [accountId])

  const form = useForm({
    validators: { onChange: bucketSchema },
    defaultValues: {
      namespace: "global" as BucketNamespaceChoice,
      name: "",
      region: defaultRegion,
    },
    onSubmit: ({ value }) =>
      value.namespace === "account-regional"
        ? onSubmit(
            accountRegionalBucketName(value.name, accountId, value.region),
            value.region,
            "account-regional",
          )
        : onSubmit(value.name, value.region),
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
          <form.Field name="namespace">
            {(field) => (
              <FormField label="S3 Bucket Namespace" htmlFor="bnamespace">
                <div className="flex flex-col gap-2" id="bnamespace">
                  {NAMESPACE_CHOICES.map((c) => (
                    <label
                      key={c.value}
                      className={cn(
                        "flex cursor-pointer gap-2 rounded-lg border p-3 text-sm",
                        field.state.value === c.value
                          ? "border-accent bg-accent/10"
                          : "border-border hover:bg-bg-muted",
                      )}
                    >
                      <input
                        type="radio"
                        name="bucket-namespace"
                        className="mt-1 self-start accent-accent"
                        value={c.value}
                        checked={field.state.value === c.value}
                        onChange={() => field.handleChange(c.value)}
                      />
                      <span className="flex flex-col gap-1">
                        <span className="font-medium text-fg">{c.label}</span>
                        <span className="text-xs text-fg-muted">{c.detail}</span>
                      </span>
                    </label>
                  ))}
                </div>
              </FormField>
            )}
          </form.Field>
          <form.Subscribe selector={(s) => s.values.namespace}>
            {(namespace) => (
              <form.Field name="name">
                {(field) => (
                  <FormField
                    label={namespace === "account-regional" ? "Bucket name prefix" : "Bucket name"}
                    htmlFor="bname"
                    required
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Input
                      id="bname"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value.toLowerCase())}
                      onBlur={field.handleBlur}
                      placeholder={namespace === "account-regional" ? "logs" : "my-bucket"}
                      spellCheck={false}
                    />
                    {namespace === "account-regional" && (
                      <form.Subscribe selector={(s) => [s.values.name, s.values.region] as const}>
                        {([name, region]) => (
                          <p className="truncate font-mono text-xs text-fg-muted">
                            {accountRegionalBucketName(name || "<prefix>", accountId, region)}
                          </p>
                        )}
                      </form.Subscribe>
                    )}
                  </FormField>
                )}
              </form.Field>
            )}
          </form.Subscribe>
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
