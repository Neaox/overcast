import { useState, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Key } from "lucide-react"
import { kmsKeysQueryOptions, kmsKeys, createKeyMutationOptions } from "@/features/kms/data"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow } from "@/components/ui/form"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListFilter,
  ResourceListPage,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { useToast } from "@/components/ui/toast"
import { ArnText } from "@/components/ui/arn-link"

interface KmsPageProps {
  /** Current filter text — owned by the route's `q` search param, see `useFilterSearchParam`. */
  filter: string
  onFilterChange: (value: string) => void
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function KmsPage({ filter, onFilterChange, sort, onSortChange }: KmsPageProps) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()
  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const { data: keys = [], isLoading, isFetching, refetch, error } = useQuery(kmsKeysQueryOptions())

  const createMut = useMutation({
    ...createKeyMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: kmsKeys.keys() })
      setShowCreate(false)
      toast({ title: "Key created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const filtered = useMemo(
    () =>
      filter
        ? keys.filter((k) => (k.KeyId ?? "").toLowerCase().includes(filter.toLowerCase()))
        : keys,
    [keys, filter],
  )

  const form = useForm({
    defaultValues: { description: "" },
    onSubmit: ({ value }) => createMut.mutate(value.description || undefined),
  })

  return (
    <ResourceListPage
      title="KMS"
      description="Customer-managed encryption keys"
      actions={
        <>
          <ServiceDocsButton
            service="kms"
            label="KMS"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create key</CreateAction>
        </>
      }
    >
      <InertBanner serviceName="KMS" />

      <ResourceListFilter value={filter} onChange={onFilterChange} placeholder="Filter keys…" />

      <ResourceTable
        query={{ data: filtered, isLoading, error }}
        noun="keys"
        emptyIcon={Key}
        emptyTitle="No keys"
        emptyDescription="Create a key to get started."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create key</CreateAction>}
        sort={sort}
        onSortChange={onSortChange}
        isFiltered={!!filter}
        onClearFilter={() => onFilterChange("")}
        rowKey={(key) => key.KeyId ?? ""}
        onRowClick={(key) => navigate({ to: "/kms/$keyId", params: { keyId: key.KeyId ?? "" } })}
        columns={[
          {
            header: "Key ID",
            cellClassName: "font-medium",
            sortValue: (key) => key.KeyId,
            cell: (key) => key.KeyId,
          },
          {
            header: "ARN",
            cellClassName: "text-fg-muted",
            cell: (key) => <ArnText arn={key.KeyArn ?? ""} />,
          },
        ]}
      />

      {/* Create key dialog */}
      <Dialog
        open={showCreate}
        onOpenChange={(open) => {
          setShowCreate(open)
          if (!open) form.reset()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create key</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void form.handleSubmit()
            }}
          >
            <div className="flex flex-col gap-4 py-2">
              <form.Field name="description">
                {(field) => (
                  <FormRow>
                    <FormField label="Description (optional)" htmlFor="key-description">
                      <Input
                        id="key-description"
                        placeholder="My encryption key"
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
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
