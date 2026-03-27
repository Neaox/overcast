import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { HardDrive, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  s3Queries,
  s3Keys,
  createBucketMutationOptions,
  deleteBucketMutationOptions,
} from "@/features/s3/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField } from "@/components/ui/form"
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
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { formatBytes, formatDate } from "@/lib/format"

export function BucketList() {
  const { endpoint } = useEndpoint()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()

  const {
    data: buckets = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(s3Queries.buckets(endpoint.baseUrl))

  const createMutation = useMutation({
    ...createBucketMutationOptions(),
    onSuccess: (_, { name }) => {
      qc.invalidateQueries({ queryKey: s3Keys.buckets() })
      setShowCreate(false)
      toast({ title: "Bucket created", description: name, variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteMutation = useMutation({
    ...deleteBucketMutationOptions(),
    onSuccess: (_, name) => {
      qc.invalidateQueries({ queryKey: s3Keys.buckets() })
      setDeleteTarget(undefined)
      toast({ title: "Bucket deleted", description: name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  return (
    <div className="flex w-full max-w-screen-xl flex-col gap-4">
      <PageHeader
        title="S3 Buckets"
        description={`${buckets.length} bucket${buckets.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={`h-4 w-4 ${isFetching ? "animate-spin" : ""}`} />
            </Button>
            <Button size="md" onClick={() => setShowCreate(true)}>
              <Plus className="h-4 w-4" /> New bucket
            </Button>
          </>
        }
      />

      <div className="overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {isLoading ? (
          <div className="flex items-center justify-center py-16">
            <Spinner className="h-6 w-6" />
          </div>
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
              {buckets.length === 0 ? (
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
              ) : (
                buckets.map((b) => (
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
                ))
              )}
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
      <Dialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(undefined)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete bucket?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            This will permanently delete <span className="font-medium text-fg">{deleteTarget}</span>{" "}
            and all objects inside it. This cannot be undone.
          </p>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setDeleteTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ─── Create bucket dialog ──────────────────────────────────────────────────

const BUCKET_RE = /^[a-z0-9][a-z0-9\-.]{1,61}[a-z0-9]$/

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
  const [name, setName] = useState("")
  const [region, setRegion] = useState(defaultRegion)
  const [nameErr, setNameErr] = useState<string>()

  function validate() {
    if (!name) return setNameErr("Name is required")
    if (!BUCKET_RE.test(name))
      return setNameErr("Must be 3–63 lowercase chars, numbers, hyphens or dots")
    setNameErr(undefined)
    return true
  }

  function handleSubmit() {
    if (validate() !== true) return
    onSubmit(name, region)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create bucket</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <FormField label="Bucket name" htmlFor="bname" required error={nameErr}>
            <Input
              id="bname"
              value={name}
              onChange={(e) => {
                setName(e.target.value.toLowerCase())
                setNameErr(undefined)
              }}
              placeholder="my-bucket"
              spellCheck={false}
            />
          </FormField>
          <FormField label="Region" htmlFor="bregion">
            <RegionSelect id="bregion" value={region} onChange={setRegion} />
          </FormField>
        </div>
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={loading}>
            {loading ? "Creating…" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// Keep unused import warning away
void formatBytes
