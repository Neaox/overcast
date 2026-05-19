import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { RefreshCw, Trash2, Eye, EyeOff, Pencil, KeyRound } from "lucide-react"
import { ArnText } from "@/components/ui/arn-link"
import {
  secretDetailQueryOptions,
  secretValueQueryOptions,
  smKeys,
  updateSecretValueMutationOptions,
  deleteSecretMutationOptions,
} from "@/features/secretsmanager/data"
import { useToast } from "@/components/ui/toast"
import { Button } from "@/components/ui/button"
import { PageHeader, Breadcrumb, Spinner, EmptyState, CodeBlock } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Card, CardContent } from "@/components/ui/card"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

interface Props {
  secretName: string
}

export function SecretDetail({ secretName }: Props) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showEdit, setShowEdit] = useState(false)
  const [showDelete, setShowDelete] = useState(false)
  const [editValue, setEditValue] = useState("")
  const [revealed, setRevealed] = useState(false)

  // ─── Queries ────────────────────────────────────────────────────────────────

  const {
    data: secret,
    isLoading,
    isFetching: secretFetching,
    refetch: refetchSecret,
  } = useQuery(secretDetailQueryOptions(secretName))

  const {
    data: secretValue,
    isFetching: valueFetching,
    refetch: refetchValue,
  } = useQuery({
    ...secretValueQueryOptions(secretName),
    enabled: revealed,
  })

  // ─── Mutations ──────────────────────────────────────────────────────────────

  const updateMut = useMutation({
    ...updateSecretValueMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: smKeys.secretValue(secretName) })
      setShowEdit(false)
      toast({ title: "Secret value updated", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Update failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteSecretMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: smKeys.secrets() })
      void navigate({ to: "/secretsmanager" })
      toast({ title: "Secret deleted", description: secretName })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  // ─── Helpers ────────────────────────────────────────────────────────────────

  async function handleOpenEdit() {
    setEditValue(secretValue?.SecretString ?? "")
    if (!secretValue) {
      // Fetch value first so the textarea is pre-populated
      try {
        const result = await qc.fetchQuery(secretValueQueryOptions(secretName))
        setEditValue(result.SecretString ?? "")
      } catch (err) {
        toast({
          title: "Failed to load secret value",
          description: (err as Error).message,
          variant: "danger",
        })
        return
      }
    }
    setShowEdit(true)
  }

  function handleRefresh() {
    void refetchSecret()
    if (revealed) void refetchValue()
  }

  const isFetching = secretFetching || (revealed && valueFetching)

  // ─── Loading / not found ────────────────────────────────────────────────────

  if (isLoading) {
    return (
      <div className="flex w-full justify-center py-24">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!secret) {
    return (
      <div className="flex w-full flex-col gap-4">
        <Breadcrumb
          items={[
            { label: "Secrets Manager", onClick: () => navigate({ to: "/secretsmanager" }) },
            { label: secretName },
          ]}
        />
        <EmptyState
          icon={<KeyRound className="h-8 w-8 opacity-40" />}
          title="Secret not found"
          description={`No secret named "${secretName}" exists.`}
        />
      </div>
    )
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={secret.Name ?? secretName}
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "Secrets Manager", onClick: () => navigate({ to: "/secretsmanager" }) },
              { label: secret.Name ?? secretName },
            ]}
          />
        }
        description={secret.Description}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={handleRefresh}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" variant="secondary" onClick={handleOpenEdit}>
              <Pencil className="mr-1.5 h-3.5 w-3.5" />
              Edit value
            </Button>
            <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete
            </Button>
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[secret.ARN, secret.Name, secretName]} />

      {/* Metadata card */}
      <Card>
        <CardContent className="grid grid-cols-2 gap-x-8 gap-y-3 p-4 text-sm md:grid-cols-3">
          <DetailRow
            label="ARN"
            value={secret.ARN != null ? <ArnText arn={secret.ARN} /> : "—"}
            mono
          />
          <DetailRow label="Created" value={formatDate(secret.CreatedDate)} />
          <DetailRow label="Last changed" value={formatDate(secret.LastChangedDate)} />
          <DetailRow label="Last accessed" value={formatDate(secret.LastAccessedDate)} />
          <DetailRow label="Rotation" value={secret.RotationEnabled ? "Enabled" : "Disabled"} />
        </CardContent>
      </Card>

      {/* Secret value */}
      <section className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-fg">Secret value</h2>
          <div className="flex items-center gap-2">
            {revealed && (
              <Button size="sm" variant="secondary" onClick={handleOpenEdit}>
                <Pencil className="mr-1.5 h-3.5 w-3.5" />
                Edit
              </Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => setRevealed((v) => !v)}>
              {revealed ? (
                <>
                  <EyeOff className="mr-1.5 h-3.5 w-3.5" />
                  Hide
                </>
              ) : (
                <>
                  <Eye className="mr-1.5 h-3.5 w-3.5" />
                  Reveal
                </>
              )}
            </Button>
          </div>
        </div>
        <div className="relative rounded-md border border-border">
          {revealed ? (
            valueFetching ? (
              <div className="flex justify-center py-8">
                <Spinner className="h-5 w-5" />
              </div>
            ) : (
              <CodeBlock className="max-h-96 overflow-y-auto text-xs">
                {secretValue?.SecretString ?? secretValue?.SecretBinary ?? "(empty)"}
              </CodeBlock>
            )
          ) : (
            <div className="flex items-center gap-2 px-4 py-3 text-sm text-fg-muted">
              <span className="font-mono tracking-widest">••••••••••••••••••••</span>
            </div>
          )}
        </div>
      </section>

      {/* Edit dialog */}
      <Dialog open={showEdit} onOpenChange={setShowEdit}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Edit value — <span className="font-mono text-sm">{secret.Name}</span>
            </DialogTitle>
          </DialogHeader>
          <textarea
            className="flex min-h-40 w-full rounded-md border border-border bg-bg px-3 py-2 font-mono text-sm placeholder:text-fg-subtle focus-visible:ring-1 focus-visible:outline-none"
            placeholder='{"username":"admin","password":"s3cret"}'
            value={editValue}
            onChange={(e) => setEditValue(e.target.value)}
          />
          <DialogFooter>
            <Button variant="ghost" onClick={() => setShowEdit(false)}>
              Cancel
            </Button>
            <Button
              disabled={updateMut.isPending}
              onClick={() => updateMut.mutate({ secretId: secretName, secretString: editValue })}
            >
              {updateMut.isPending ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <ConfirmDialog
        open={showDelete}
        onOpenChange={setShowDelete}
        title="Delete secret"
        description={
          <>
            Permanently delete <strong>{secretName}</strong>? This cannot be undone.
          </>
        }
        confirmLabel="Delete secret"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(secretName)}
      />
    </div>
  )
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function DetailRow({
  label,
  value,
  mono = false,
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs text-fg-muted">{label}</dt>
      <dd className={cn("text-sm", mono ? "font-mono text-xs" : "text-fg")}>{value}</dd>
    </div>
  )
}
