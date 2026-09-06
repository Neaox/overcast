import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { HardDrive } from "lucide-react"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import {
  efsFileSystemsQueryOptions,
  efsKeys,
  createFileSystemMutationOptions,
  deleteFileSystemMutationOptions,
} from "@/features/efs/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { Combobox } from "@/components/ui/combobox"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"

interface FileSystemListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function FileSystemList({ sort, onSortChange }: FileSystemListProps = {}) {
  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: fileSystems = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(efsFileSystemsQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof fileSystems)[number]>()

  const createMut = useResourceMutation({
    options: createFileSystemMutationOptions(),
    invalidateKeys: [efsKeys.fileSystems()],
    successTitle: "File system created",
    successDescription: (opts) => opts.CreationToken ?? "",
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteFileSystemMutationOptions(),
    invalidateKeys: [efsKeys.fileSystems()],
    successTitle: "File system deleted",
    successDescription: (id) => id,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <ResourceListPage
      title="EFS File Systems"
      count={fileSystems.length}
      actions={
        <>
          <ServiceDocsButton
            service="efs"
            label="EFS"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create file system</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: fileSystems, isLoading, error }}
        noun="file systems"
        emptyIcon={HardDrive}
        emptyTitle="No file systems"
        emptyDescription="Create a file system to get started."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create file system</CreateAction>
        }
        errorTitle="Failed to load file systems"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(fs) => fs.FileSystemId ?? ""}
        columns={[
          {
            id: "file-system",
            header: "File system",
            sortValue: (fs) => fs.FileSystemId,
            cell: (fs) => <ResourceName icon={HardDrive} name={fs.FileSystemId} />,
          },
          {
            id: "name",
            header: "Name",
            cellClassName: "text-fg-muted",
            sortValue: (fs) => fs.Name,
            cell: (fs) => fs.Name || "—",
          },
          {
            header: "State",
            cell: (fs) => <FileSystemStateBadge state={fs.LifeCycleState ?? ""} />,
          },
          {
            header: "Performance",
            cellClassName: "text-fg-muted",
            cell: (fs) => fs.PerformanceMode,
          },
          {
            header: "Throughput",
            cellClassName: "text-fg-muted",
            cell: (fs) => fs.ThroughputMode,
          },
          {
            header: "Encrypted",
            cellClassName: "text-fg-muted",
            cell: (fs) => (fs.Encrypted ? "Yes" : "No"),
          },
          {
            id: "mount-targets",
            header: "Mount targets",
            cellClassName: "text-fg-muted",
            sortValue: (fs) => fs.NumberOfMountTargets ?? 0,
            cell: (fs) => fs.NumberOfMountTargets ?? 0,
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (v) => !v && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (fs) => fs.FileSystemId ?? "",
          label: (fs) => fs.FileSystemId ?? "",
          noun: "file system",
          title: "Delete File System",
          actionLabel: (fs) => `Delete ${fs.FileSystemId ?? "file system"}`,
          description: (fs) => (
            <>
              Permanently delete <strong>{fs.FileSystemId}</strong>? Its access points, tags, and
              policy are removed with it. This action cannot be undone.
            </>
          ),
        }}
      />

      <CreateFileSystemDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />
    </ResourceListPage>
  )
}

// ─── State badge ──────────────────────────────────────────────────────────

function FileSystemStateBadge({ state }: { state: string }) {
  const variant =
    state === "available"
      ? "success"
      : state === "creating" || state === "updating"
        ? "warning"
        : state === "deleting" || state === "error"
          ? "danger"
          : "default"
  return <Badge variant={variant}>{state}</Badge>
}

// ─── Create File System Dialog ────────────────────────────────────────────

const createSchema = z.object({
  Name: z.string(),
  CreationToken: z.string().min(1, "Creation token is required").max(64, "At most 64 characters"),
  PerformanceMode: z.enum(["generalPurpose", "maxIO"]),
  ThroughputMode: z.enum(["bursting", "elastic", "provisioned"]),
  Encrypted: z.boolean(),
})

function CreateFileSystemDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (opts: {
    CreationToken: string
    PerformanceMode?: "generalPurpose" | "maxIO"
    ThroughputMode?: "bursting" | "elastic" | "provisioned"
    Encrypted?: boolean
    Tags?: { Key: string; Value: string }[]
  }) => void
}) {
  const form = useForm({
    validators: { onChange: createSchema },
    defaultValues: {
      Name: "",
      CreationToken: "",
      PerformanceMode: "generalPurpose" as "generalPurpose" | "maxIO",
      ThroughputMode: "bursting" as "bursting" | "elastic" | "provisioned",
      Encrypted: false,
    },
    onSubmit: ({ value }) =>
      onSubmit({
        CreationToken: value.CreationToken,
        PerformanceMode: value.PerformanceMode,
        ThroughputMode: value.ThroughputMode,
        Encrypted: value.Encrypted,
        Tags: value.Name ? [{ Key: "Name", Value: value.Name }] : undefined,
      }),
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          form.reset()
        }
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create file system</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="space-y-4">
            <form.Field name="CreationToken">
              {(field) => (
                <FormField
                  label="Creation token"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="my-file-system"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field name="Name">
              {(field) => (
                <FormField
                  label="Name (tag, optional)"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="shared-files"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <div className="grid grid-cols-2 gap-4">
              <form.Field name="PerformanceMode">
                {(field) => (
                  <FormField
                    label="Performance mode"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Combobox<{ value: string }>
                      value={field.state.value}
                      onChange={(v) => field.handleChange(v as "generalPurpose" | "maxIO")}
                      items={[{ value: "generalPurpose" }, { value: "maxIO" }]}
                      filterFn={(item, q) => item.value.toLowerCase().includes(q.toLowerCase())}
                      getItemValue={(item) => item.value}
                      renderItem={(item) => item.value}
                      placeholder="Select mode…"
                    />
                  </FormField>
                )}
              </form.Field>
              <form.Field name="ThroughputMode">
                {(field) => (
                  <FormField
                    label="Throughput mode"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Combobox<{ value: string }>
                      value={field.state.value}
                      onChange={(v) =>
                        field.handleChange(v as "bursting" | "elastic" | "provisioned")
                      }
                      items={[{ value: "bursting" }, { value: "elastic" }]}
                      filterFn={(item, q) => item.value.toLowerCase().includes(q.toLowerCase())}
                      getItemValue={(item) => item.value}
                      renderItem={(item) => item.value}
                      placeholder="Select mode…"
                    />
                  </FormField>
                )}
              </form.Field>
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={onClose}>
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
