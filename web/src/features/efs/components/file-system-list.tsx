import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { HardDrive, Trash2 } from "lucide-react"
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
import { QueryListState, Spinner, EmptyState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import { Badge } from "@/components/ui/badge"
import { Combobox } from "@/components/ui/combobox"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"

export function FileSystemList() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: fileSystems = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(efsFileSystemsQueryOptions())

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
      <ResourceListCard>
        {isLoading || fileSystems.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={fileSystems.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<HardDrive className="h-10 w-10" />}
                title="No file systems"
                description="Create a file system to get started."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>
                    Create file system
                  </CreateAction>
                }
              />
            }
            errorTitle="Failed to load file systems"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>File system</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Performance</TableHead>
                <TableHead>Throughput</TableHead>
                <TableHead>Encrypted</TableHead>
                <TableHead>Mount targets</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {fileSystems.map((fs) => (
                <TableRow key={fs.FileSystemId}>
                  <TableCell>
                    <ResourceName icon={HardDrive} name={fs.FileSystemId} />
                  </TableCell>
                  <TableCell className="text-fg-muted">{fs.Name || "—"}</TableCell>
                  <TableCell>
                    <FileSystemStateBadge state={fs.LifeCycleState ?? ""} />
                  </TableCell>
                  <TableCell className="text-fg-muted">{fs.PerformanceMode}</TableCell>
                  <TableCell className="text-fg-muted">{fs.ThroughputMode}</TableCell>
                  <TableCell className="text-fg-muted">{fs.Encrypted ? "Yes" : "No"}</TableCell>
                  <TableCell className="text-fg-muted">{fs.NumberOfMountTargets ?? 0}</TableCell>
                  <TableCell>
                    <RowActions>
                      <RowAction
                        label={`Delete ${fs.FileSystemId ?? "file system"}`}
                        tone="danger"
                        onClick={() => setDeleteTarget(fs.FileSystemId ?? "")}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </RowAction>
                    </RowActions>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      <CreateFileSystemDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete File System"
        description={
          <>
            Permanently delete <strong>{deleteTarget}</strong>? Its access points, tags, and
            policy are removed with it. This action cannot be undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
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
  CreationToken: z
    .string()
    .min(1, "Creation token is required")
    .max(64, "At most 64 characters"),
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
