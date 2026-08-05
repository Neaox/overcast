import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { AlertTriangle, RefreshCw, Trash2 } from "lucide-react"
import { deleteWebACLMutationOptions, wafKeys, webACLQueryOptions } from "../data"
import { MetadataOnlyNotice } from "./metadata-only-notice"
import type { WAFScope } from "@/services/api/waf"
import { topologyKey } from "@/features/map/use-topology"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Badge } from "@/components/ui/badge"
import { CodeBlock, EmptyState, PageHeader, Spinner } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

export function WebACLDetail({ scope, id, name }: { scope: WAFScope; id: string; name: string }) {
  const navigate = useNavigate()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()
  const { data, isLoading, isFetching, refetch, error } = useQuery(
    webACLQueryOptions(scope, id, name),
  )
  const deleteMutation = useResourceMutation({
    options: deleteWebACLMutationOptions(),
    invalidateKeys: [wafKeys.webACLs(), topologyKey],
    successTitle: "Web ACL deleted",
    successDescription: (value) => value.name,
    onSuccess: () => navigate({ to: "/waf" }),
  })

  if (isLoading)
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  if (!data) {
    return (
      <EmptyState
        icon={<AlertTriangle className="h-8 w-8" />}
        title="Web ACL not found"
        description={error instanceof Error ? error.message : `No Web ACL named ${name} exists.`}
        action={<Button onClick={() => navigate({ to: "/waf" })}>Back to Web ACLs</Button>}
      />
    )
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={data.name}
        description={data.arn}
        actions={
          <>
            <ServiceDocsButton
              service="waf"
              label="WAF"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button variant="ghost" size="icon" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="mr-2 h-4 w-4" />
              Delete
            </Button>
          </>
        }
      />
      <MetadataOnlyNotice />
      <div className="grid gap-3 md:grid-cols-3">
        <Info label="Scope">
          <Badge variant="default">{data.scope}</Badge>
        </Info>
        <Info label="Stored rules">
          <span>{data.rules.length}</span>
        </Info>
        <Info label="Lock token">
          <span className="font-mono text-xs break-all">{data.lockToken}</span>
        </Info>
      </div>
      {data.description && (
        <Info label="Description">
          <span>{data.description}</span>
        </Info>
      )}
      <section className="rounded-lg border bg-bg-elevated p-4">
        <h2 className="mb-3 font-mono text-sm font-medium text-fg">Default action</h2>
        <CodeBlock>{JSON.stringify(data.defaultAction, null, 2)}</CodeBlock>
      </section>
      <section className="rounded-lg border bg-bg-elevated p-4">
        <h2 className="mb-3 font-mono text-sm font-medium text-fg">
          Rules (stored, not evaluated)
        </h2>
        <CodeBlock>{JSON.stringify(data.rules, null, 2)}</CodeBlock>
      </section>
      <section className="rounded-lg border bg-bg-elevated p-4">
        <h2 className="mb-3 font-mono text-sm font-medium text-fg">Visibility configuration</h2>
        <CodeBlock>{JSON.stringify(data.visibilityConfig, null, 2)}</CodeBlock>
      </section>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete Web ACL?"
        description={
          <>
            Delete metadata for <span className="font-medium text-fg">{data.name}</span>?
          </>
        }
        isPending={deleteMutation.isPending}
        onConfirm={() => deleteMutation.mutate(data)}
      />
    </div>
  )
}

function Info({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border bg-bg-elevated p-4">
      <div className="font-mono text-xs text-fg-muted">{label}</div>
      <div className="mt-1 text-sm text-fg">{children}</div>
    </div>
  )
}
