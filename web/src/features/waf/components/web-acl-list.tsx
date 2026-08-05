import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Eye, ShieldAlert, Trash2 } from "lucide-react"
import { z } from "zod"
import {
  createWebACLMutationOptions,
  deleteWebACLMutationOptions,
  wafKeys,
  webACLsQueryOptions,
} from "../data"
import { MetadataOnlyNotice } from "./metadata-only-notice"
import type { CreateWebACLValues, WebACLSummary, WAFScope } from "@/services/api/waf"
import { topologyKey } from "@/features/map/use-topology"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
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

const schema = z.object({
  name: z.string().min(1, "Web ACL name is required"),
  scope: z.enum(["REGIONAL", "CLOUDFRONT"]),
  description: z.string(),
})

export function WebACLList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<WebACLSummary>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()
  const { data = [], isLoading, isFetching, refetch, error } = useQuery(webACLsQueryOptions())
  const createMutation = useResourceMutation({
    options: createWebACLMutationOptions(),
    invalidateKeys: [wafKeys.webACLs(), topologyKey],
    successTitle: "Web ACL created",
    successDescription: (value) => value.name,
    onSuccess: () => setShowCreate(false),
  })
  const deleteMutation = useResourceMutation({
    options: deleteWebACLMutationOptions(),
    invalidateKeys: [wafKeys.webACLs(), topologyKey],
    successTitle: "Web ACL deleted",
    successDescription: (value) => value.name,
    onSuccess: () => setDeleteTarget(undefined),
  })
  const openACL = (acl: WebACLSummary) =>
    navigate({
      to: "/waf/$scope/$webAclId/$name",
      params: { scope: acl.scope, webAclId: acl.id, name: acl.name },
    })

  return (
    <ResourceListPage
      title="WAF Web ACLs"
      count={data.length}
      actions={
        <>
          <ServiceDocsButton
            service="waf"
            label="WAF"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create Web ACL</CreateAction>
        </>
      }
    >
      <MetadataOnlyNotice />
      <ResourceListCard>
        {isLoading || data.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={data.length === 0}
            error={error}
            errorTitle="Failed to load Web ACLs"
            empty={
              <EmptyState
                icon={<ShieldAlert className="h-10 w-10" />}
                title="No Web ACLs yet"
                description="Create metadata for a regional or CloudFront Web ACL."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create Web ACL</CreateAction>
                }
              />
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead>Description</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.map((acl) => (
                <TableRow key={`${acl.scope}:${acl.id}`} onClick={() => openACL(acl)}>
                  <TableCell>
                    <ResourceName icon={ShieldAlert} name={acl.name} />
                  </TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">{acl.scope}</TableCell>
                  <TableCell className="text-fg-muted">{acl.description || "—"}</TableCell>
                  <TableCell onClick={(event) => event.stopPropagation()}>
                    <RowActions>
                      <RowAction label={`View ${acl.name}`} onClick={() => openACL(acl)}>
                        <Eye className="h-3.5 w-3.5" />
                      </RowAction>
                      <RowAction
                        label={`Delete ${acl.name}`}
                        tone="danger"
                        onClick={() => setDeleteTarget(acl)}
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
      <CreateWebACLDialog
        open={showCreate}
        loading={createMutation.isPending}
        onClose={() => setShowCreate(false)}
        onSubmit={(value) => createMutation.mutate(value)}
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Web ACL?"
        description={
          <>
            Delete metadata for <span className="font-medium text-fg">{deleteTarget?.name}</span>?
          </>
        }
        isPending={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
      />
    </ResourceListPage>
  )
}

function CreateWebACLDialog({
  open,
  loading,
  onClose,
  onSubmit,
}: {
  open: boolean
  loading: boolean
  onClose: () => void
  onSubmit: (value: CreateWebACLValues) => void
}) {
  const form = useForm({
    defaultValues: { name: "", scope: "REGIONAL" as WAFScope, description: "" },
    validators: { onSubmit: schema },
    onSubmit: ({ value }) => onSubmit(value),
  })
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create WAF Web ACL metadata</DialogTitle>
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
                label="Name"
                error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
              >
                <Input
                  autoFocus
                  value={field.state.value}
                  onBlur={field.handleBlur}
                  onChange={(event) => field.handleChange(event.target.value)}
                  placeholder="edge-acl"
                />
              </FormField>
            )}
          </form.Field>
          <form.Field name="scope">
            {(field) => (
              <FormField label="Scope">
                <Select
                  value={field.state.value}
                  onChange={(event) =>
                    field.handleChange(event.target.value as "REGIONAL" | "CLOUDFRONT")
                  }
                >
                  <option value="REGIONAL">Regional</option>
                  <option value="CLOUDFRONT">CloudFront</option>
                </Select>
              </FormField>
            )}
          </form.Field>
          <form.Field name="description">
            {(field) => (
              <FormField label="Description">
                <Input
                  value={field.state.value}
                  onChange={(event) => field.handleChange(event.target.value)}
                  placeholder="Optional metadata description"
                />
              </FormField>
            )}
          </form.Field>
          <p className="text-xs text-fg-muted">
            The ACL is created with an allow default action, no rules, and metrics disabled.
            UpdateWebACL is not implemented.
          </p>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" disabled={loading}>
              Create Web ACL
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
