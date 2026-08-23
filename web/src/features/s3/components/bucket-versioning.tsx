/**
 * BucketVersioningPanel — the bucket's versioning state, and the control that
 * changes it.
 *
 * S3 has three states but only two of them can be set: a bucket that has never
 * been versioned can be moved to Enabled or Suspended, and nothing can move it
 * back. That is why the choice offered here is Enabled/Suspended rather than
 * on/off, and why the copy insists that suspending keeps every version already
 * stored — a user who reads "Suspended" as "off" will expect the history to be
 * gone, and it is not.
 */
import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { History } from "lucide-react"
import {
  s3BucketVersioningQueryOptions,
  s3Keys,
  putBucketVersioningMutationOptions,
} from "@/features/s3/data"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { ConfigSection, ConfigRow } from "./config-section"
import { cn } from "@/lib/utils"
import type { S3VersioningStatus } from "@/types"

/** The two states `PutBucketVersioning` accepts. */
type SettableStatus = "Enabled" | "Suspended"

const STATUS_SUMMARY: Record<S3VersioningStatus, string> = {
  "": "Overwriting a key replaces it, and deleting it removes it.",
  Enabled: "Every write keeps the previous version; deleting adds a delete marker.",
  Suspended: "New writes are stored as version “null”. Versions already stored are kept.",
}

const CHOICES: { value: SettableStatus; label: string; detail: string }[] = [
  {
    value: "Enabled",
    label: "Enabled",
    detail:
      "Overwriting a key keeps the previous version, and deleting one adds a delete marker " +
      "rather than removing anything.",
  },
  {
    value: "Suspended",
    label: "Suspended",
    detail:
      "New writes stop creating versions — they are stored with version id “null”, and a " +
      "delete adds a null delete marker. This is not the same as turning versioning off: " +
      "every version already stored is kept, and the bucket goes on serving them until they " +
      "are deleted individually or by a lifecycle rule.",
  },
]

export function BucketVersioningPanel({ bucket }: { bucket: string }) {
  const [editing, setEditing] = useState(false)
  const { data: status } = useQuery(s3BucketVersioningQueryOptions(bucket))

  if (status === undefined) return null

  return (
    <>
      <ConfigSection title="Versioning" icon={<History className="h-4 w-4 text-cat-8" />}>
        <div className="flex flex-col gap-3 px-4 py-3">
          <ConfigRow label="Status">
            <span className="flex flex-wrap items-center gap-2">
              <Badge
                variant={
                  status === "Enabled" ? "success" : status === "Suspended" ? "warning" : "default"
                }
              >
                {status === "" ? "Not enabled" : status}
              </Badge>
              <span className="text-xs text-fg-subtle">{STATUS_SUMMARY[status]}</span>
            </span>
          </ConfigRow>
          <div className="pl-5">
            <Button variant="secondary" size="sm" onClick={() => setEditing(true)}>
              Edit versioning
            </Button>
          </div>
        </div>
      </ConfigSection>

      {/* Mounted only while open so each visit starts from the current state,
          rather than from whatever the last visit left behind. */}
      {editing && (
        <EditVersioningDialog bucket={bucket} current={status} onClose={() => setEditing(false)} />
      )}
    </>
  )
}

function EditVersioningDialog({
  bucket,
  current,
  onClose,
}: {
  bucket: string
  current: S3VersioningStatus
  onClose: () => void
}) {
  const qc = useQueryClient()
  const { toast } = useToast()

  // A bucket that has never been versioned has no state to preselect, so the
  // dialog opens on the only change worth defaulting to.
  const [choice, setChoice] = useState<SettableStatus>(
    current === "Suspended" ? "Suspended" : "Enabled",
  )

  const mut = useMutation({
    ...putBucketVersioningMutationOptions(bucket),
    onSuccess: (_, status) => {
      // The status decides what a listing means, and suspending leaves the
      // history in place, so both listings are refetched either way.
      void qc.invalidateQueries({ queryKey: s3Keys.bucketVersioning(bucket) })
      void qc.invalidateQueries({ queryKey: s3Keys.versions() })
      void qc.invalidateQueries({ queryKey: s3Keys.objects() })
      toast({
        title: status === "Enabled" ? "Versioning enabled" : "Versioning suspended",
        description:
          status === "Enabled"
            ? `${bucket} now keeps a version of every write.`
            : `${bucket} keeps the versions it already has; new writes are not versioned.`,
        variant: "success",
      })
      onClose()
    },
    onError: (err: Error) =>
      toast({ title: "Save failed", description: err.message, variant: "danger" }),
  })

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Bucket versioning</DialogTitle>
        </DialogHeader>

        <p className="text-sm text-fg-muted">
          <span className="font-medium text-fg">{bucket}</span> is currently{" "}
          <span className="font-medium text-fg">
            {current === "" ? "not versioned" : current.toLowerCase()}
          </span>
          . S3 has no way back to the never-versioned state.
        </p>

        <div className="flex flex-col gap-2">
          {CHOICES.map((c) => (
            <label
              key={c.value}
              className={cn(
                "flex cursor-pointer gap-2 rounded-lg border p-3 text-sm",
                choice === c.value
                  ? "border-accent bg-accent/10"
                  : "border-border hover:bg-bg-muted",
              )}
            >
              <input
                type="radio"
                name="versioning-status"
                className="mt-1 self-start accent-accent"
                value={c.value}
                checked={choice === c.value}
                onChange={() => setChoice(c.value)}
              />
              <span className="flex flex-col gap-1">
                <span className="font-medium text-fg">{c.label}</span>
                <span className="text-xs text-fg-muted">{c.detail}</span>
              </span>
            </label>
          ))}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => mut.mutate(choice)} disabled={choice === current || mut.isPending}>
            {mut.isPending && <Spinner className="mr-1.5 h-3.5 w-3.5" />}
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
