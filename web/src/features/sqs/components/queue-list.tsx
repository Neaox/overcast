import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { MessagesSquare, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  sqsQueries,
  sqsKeys,
  createQueueMutationOptions,
  deleteQueueMutationOptions,
} from "@/features/sqs/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow } from "@/components/ui/form"
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
import { Badge } from "@/components/ui/badge"
import { useToast } from "@/components/ui/toast"

export function QueueList() {
  const { endpoint } = useEndpoint()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()

  // Create form state
  const [newName, setNewName] = useState("")
  const [newVT, setNewVT] = useState("30")
  const [newRetention, setNewRetention] = useState("345600")

  const {
    data: queues = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(sqsQueries.queues(endpoint.baseUrl))

  const createMut = useMutation({
    ...createQueueMutationOptions(),
    onSuccess: (_, { name }) => {
      qc.invalidateQueries({ queryKey: sqsKeys.queues() })
      setShowCreate(false)
      resetCreateForm()
      toast({ title: "Queue created", description: name, variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteQueueMutationOptions(),
    onSuccess: (_, name) => {
      qc.invalidateQueries({ queryKey: sqsKeys.queues() })
      setDeleteTarget(undefined)
      toast({ title: "Queue deleted", description: name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  function resetCreateForm() {
    setNewName("")
    setNewVT("30")
    setNewRetention("345600")
  }

  function handleCreate() {
    if (!newName.trim()) return
    createMut.mutate({
      name: newName.trim(),
      visibilityTimeout: parseInt(newVT, 10),
      messageRetentionPeriod: parseInt(newRetention, 10),
    })
  }

  return (
    <div className="flex w-full max-w-screen-xl flex-col gap-4">
      <PageHeader
        title="SQS Queues"
        description={`${queues.length} queue${queues.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={`mr-1.5 h-3.5 w-3.5 ${isFetching ? "animate-spin" : ""}`} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Queue
            </Button>
          </>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : queues.length === 0 ? (
        <EmptyState
          icon={<MessagesSquare className="h-10 w-10" />}
          title="No queues yet"
          description="Create a queue to start sending and receiving messages."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Queue
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Queue Name</TableHead>
              <TableHead>Visible</TableHead>
              <TableHead>In-flight</TableHead>
              <TableHead>Visibility Timeout</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {queues.map((q) => (
              <TableRow
                key={q.name}
                className="cursor-pointer"
                onClick={() => navigate({ to: "/sqs/$queue", params: { queue: q.name } })}
              >
                <TableCell className="font-medium">{q.name}</TableCell>
                <TableCell>
                  <Badge variant={q.approximateNumberOfMessages > 0 ? "accent" : "default"}>
                    {q.approximateNumberOfMessages}
                  </Badge>
                </TableCell>
                <TableCell>
                  <Badge
                    variant={q.approximateNumberOfMessagesNotVisible > 0 ? "warning" : "default"}
                  >
                    {q.approximateNumberOfMessagesNotVisible}
                  </Badge>
                </TableCell>
                <TableCell className="text-fg-muted">{q.visibilityTimeout}s</TableCell>
                <TableCell className="max-w-xs truncate font-mono text-xs text-fg-muted">
                  {q.arn}
                </TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget(q.name)
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

      {/* ── Create queue dialog ── */}
      <Dialog
        open={showCreate}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreate(false)
            resetCreateForm()
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Queue</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            <FormField label="Queue Name" required>
              <Input
                placeholder="my-queue"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleCreate()}
                autoFocus
              />
            </FormField>
            <FormRow>
              <FormField label="Visibility Timeout (s)" hint="0–43200">
                <Input
                  type="number"
                  min={0}
                  max={43200}
                  value={newVT}
                  onChange={(e) => setNewVT(e.target.value)}
                />
              </FormField>
              <FormField label="Message Retention (s)" hint="60–1209600">
                <Input
                  type="number"
                  min={60}
                  max={1209600}
                  value={newRetention}
                  onChange={(e) => setNewRetention(e.target.value)}
                />
              </FormField>
            </FormRow>
          </div>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => {
                setShowCreate(false)
                resetCreateForm()
              }}
            >
              Cancel
            </Button>
            <Button onClick={handleCreate} disabled={!newName.trim() || createMut.isPending}>
              {createMut.isPending && <Spinner className="mr-2" />}
              Create Queue
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Delete confirm dialog ── */}
      <Dialog open={!!deleteTarget} onOpenChange={(v) => !v && setDeleteTarget(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Queue</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Permanently delete <strong>{deleteTarget}</strong> and all its messages?
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
    </div>
  )
}
