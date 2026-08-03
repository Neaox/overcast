import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, FileText, Trash2 } from "lucide-react"
import {
  logsGroupsQueryOptions,
  logsKeys,
  createLogGroupMutationOptions,
  deleteLogGroupMutationOptions,
} from "@/features/cloudwatch/logs/data"
import { logs } from "@/services/api"
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
import { QueryListState, Spinner, EmptyState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
  SelectCheckbox,
} from "@/components/ui/resource-list-page"
import { useToast } from "@/components/ui/toast"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { formatLogDate } from "@/lib/log-format"

export function LogGroupList() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [selectedGroups, setSelectedGroups] = useState<Set<string>>(new Set())
  const [showBulkDelete, setShowBulkDelete] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: groups = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(logsGroupsQueryOptions())

  const createMut = useMutation({
    ...createLogGroupMutationOptions(),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: logsKeys.groups() })
      setShowCreate(false)
      toast({ title: "Log group created", description: name, variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteLogGroupMutationOptions(),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: logsKeys.groups() })
      setDeleteTarget(undefined)
      toast({ title: "Log group deleted", description: name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const bulkDeleteMut = useMutation({
    mutationFn: async (names: string[]) => {
      await Promise.all(names.map((n) => logs.deleteGroup(n)))
    },
    onSuccess: (_, names) => {
      void qc.invalidateQueries({ queryKey: logsKeys.groups() })
      setSelectedGroups(new Set())
      setShowBulkDelete(false)
      toast({
        title: `${names.length} log group${names.length !== 1 ? "s" : ""} deleted`,
        variant: "success",
      })
    },
    onError: (err: Error) =>
      toast({ title: "Bulk delete failed", description: err.message, variant: "danger" }),
  })

  return (
    <ResourceListPage
      title="CloudWatch Log Groups"
      count={groups.length}
      actions={
        <>
          <ServiceDocsButton
            service="cloudwatch-logs"
            label="CloudWatch Logs"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create log group</CreateAction>
        </>
      }
    >
      {selectedGroups.size > 0 && (
        <div className="flex items-center gap-3 rounded-card border border-border bg-bg-muted px-3 py-2">
          <span className="text-sm font-medium">
            {selectedGroups.size} group{selectedGroups.size !== 1 ? "s" : ""} selected
          </span>
          <Button size="sm" variant="danger" onClick={() => setShowBulkDelete(true)}>
            <Trash2 className="h-3.5 w-3.5" />
            Delete selected
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelectedGroups(new Set())}>
            Clear
          </Button>
        </div>
      )}

      <ResourceListCard>
        {isLoading || groups.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={groups.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<FileText className="h-10 w-10" />}
                title="No log groups yet"
                description="Create a log group to start collecting logs."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create log group</CreateAction>
                }
              />
            }
            errorTitle="Failed to load log groups"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10 pr-0">
                  <SelectCheckbox
                    label="Select all log groups"
                    checked={selectedGroups.size === groups.length}
                    indeterminate={selectedGroups.size > 0 && selectedGroups.size < groups.length}
                    onCheckedChange={(checked) =>
                      setSelectedGroups(
                        checked ? new Set(groups.map((g) => g.logGroupName ?? "")) : new Set(),
                      )
                    }
                  />
                </TableHead>
                <TableHead>Log group name</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Retention</TableHead>
                <TableHead>ARN</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groups.map((g) => {
                const groupName = g.logGroupName ?? ""
                return (
                  <TableRow
                    key={groupName}
                    onClick={() =>
                      navigate({ to: "/cloudwatch/logs/group", search: { groupName } })
                    }
                  >
                    <TableCell className="pr-0" onClick={(e) => e.stopPropagation()}>
                      <SelectCheckbox
                        label={`Select ${groupName}`}
                        checked={selectedGroups.has(groupName)}
                        onCheckedChange={(checked) => {
                          const next = new Set(selectedGroups)
                          if (checked) next.add(groupName)
                          else next.delete(groupName)
                          setSelectedGroups(next)
                        }}
                      />
                    </TableCell>
                    <TableCell title={groupName}>
                      <ResourceName icon={FileText} name={groupName} />
                    </TableCell>
                    <TableCell className="text-fg-muted">{formatLogDate(g.creationTime)}</TableCell>
                    <TableCell className="text-fg-muted">
                      {g.retentionInDays ? `${g.retentionInDays} days` : "Never expire"}
                    </TableCell>
                    <TableCell className="max-w-xs truncate text-fg-muted" title={g.arn}>
                      {g.arn}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <RowActions>
                        <RowAction
                          label={`View ${groupName}`}
                          onClick={() =>
                            navigate({ to: "/cloudwatch/logs/group", search: { groupName } })
                          }
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </RowAction>
                        <RowAction
                          label={`Delete ${groupName}`}
                          tone="danger"
                          onClick={() => setDeleteTarget(groupName)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </RowAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      {/* ── Create log group dialog ── */}
      <CreateLogGroupDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(name) => createMut.mutate(name)}
      />

      {/* ── Delete confirm dialog ── */}
      <Dialog open={!!deleteTarget} onOpenChange={(v) => !v && setDeleteTarget(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Log Group</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Permanently delete <strong>{deleteTarget}</strong> and all its log streams and events?
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteTarget && deleteMut.mutate(deleteTarget)}
              disabled={deleteMut.isPending}
            >
              {deleteMut.isPending && <Spinner className="mr-2" />}
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Bulk delete confirm dialog ── */}
      <Dialog open={showBulkDelete} onOpenChange={(v) => !v && setShowBulkDelete(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Delete {selectedGroups.size} Log Group{selectedGroups.size !== 1 ? "s" : ""}
            </DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-fg-muted">
              Permanently delete these log groups and all their streams and events?
            </p>
            <ul className="mt-3 max-h-40 overflow-y-auto rounded border border-border bg-bg-muted p-2 font-mono text-xs">
              {[...selectedGroups].map((name) => (
                <li key={name} className="truncate py-0.5">
                  {name}
                </li>
              ))}
            </ul>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setShowBulkDelete(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => bulkDeleteMut.mutate([...selectedGroups])}
              disabled={bulkDeleteMut.isPending}
            >
              {bulkDeleteMut.isPending && <Spinner className="mr-2" />}
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </ResourceListPage>
  )
}

// ─── CreateLogGroupDialog ─────────────────────────────────────────────────────

const createGroupSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .max(512, "Max 512 characters")
    .regex(/^[a-zA-Z0-9_./#-]+$/, "Letters, numbers, and . _ / # - only"),
})

function CreateLogGroupDialog({
  open,
  onClose,
  onSubmit,
  isPending,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (name: string) => void
  isPending: boolean
}) {
  const form = useForm({
    validators: { onChange: createGroupSchema },
    defaultValues: { name: "" },
    onSubmit: ({ value }) => onSubmit(value.name),
  })

  function handleClose() {
    onClose()
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create log group</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          <form.Field name="name" validators={{ onChange: createGroupSchema.shape.name }}>
            {(field) => (
              <FormRow>
                <FormField
                  label="Log Group Name"
                  required
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="/aws/lambda/my-function"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    autoFocus
                  />
                </FormField>
              </FormRow>
            )}
          </form.Field>
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
