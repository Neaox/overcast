import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Boxes, Eye } from "lucide-react"
import {
  createRepositoryMutationOptions,
  deleteRepositoryMutationOptions,
  ecrKeys,
  ecrRepositoriesQueryOptions,
} from "@/features/ecr/data"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { formatDate } from "@/lib/format"
import type { EcrRepository } from "@/types"

const schema = z.object({
  name: z
    .string()
    .min(2, "Repository name is required")
    .regex(/^[a-z0-9]+(?:[._/-][a-z0-9]+)*$/, "Use lowercase repo path segments"),
})

interface RepositoryListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function RepositoryList({ sort, onSortChange }: RepositoryListProps = {}) {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<EcrRepository>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: repositories = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(ecrRepositoriesQueryOptions())

  const createMutation = useResourceMutation({
    options: createRepositoryMutationOptions(),
    invalidateKeys: [ecrKeys.repositories()],
    successTitle: "Repository created",
    successDescription: (name) => name,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMutation = useResourceMutation({
    options: deleteRepositoryMutationOptions(),
    invalidateKeys: [ecrKeys.repositories()],
    successTitle: "Repository deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <ResourceListPage
      title="ECR Repositories"
      count={repositories.length}
      actions={
        <>
          <ServiceDocsButton
            service="ecr"
            label="ECR"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create repository</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: repositories, isLoading, error }}
        noun="repositories"
        emptyIcon={Boxes}
        emptyTitle="No repositories yet"
        emptyDescription="Create a repository, then push a local image to populate tags and digests."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create repository</CreateAction>
        }
        errorTitle="Failed to load repositories"
        sort={sort}
        onSortChange={onSortChange}
        // Three columns, all load-bearing — nothing worth hiding.
        columnToggle={false}
        rowKey={(repository) => repository.name}
        onRowClick={(repository) =>
          navigate({
            to: "/ecr/$repositoryName",
            params: { repositoryName: repository.name },
          })
        }
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (repository) => repository.name,
            cell: (repository) => <ResourceName icon={Boxes} name={repository.name} />,
          },
          { header: "URI", cellClassName: "text-fg-muted", cell: (repository) => repository.uri },
          {
            id: "created",
            header: "Created",
            cellClassName: "text-fg-muted",
            sortValue: (repository) => repository.createdAt,
            cell: (repository) => formatDate(repository.createdAt),
          },
        ]}
        rowActions={(repository) => (
          <RowAction
            label={`View ${repository.name}`}
            onClick={() =>
              navigate({
                to: "/ecr/$repositoryName",
                params: { repositoryName: repository.name },
              })
            }
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMutation,
          getId: (repository) => repository.name,
          label: (repository) => repository.name,
          noun: "repository",
          title: "Delete repository?",
          description: (repository) => (
            <>
              Delete <span className="font-medium text-fg">{repository.name}</span> and its tracked
              image metadata? Registry blobs may still exist in the local registry cache.
            </>
          ),
        }}
      />

      <CreateRepositoryDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onSubmit={(name) => createMutation.mutate(name)}
        loading={createMutation.isPending}
      />
    </ResourceListPage>
  )
}

function CreateRepositoryDialog({
  open,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (name: string) => void
  loading: boolean
}) {
  const form = useForm({
    defaultValues: { name: "" },
    validators: { onSubmit: schema },
    onSubmit: ({ value }) => onSubmit(value.name),
  })

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create ECR repository</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            event.stopPropagation()
            void form.handleSubmit()
          }}
        >
          <form.Field name="name">
            {(field) => (
              <FormField
                label="Repository name"
                error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                hint="Examples: my-app, backend/api, worker.latest"
              >
                <Input
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="my-app"
                  autoFocus
                />
              </FormField>
            )}
          </form.Field>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              Create repository
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
