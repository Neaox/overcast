import { useState, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Settings } from "lucide-react"
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
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListFilter,
  ResourceListPage,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { useToast } from "@/components/ui/toast"
import { formatDate } from "@/lib/format"

interface SsmPageProps {
  /** Current filter text — owned by the route's `q` search param, see `useFilterSearchParam`. */
  filter: string
  onFilterChange: (value: string) => void
}

export function SsmPage({ filter, onFilterChange }: SsmPageProps) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()
  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: parameters = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(ssmParametersQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof parameters)[number]>()

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
    <ResourceListPage
      title="SSM Parameter Store"
      description="Manage configuration data and secrets"
      actions={
        <>
          <ServiceDocsButton
            service="ssm"
            label="SSM"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create parameter</CreateAction>
        </>
      }
    >
      <InertBanner serviceName="SSM Parameter Store" />

      <ResourceListFilter
        value={filter}
        onChange={onFilterChange}
        placeholder="Filter parameters…"
      />

      <ResourceTable
        query={{ data: filtered, isLoading, error }}
        noun="parameters"
        emptyIcon={Settings}
        emptyTitle="No parameters"
        emptyDescription="Create a parameter to get started."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create parameter</CreateAction>
        }
        isFiltered={!!filter}
        onClearFilter={() => onFilterChange("")}
        rowKey={(param) => param.Name ?? ""}
        onRowClick={(param) => navigate({ to: "/ssm/$name", params: { name: param.Name ?? "" } })}
        columns={[
          { header: "Name", cellClassName: "font-medium", cell: (param) => param.Name },
          {
            header: "Type",
            cell: (param) => <Badge variant="outline">{param.Type}</Badge>,
          },
          {
            header: "Version",
            cellClassName: "text-fg-muted",
            cell: (param) => `v${param.Version}`,
          },
          {
            header: "Last modified",
            cellClassName: "text-fg-muted",
            cell: (param) => formatDate(param.LastModifiedDate),
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (param) => param.Name ?? "",
          label: (param) => param.Name ?? "",
          noun: "parameter",
          title: "Delete Parameter",
          description: (param) => (
            <>
              Delete parameter <span className="font-mono font-semibold">{param.Name}</span>? This
              cannot be undone.
            </>
          ),
        }}
      />

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
                        className="w-full rounded border border-border bg-bg-elevated px-3 py-2 text-sm"
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
    </ResourceListPage>
  )
}
