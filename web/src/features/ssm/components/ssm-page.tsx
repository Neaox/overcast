import { useState, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Settings, Plus, Trash2, RefreshCw, Search } from "lucide-react"
import {
  ssmParametersQueryOptions,
  ssmKeys,
  putParameterMutationOptions,
  deleteParameterMutationOptions,
} from "@/features/ssm/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
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
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { useToast } from "@/components/ui/toast"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

export function SsmPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: parameters = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ssmParametersQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteParameterMutationOptions(),
    invalidateKeys: [ssmKeys.parameters()],
    successTitle: "Parameter deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const createMut = useMutation({
    ...putParameterMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ssmKeys.parameters() })
      setShowCreate(false)
      toast({ title: "Parameter created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const filtered = useMemo(
    () =>
      filter
        ? parameters.filter((p) => (p.Name ?? "").toLowerCase().includes(filter.toLowerCase()))
        : parameters,
    [parameters, filter],
  )

  const form = useForm({
    validators: {
      onChange: z.object({
        name: z.string().min(1, "Required"),
        value: z.string().min(1, "Required"),
        type: z.string(),
      }),
    },
    defaultValues: { name: "", value: "", type: "String" },
    onSubmit: ({ value }) => createMut.mutate(value),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="SSM Parameter Store"
        description="Manage configuration data and secrets"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="ssm"
              label="SSM"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create parameter
            </Button>
          </div>
        }
      />
      <InertBanner serviceName="SSM Parameter Store" />

      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter parameters…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Settings className="h-8 w-8 opacity-40" />}
          title="No parameters"
          description={
            filter ? "No parameters match the filter." : "Create a parameter to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create parameter
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Last modified</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((param) => (
              <TableRow
                key={param.Name}
                className="cursor-pointer"
                onClick={() => navigate({ to: "/ssm/$name", params: { name: param.Name ?? "" } })}
              >
                <TableCell className="font-mono text-sm font-medium">{param.Name}</TableCell>
                <TableCell>
                  <Badge variant="outline">{param.Type}</Badge>
                </TableCell>
                <TableCell className="text-sm text-fg-muted">v{param.Version}</TableCell>
                <TableCell className="text-sm text-fg-muted">
                  {formatDate(param.LastModifiedDate)}
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-danger hover:text-danger"
                    title="Delete"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget(param.Name)
                    }}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* Create parameter dialog */}
      <Dialog
        open={showCreate}
        onOpenChange={(open) => {
          setShowCreate(open)
          if (!open) form.reset()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create parameter</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void form.handleSubmit()
            }}
          >
            <div className="flex flex-col gap-4 py-2">
              <form.Field name="name">
                {(field) => (
                  <FormRow>
                    <FormField
                      label="Parameter name"
                      htmlFor="param-name"
                      error={fieldError(field.state.meta.errors)}
                    >
                      <Input
                        id="param-name"
                        placeholder="/my-app/config/key"
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
              <form.Field name="value">
                {(field) => (
                  <FormRow>
                    <FormField
                      label="Value"
                      htmlFor="param-value"
                      error={fieldError(field.state.meta.errors)}
                    >
                      <Input
                        id="param-value"
                        placeholder="my-value"
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
              <form.Field name="type">
                {(field) => (
                  <FormRow>
                    <FormField label="Type" htmlFor="param-type">
                      <select
                        id="param-type"
                        className="bg-surface w-full rounded border border-border px-3 py-2 text-sm"
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                      >
                        <option value="String">String</option>
                        <option value="StringList">StringList</option>
                        <option value="SecureString">SecureString</option>
                      </select>
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
            </div>
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createMut.isPending}>
                {createMut.isPending ? <Spinner className="mr-2 h-3.5 w-3.5" /> : null}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Parameter"
        description={
          <>
            Delete parameter <span className="font-mono font-semibold">{deleteTarget}</span>? This
            cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
