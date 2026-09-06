import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Eye, ShieldAlert } from "lucide-react"
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
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"

const schema = z.object({
  name: z.string().min(1, "Web ACL name is required"),
  scope: z.enum(["REGIONAL", "CLOUDFRONT"]),
  description: z.string(),
})

interface WebACLListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function WebACLList({ sort, onSortChange }: WebACLListProps = {}) {
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
      <ResourceTable
        query={{ data, isLoading, error }}
        noun="Web ACLs"
        emptyIcon={ShieldAlert}
        emptyTitle="No Web ACLs yet"
        emptyDescription="Create metadata for a regional or CloudFront Web ACL."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create Web ACL</CreateAction>
        }
        errorTitle="Failed to load Web ACLs"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(acl) => `${acl.scope}:${acl.id}`}
        onRowClick={openACL}
        columns={[
          {
            header: "Name",
            sortValue: (acl) => acl.name,
            cell: (acl) => <ResourceName icon={ShieldAlert} name={acl.name} />,
          },
          {
            header: "Scope",
            cellClassName: "font-mono text-xs text-fg-muted",
            sortValue: (acl) => acl.scope,
            cell: (acl) => acl.scope,
          },
          {
            header: "Description",
            cellClassName: "text-fg-muted",
            cell: (acl) => acl.description || "—",
          },
        ]}
        rowActions={(acl) => (
          <RowAction label={`View ${acl.name}`} onClick={() => openACL(acl)}>
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          // DeleteWebACL needs the whole summary — it carries the lock token —
          // which is what `getVars` is for.
          mutation: deleteMutation,
          getVars: (acl) => acl,
          label: (acl) => acl.name,
          noun: "Web ACL",
          title: "Delete Web ACL?",
          description: (acl) => (
            <>
              Delete metadata for <span className="font-medium text-fg">{acl.name}</span>?
            </>
          ),
        }}
      />
      <CreateWebACLDialog
        open={showCreate}
        loading={createMutation.isPending}
        onClose={() => setShowCreate(false)}
        onSubmit={(value) => createMutation.mutate(value)}
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
