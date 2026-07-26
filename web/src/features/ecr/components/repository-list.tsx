import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Boxes, Eye, Trash2 } from "lucide-react"
import {
  createRepositoryMutationOptions,
  deleteRepositoryMutationOptions,
  ecrKeys,
  ecrRepositoriesQueryOptions,
} from "@/features/ecr/data"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import { formatDate } from "@/lib/format"

const schema = z.object({
  name: z
    .string()
    .min(2, "Repository name is required")
    .regex(/^[a-z0-9]+(?:[._/-][a-z0-9]+)*$/, "Use lowercase repo path segments"),
})

export function RepositoryList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
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
      <ResourceListCard>
        {isLoading || repositories.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={repositories.length === 0}
            error={error}
            errorTitle="Failed to load repositories"
            empty={
              <EmptyState
                icon={<Boxes className="h-10 w-10" />}
                title="No repositories yet"
                description="Create a repository, then push a local image to populate tags and digests."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create repository</CreateAction>
                }
              />
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>URI</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {repositories.map((repository) => (
                <TableRow
                  key={repository.name}
                  onClick={() =>
                    navigate({
                      to: "/ecr/$repositoryName",
                      params: { repositoryName: repository.name },
                    })
                  }
                >
                  <TableCell>
                    <ResourceName icon={Boxes} name={repository.name} />
                  </TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">
                    {repository.uri}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">
                    {formatDate(repository.createdAt)}
                  </TableCell>
                  <TableCell onClick={(event) => event.stopPropagation()}>
                    <RowActions>
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
                      <RowAction
                        label={`Delete ${repository.name}`}
                        tone="danger"
                        onClick={() => setDeleteTarget(repository.name)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </RowAction>
                    </RowActions>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      <CreateRepositoryDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onSubmit={(name) => createMutation.mutate(name)}
        loading={createMutation.isPending}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete repository?"
        description={
          <>
            Delete <span className="font-medium text-fg">{deleteTarget}</span> and its tracked image
            metadata? Registry blobs may still exist in the local registry cache.
          </>
        }
        isPending={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
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
