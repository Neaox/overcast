/**
 * LayerDetail — shows all versions of a specific Lambda layer.
 * Supports publishing a new version and deleting existing ones.
 * Also shows which functions are attached to this layer.
 */
import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { Plus } from "lucide-react"
import {
  layerVersionsQueryOptions,
  layerVersionMetadataQueryOptions,
  lambdaFunctionsQueryOptions,
  publishLayerVersionMutationOptions,
  deleteLayerVersionMutationOptions,
  lambdaKeys,
} from "@/features/lambda/data"
import { Route } from "@/routes/lambda/layers/$layerName"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { PageHeader } from "@/components/ui/primitives"
import { ResourceTable } from "@/components/ui/resource-table"
import { useToast } from "@/components/ui/toast"
import { PublishLayerDialog } from "./layer-list"
import type { LayerVersion } from "@/types"

// ─── Component ────────────────────────────────────────────────────────────────

export function LayerDetail() {
  const { layerName } = Route.useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showPublish, setShowPublish] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<LayerVersion>()

  const { data: versions = [], isLoading } = useQuery(layerVersionsQueryOptions(layerName))
  const { data: allFunctions = [] } = useQuery(lambdaFunctionsQueryOptions())

  // Functions that have this layer attached
  const attachedFunctions = allFunctions.filter((fn) =>
    (fn.Layers ?? []).some((l) => (l.Arn ?? "").includes(`:layer:${layerName}:`)),
  )

  const publishMut = useMutation({
    ...publishLayerVersionMutationOptions(),
    onSuccess: (lv) => {
      void qc.invalidateQueries({ queryKey: lambdaKeys.layerVersions(layerName) })
      void qc.invalidateQueries({ queryKey: lambdaKeys.layers() })
      setShowPublish(false)
      toast({ title: "Version published", description: `Version ${lv.Version}` })
    },
    onError: (err: Error) =>
      toast({ title: "Publish failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteLayerVersionMutationOptions(),
    onSuccess: (_, { version }) => {
      void qc.invalidateQueries({ queryKey: lambdaKeys.layerVersions(layerName) })
      void qc.invalidateQueries({ queryKey: lambdaKeys.layers() })
      setDeleteTarget(undefined)
      toast({ title: "Version deleted", description: `Version ${version} removed` })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  // Derive the unversioned layer ARN from any version (if available)
  const layerArn =
    versions.length > 0
      ? versions[0].LayerVersionArn?.replace(/:[^:]+$/, "")
      : `arn:aws:lambda:us-east-1:000000000000:layer:${layerName}`

  return (
    <div className="flex flex-col gap-4 p-4 pb-8">
      <PageHeader
        title={layerName}
        description={layerArn}
        actions={
          <Button size="sm" onClick={() => setShowPublish(true)}>
            <Plus className="mr-1 h-4 w-4" />
            Publish version
          </Button>
        }
      />

      {/* ── Versions table ─────────────────────────────────────────────── */}
      <section className="flex flex-col gap-2">
        <h2 className="font-mono text-sm font-semibold text-fg">Versions</h2>
        <ResourceTable
          variant="embedded"
          columnToggle={false}
          query={{ data: versions, isLoading }}
          noun="versions"
          emptyTitle="No versions"
          emptyDescription="Publish a version to get started."
          rowKey={(v) => String(v.Version ?? "")}
          defaultSort={{ id: "version", desc: true }}
          columns={[
            {
              id: "version",
              header: "Version",
              sortValue: (v) => v.Version ?? 0,
              cell: (v) => <Badge variant="default">{v.Version}</Badge>,
            },
            { header: "ARN", cellClassName: "text-fg-muted", cell: (v) => v.LayerVersionArn },
            { header: "Description", prose: true, cell: (v) => v.Description || "—" },
            {
              header: "Compatible runtimes",
              cell: (v) =>
                (v.CompatibleRuntimes?.length ?? 0) > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {v.CompatibleRuntimes!.map((rt) => (
                      <Badge key={rt} variant="default">
                        {rt}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  "—"
                ),
            },
            {
              header: "Extensions",
              // A component, not inline JSX: the extension list comes from a
              // per-version query, and `cell` is a render function that cannot
              // hold hooks of its own.
              cell: (v) => <VersionExtensions layerName={layerName} version={v.Version ?? 0} />,
            },
            {
              header: "Created",
              cellClassName: "text-fg-muted",
              sortValue: (v) => v.CreatedDate,
              cell: (v) => (v.CreatedDate ? new Date(v.CreatedDate).toLocaleString() : "—"),
            },
          ]}
          onDelete={{
            target: deleteTarget,
            onRequest: setDeleteTarget,
            onOpenChange: (open) => !open && setDeleteTarget(undefined),
            mutation: {
              mutate: (version) => deleteMut.mutate({ layerName, version: Number(version) }),
              isPending: deleteMut.isPending,
            },
            getId: (v) => String(v.Version ?? 0),
            label: (v) => `version ${v.Version}`,
            noun: "version",
            title: deleteTarget ? `Delete version ${deleteTarget.Version}?` : undefined,
            description: (v) => (
              <>
                This will permanently delete version <strong>{v.Version}</strong> of layer{" "}
                <strong>{layerName}</strong>. Functions still referencing this version will continue
                to work until their configuration is updated.
              </>
            ),
          }}
        />
      </section>

      {/* ── Attached functions ─────────────────────────────────────────── */}
      <section className="flex flex-col gap-2">
        <h2 className="font-mono text-sm font-semibold text-fg">Functions using this layer</h2>
        <ResourceTable
          variant="embedded"
          columnToggle={false}
          query={{ data: attachedFunctions, isLoading: false }}
          noun="functions"
          emptyTitle="No functions are currently using this layer."
          rowKey={(fn) => fn.FunctionArn ?? fn.FunctionName ?? ""}
          onRowClick={(fn) =>
            navigate({ to: "/lambda/$name", params: { name: fn.FunctionName ?? "" } })
          }
          columns={[
            {
              header: "Function name",
              cellClassName: "font-medium",
              sortValue: (fn) => fn.FunctionName,
              cell: (fn) => fn.FunctionName,
            },
            {
              header: "Layer version ARN",
              cellClassName: "text-fg-muted",
              cell: (fn) =>
                (fn.Layers ?? []).find((l) => (l.Arn ?? "").includes(`:layer:${layerName}:`))
                  ?.Arn ?? "—",
            },
          ]}
        />
      </section>

      {/* ── Dialogs ─────────────────────────────────────────────────────── */}
      {showPublish && (
        <PublishLayerDialog
          layerName={layerName}
          onClose={() => setShowPublish(false)}
          onPublish={(params) => publishMut.mutate(params)}
          isPending={publishMut.isPending}
        />
      )}
    </div>
  )
}

// ─── Version extensions cell ──────────────────────────────────────────────────

/**
 * Whether a layer version ships a Lambda extension. Its own component because
 * the answer comes from a per-version query, and a `ResourceTable` `cell` is a
 * render function that cannot hold hooks.
 */
function VersionExtensions({ layerName, version }: { layerName: string; version: number }) {
  const {
    data: metadata,
    isLoading,
    isError,
  } = useQuery(layerVersionMetadataQueryOptions(layerName, version))

  if (isLoading) return <span className="text-sm text-fg-muted">Checking…</span>
  if (isError) return <Badge variant="warning">Metadata unavailable</Badge>
  if (!metadata?.hasExternalExtensions) return <span className="text-sm text-fg-muted">—</span>

  return (
    <div className="flex flex-wrap gap-1">
      <Badge variant="accent">Lambda extension</Badge>
      {metadata.externalExtensions.map((name) => (
        <Badge key={name} variant="outline" className="font-mono">
          {name}
        </Badge>
      ))}
    </div>
  )
}
