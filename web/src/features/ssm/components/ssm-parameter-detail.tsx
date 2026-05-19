import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Settings, RefreshCw, Trash2, Pencil, Eye, EyeOff } from "lucide-react"
import { ArnText } from "@/components/ui/arn-link"
import {
  ssmParameterDetailQueryOptions,
  ssmParameterHistoryQueryOptions,
  ssmKeys,
  putParameterMutationOptions,
  deleteParameterMutationOptions,
} from "@/features/ssm/data"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { PageHeader, Breadcrumb, Spinner, EmptyState, CodeBlock } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useToast } from "@/components/ui/toast"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

interface Props {
  name: string
}

export function SsmParameterDetail({ name }: Props) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()
  const [showEdit, setShowEdit] = useState(false)
  const [showDelete, setShowDelete] = useState(false)
  const [editValue, setEditValue] = useState("")
  const [revealed, setRevealed] = useState(false)

  const {
    data: param,
    isLoading,
    isFetching: paramFetching,
    refetch: refetchParam,
  } = useQuery(ssmParameterDetailQueryOptions(name))

  const {
    data: history = [],
    isFetching: historyFetching,
    refetch: refetchHistory,
  } = useQuery(ssmParameterHistoryQueryOptions(name))

  const updateMut = useMutation({
    ...putParameterMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ssmKeys.parameter(name) })
      void qc.invalidateQueries({ queryKey: ssmKeys.history(name) })
      setShowEdit(false)
      toast({ title: "Parameter updated", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Update failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteParameterMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ssmKeys.parameters() })
      void navigate({ to: "/ssm" })
      toast({ title: "Parameter deleted", description: name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const isFetching = paramFetching || historyFetching

  function handleRefresh() {
    void refetchParam()
    void refetchHistory()
  }

  if (isLoading) {
    return (
      <div className="flex w-full justify-center py-24">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!param) {
    return (
      <div className="flex w-full flex-col gap-4">
        <Breadcrumb
          items={[
            { label: "SSM Parameter Store", onClick: () => navigate({ to: "/ssm" }) },
            { label: name },
          ]}
        />
        <EmptyState
          icon={<Settings className="h-8 w-8 opacity-40" />}
          title="Parameter not found"
          description={`No parameter named "${name}" exists.`}
        />
      </div>
    )
  }

  const isSecure = param.Type === "SecureString"
  const displayValue = isSecure && !revealed ? "••••••••" : param.Value

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={param.Name ?? name}
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "SSM Parameter Store", onClick: () => navigate({ to: "/ssm" }) },
              { label: param.Name ?? name },
            ]}
          />
        }
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
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                setEditValue(param.Value ?? "")
                setShowEdit(true)
              }}
            >
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

      <ApplicationOwnershipBanner candidates={[param.ARN, param.Name, name]} />

      {/* Metadata card */}
      <Card>
        <CardContent className="grid grid-cols-2 gap-x-8 gap-y-3 p-4 text-sm md:grid-cols-3">
          <DetailRow label="Name" value={param.Name} mono />
          <DetailRow label="ARN" value={<ArnText arn={param.ARN ?? ""} />} mono />
          <div className="flex flex-col gap-0.5">
            <span className="text-xs text-fg-muted">Type</span>
            <Badge variant="outline">{param.Type}</Badge>
          </div>
          <DetailRow label="Version" value={`v${param.Version}`} />
          <DetailRow label="Data type" value={param.DataType} />
          <DetailRow label="Last modified" value={formatDate(param.LastModifiedDate)} />
        </CardContent>
      </Card>

      {/* Parameter value */}
      <section className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-fg">Value</h2>
          {isSecure && (
            <Button size="sm" variant="ghost" onClick={() => setRevealed((v) => !v)}>
              {revealed ? (
                <>
                  <EyeOff className="mr-1.5 h-3.5 w-3.5" /> Hide
                </>
              ) : (
                <>
                  <Eye className="mr-1.5 h-3.5 w-3.5" /> Reveal
                </>
              )}
            </Button>
          )}
        </div>
        <CodeBlock>{displayValue ?? ""}</CodeBlock>
      </section>

      {/* Version history */}
      {history.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-medium text-fg">Version history</h2>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Version</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Value</TableHead>
                <TableHead>Modified</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.map((entry) => (
                <TableRow key={entry.Version}>
                  <TableCell className="text-sm">v{entry.Version}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{entry.Type}</Badge>
                  </TableCell>
                  <TableCell className="max-w-xs truncate font-mono text-xs">
                    {entry.Value}
                  </TableCell>
                  <TableCell className="text-sm text-fg-muted">
                    {formatDate(entry.LastModifiedDate)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      )}

      {/* Edit value dialog */}
      <Dialog open={showEdit} onOpenChange={(open) => !open && setShowEdit(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit parameter value</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              updateMut.mutate({ name, value: editValue, type: param.Type ?? "String" })
            }}
          >
            <div className="flex flex-col gap-4 py-2">
              <Input
                value={editValue}
                onChange={(e) => setEditValue(e.target.value)}
                placeholder="New value"
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setShowEdit(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={updateMut.isPending}>
                {updateMut.isPending ? <Spinner className="mr-2 h-3.5 w-3.5" /> : null}
                Save
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={showDelete}
        onOpenChange={(open) => !open && setShowDelete(false)}
        title="Delete Parameter"
        description={
          <>
            Delete parameter <span className="font-mono font-semibold">{name}</span>? This cannot be
            undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(name)}
      />
    </div>
  )
}

function DetailRow({
  label,
  value,
  mono,
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className={cn(mono && "font-mono text-xs break-all")}>{value}</span>
    </div>
  )
}
