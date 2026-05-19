import { useState, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { ArrowLeft, Plus, Trash2, RefreshCw, FileText, Search, X } from "lucide-react"
import {
  logsGroupsQueryOptions,
  logsStreamsQueryOptions,
  logsKeys,
  logsFilterQueryOptions,
  createLogStreamMutationOptions,
  deleteLogStreamMutationOptions,
  deleteLogGroupMutationOptions,
} from "@/features/cloudwatch/logs/data"
import { logs } from "@/services/api"
import {
  TimeRangeFilter,
  type TimeRange,
} from "@/features/cloudwatch/logs/components/time-range-filter"
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
import { Card, CardContent } from "@/components/ui/card"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useToast } from "@/components/ui/toast"
import { cn } from "@/lib/utils"

function formatTimestamp(ts: number): string {
  if (!ts) return "—"
  return new Date(ts).toLocaleString()
}

interface Props {
  groupName: string
}

export function LogGroupDetail({ groupName }: Props) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showCreateStream, setShowCreateStream] = useState(false)
  const [deleteStreamTarget, setDeleteStreamTarget] = useState<string>()
  const [selectedStreams, setSelectedStreams] = useState<Set<string>>(new Set())
  const [showBulkDelete, setShowBulkDelete] = useState(false)
  const [filterInput, setFilterInput] = useState("")
  const [activeFilter, setActiveFilter] = useState("")
  const [timeRange, setTimeRange] = useState<TimeRange>({})

  // ─── Data ────────────────────────────────────────────────────────────────
  const { data: allGroups = [] } = useQuery(logsGroupsQueryOptions())
  const group = allGroups.find((g) => g.logGroupName === groupName)

  const {
    data: streams = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(logsStreamsQueryOptions(groupName))

  // Cross-stream search query — only runs when user has an active filter.
  const {
    data: filterResult,
    isLoading: isFilterLoading,
    isFetching: isFilterFetching,
  } = useQuery({
    ...logsFilterQueryOptions(groupName, {
      filterPattern: activeFilter || undefined,
      startTime: timeRange.startTime,
      endTime: timeRange.endTime,
    }),
    enabled: !!activeFilter,
  })

  const filteredEvents = useMemo(() => filterResult?.events ?? [], [filterResult])

  // ─── Mutations ───────────────────────────────────────────────────────────
  const deleteGroupMut = useMutation({
    ...deleteLogGroupMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: logsKeys.groups() })
      void navigate({ to: "/cloudwatch/logs" })
      toast({ title: "Log group deleted", description: groupName })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const createStreamMut = useMutation({
    ...createLogStreamMutationOptions(groupName),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: logsKeys.streams(groupName) })
      setShowCreateStream(false)
      toast({ title: "Log stream created", description: name, variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteStreamMut = useMutation({
    ...deleteLogStreamMutationOptions(groupName),
    onSuccess: (_, streamName) => {
      void qc.invalidateQueries({ queryKey: logsKeys.streams(groupName) })
      setDeleteStreamTarget(undefined)
      toast({ title: "Log stream deleted", description: streamName })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const bulkDeleteMut = useMutation({
    mutationFn: async (names: string[]) => {
      await Promise.all(names.map((n) => logs.deleteStream(groupName, n)))
    },
    onSuccess: (_, names) => {
      void qc.invalidateQueries({ queryKey: logsKeys.streams(groupName) })
      setSelectedStreams(new Set())
      setShowBulkDelete(false)
      toast({
        title: `${names.length} stream${names.length !== 1 ? "s" : ""} deleted`,
        variant: "success",
      })
    },
    onError: (err: Error) =>
      toast({ title: "Bulk delete failed", description: err.message, variant: "danger" }),
  })

  return (
    <div className="flex w-full flex-col gap-6">
      <PageHeader
        title={groupName}
        description={group?.arn}
        actions={
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" onClick={() => navigate({ to: "/cloudwatch/logs" })}>
              <ArrowLeft className="mr-1.5 h-3.5 w-3.5" />
              All Groups
            </Button>
            <Button variant="ghost" size="sm" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setShowCreateStream(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create Stream
            </Button>
            <Button
              size="sm"
              variant="danger"
              onClick={() => deleteGroupMut.mutate(groupName)}
              disabled={deleteGroupMut.isPending}
            >
              <Trash2 className="mr-1 h-4 w-4" />
              Delete Group
            </Button>
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[group?.arn, groupName]} />

      {/* Group info card */}
      {group && (
        <Card>
          <CardContent className="grid grid-cols-3 gap-4 py-4">
            <div>
              <p className="text-xs font-medium text-fg-muted">Created</p>
              <p className="text-sm">{formatTimestamp(group.creationTime ?? 0)}</p>
            </div>
            <div>
              <p className="text-xs font-medium text-fg-muted">Retention</p>
              <p className="text-sm">
                {group.retentionInDays ? `${group.retentionInDays} days` : "Never expire"}
              </p>
            </div>
            <div>
              <p className="text-xs font-medium text-fg-muted">Log Streams</p>
              <p className="text-sm">{streams.length}</p>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Cross-stream search toolbar */}
      <div className="flex items-center gap-2 rounded-md border border-border bg-bg-muted px-3 py-2.5">
        <Search className="h-4 w-4 shrink-0 text-fg-muted" />
        <Input
          className="h-7 border-0 bg-transparent px-1 shadow-none focus-visible:ring-0"
          placeholder='Search across all streams — e.g. ERROR, "request failed", ERROR timeout'
          value={filterInput}
          onChange={(e) => setFilterInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") setActiveFilter(filterInput)
            if (e.key === "Escape") {
              setFilterInput("")
              setActiveFilter("")
            }
          }}
        />
        {(filterInput || activeFilter) && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setFilterInput("")
              setActiveFilter("")
            }}
            className="h-7 px-2"
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        )}
        <div className="mx-0.5 h-5 w-px bg-border" />
        <TimeRangeFilter value={timeRange} onChange={setTimeRange} />
        <Button
          size="sm"
          onClick={() => setActiveFilter(filterInput)}
          disabled={isFilterFetching || !filterInput.trim()}
          className="h-7"
        >
          {isFilterFetching ? <Spinner className="mr-1.5 h-3.5 w-3.5" /> : null}
          Search
        </Button>
        {activeFilter && !isFilterLoading && (
          <span className="ml-1 shrink-0 text-xs text-fg-muted">
            {filteredEvents.length} result{filteredEvents.length !== 1 ? "s" : ""}
          </span>
        )}
      </div>

      {/* Search results */}
      {activeFilter && (
        <>
          {isFilterLoading ? (
            <div className="flex justify-center py-8">
              <Spinner className="h-5 w-5" />
            </div>
          ) : filteredEvents.length === 0 ? (
            <EmptyState
              icon={<Search className="h-8 w-8" />}
              title="No matching events"
              description="Try a different filter pattern."
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-44">Timestamp</TableHead>
                  <TableHead className="w-40">Stream</TableHead>
                  <TableHead>Message</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredEvents.map((evt, i) => (
                  <TableRow
                    key={`${evt.timestamp}-${evt.logStreamName}-${i}`}
                    className="cursor-pointer"
                    onClick={() =>
                      navigate({
                        to: "/cloudwatch/logs/stream",
                        search: { groupName, streamName: evt.logStreamName ?? "" },
                      })
                    }
                  >
                    <TableCell className="font-mono text-xs whitespace-nowrap text-fg-muted">
                      {formatTimestamp(evt.timestamp ?? 0)}
                    </TableCell>
                    <TableCell className="text-xs text-fg-muted">{evt.logStreamName}</TableCell>
                    <TableCell>
                      <pre className="max-w-2xl font-mono text-xs break-all whitespace-pre-wrap">
                        {evt.message}
                      </pre>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </>
      )}

      {/* Streams table */}
      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : streams.length === 0 ? (
        <EmptyState
          icon={<FileText className="h-10 w-10" />}
          title="No log streams"
          description="Create a log stream or wait for your application to generate logs."
          action={
            <Button onClick={() => setShowCreateStream(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Stream
            </Button>
          }
        />
      ) : (
        <>
          {selectedStreams.size > 0 && (
            <div className="flex items-center gap-3 rounded-md border border-border bg-bg-muted px-3 py-2">
              <span className="text-sm font-medium">
                {selectedStreams.size} stream{selectedStreams.size !== 1 ? "s" : ""} selected
              </span>
              <Button size="sm" variant="danger" onClick={() => setShowBulkDelete(true)}>
                <Trash2 className="mr-1 h-3.5 w-3.5" />
                Delete Selected
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setSelectedStreams(new Set())}>
                Clear
              </Button>
            </div>
          )}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <input
                    type="checkbox"
                    className="accent-primary h-4 w-4 rounded"
                    checked={streams.length > 0 && selectedStreams.size === streams.length}
                    ref={(el) => {
                      if (el)
                        el.indeterminate =
                          selectedStreams.size > 0 && selectedStreams.size < streams.length
                    }}
                    onChange={(e) => {
                      if (e.target.checked) {
                        setSelectedStreams(new Set(streams.map((s) => s.logStreamName ?? "")))
                      } else {
                        setSelectedStreams(new Set())
                      }
                    }}
                  />
                </TableHead>
                <TableHead>Stream Name</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Last Event</TableHead>
                <TableHead>Last Ingestion</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {streams.map((s) => (
                <TableRow
                  key={s.logStreamName}
                  className="cursor-pointer"
                  onClick={() =>
                    navigate({
                      to: "/cloudwatch/logs/stream",
                      search: { groupName, streamName: s.logStreamName ?? "" },
                    })
                  }
                >
                  <TableCell className="p-0">
                    <label
                      className="flex cursor-pointer items-center justify-center p-3"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <input
                        type="checkbox"
                        className="accent-primary h-4 w-4 rounded"
                        checked={selectedStreams.has(s.logStreamName ?? "")}
                        onChange={(e) => {
                          const next = new Set(selectedStreams)
                          if (e.target.checked) {
                            next.add(s.logStreamName ?? "")
                          } else {
                            next.delete(s.logStreamName ?? "")
                          }
                          setSelectedStreams(next)
                        }}
                      />
                    </label>
                  </TableCell>
                  <TableCell className="font-medium">{s.logStreamName}</TableCell>
                  <TableCell className="text-fg-muted">
                    {formatTimestamp(s.creationTime ?? 0)}
                  </TableCell>
                  <TableCell className="text-fg-muted">
                    {formatTimestamp(s.lastEventTimestamp ?? 0)}
                  </TableCell>
                  <TableCell className="text-fg-muted">
                    {formatTimestamp(s.lastIngestionTime ?? 0)}
                  </TableCell>
                  <TableCell>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-fg-muted hover:text-danger"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteStreamTarget(s.logStreamName)
                      }}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}

      {/* ── Create stream dialog ── */}
      <CreateStreamDialog
        open={showCreateStream}
        onClose={() => setShowCreateStream(false)}
        isPending={createStreamMut.isPending}
        onSubmit={(name) => createStreamMut.mutate(name)}
      />

      {/* ── Delete stream confirm ── */}
      <Dialog
        open={!!deleteStreamTarget}
        onOpenChange={(v) => !v && setDeleteStreamTarget(undefined)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Log Stream</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Permanently delete <strong>{deleteStreamTarget}</strong> and all its events?
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteStreamTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteStreamTarget && deleteStreamMut.mutate(deleteStreamTarget)}
              disabled={deleteStreamMut.isPending}
            >
              {deleteStreamMut.isPending && <Spinner className="mr-2" />}
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Bulk delete confirm ── */}
      <Dialog open={showBulkDelete} onOpenChange={(v) => !v && setShowBulkDelete(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Delete {selectedStreams.size} Log Stream{selectedStreams.size !== 1 ? "s" : ""}
            </DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-fg-muted">
              Permanently delete the following streams and all their events?
            </p>
            <ul className="mt-3 max-h-40 overflow-y-auto rounded border border-border bg-bg-muted p-2 text-sm">
              {[...selectedStreams].map((name) => (
                <li key={name} className="truncate py-0.5 font-mono text-xs">
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
              onClick={() => bulkDeleteMut.mutate([...selectedStreams])}
              disabled={bulkDeleteMut.isPending}
            >
              {bulkDeleteMut.isPending && <Spinner className="mr-2" />}
              Delete {selectedStreams.size} Stream{selectedStreams.size !== 1 ? "s" : ""}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ─── CreateStreamDialog ───────────────────────────────────────────────────────

const createStreamSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .max(512, "Max 512 characters")
    .regex(/^[a-zA-Z0-9_./#-]+$/, "Letters, numbers, and . _ / # - only"),
})

function CreateStreamDialog({
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
    validators: { onChange: createStreamSchema },
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
          <DialogTitle>Create Log Stream</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          <form.Field name="name" validators={{ onChange: createStreamSchema.shape.name }}>
            {(field) => (
              <FormRow>
                <FormField
                  label="Stream Name"
                  required
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="my-stream"
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
