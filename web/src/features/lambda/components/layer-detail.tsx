/**
 * LayerDetail — shows all versions of a specific Lambda layer.
 * Supports publishing a new version and deleting existing ones.
 * Also shows which functions are attached to this layer.
 */
import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { Trash2, Plus } from "lucide-react"
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
import { Badge } from "@/components/ui/badge"
import { PageHeader, Breadcrumb, Spinner, EmptyState } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { PublishLayerDialog } from "./layer-list"
import type { LayerVersion, LambdaFunction } from "@/types"

// ─── Component ────────────────────────────────────────────────────────────────

export function LayerDetail() {
  const { layerName } = Route.useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showPublish, setShowPublish] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<number | null>(null)

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
      setDeleteTarget(null)
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
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "Lambda", onClick: () => navigate({ to: "/lambda" }) },
              { label: "Layers", onClick: () => navigate({ to: "/lambda/layers" }) },
              { label: layerName },
            ]}
          />
        }
        actions={
          <Button size="sm" onClick={() => setShowPublish(true)}>
            <Plus className="mr-1 h-4 w-4" />
            Publish version
          </Button>
        }
      />

      {/* ── Versions table ─────────────────────────────────────────────── */}
      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-fg">Versions</h2>
        {isLoading ? (
          <div className="flex justify-center py-16">
            <Spinner className="h-5 w-5" />
          </div>
        ) : versions.length === 0 ? (
          <EmptyState
            icon={null}
            title="No versions"
            description="Publish a version to get started."
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Version</TableHead>
                <TableHead>ARN</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Compatible runtimes</TableHead>
                <TableHead>Extensions</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-16" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {versions.map((v) => (
                <VersionRow
                  key={v.Version}
                  layerName={layerName}
                  version={v}
                  onDelete={() => setDeleteTarget(v.Version ?? 0)}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      {/* ── Attached functions ─────────────────────────────────────────── */}
      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-fg">Functions using this layer</h2>
        {attachedFunctions.length === 0 ? (
          <p className="text-sm text-fg-muted">No functions are currently using this layer.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Function name</TableHead>
                <TableHead>Layer version ARN</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {attachedFunctions.map((fn) => (
                <AttachedFunctionRow key={fn.FunctionArn} fn={fn} layerName={layerName} />
              ))}
            </TableBody>
          </Table>
        )}
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

      {deleteTarget !== null && (
        <Dialog open onOpenChange={(open) => !open && setDeleteTarget(null)}>
          <DialogContent className="max-w-sm">
            <DialogHeader>
              <DialogTitle>Delete version {deleteTarget}?</DialogTitle>
            </DialogHeader>
            <p className="text-sm text-fg-muted">
              This will permanently delete version <strong>{deleteTarget}</strong> of layer{" "}
              <strong>{layerName}</strong>. Functions still referencing this version will continue
              to work until their configuration is updated.
            </p>
            <DialogFooter>
              <Button variant="secondary" size="sm" onClick={() => setDeleteTarget(null)}>
                Cancel
              </Button>
              <Button
                variant="danger"
                size="sm"
                disabled={deleteMut.isPending}
                onClick={() => deleteMut.mutate({ layerName, version: deleteTarget })}
              >
                {deleteMut.isPending ? <Spinner className="mr-2 h-3.5 w-3.5" /> : null}
                Delete
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </div>
  )
}

// ─── Version row ──────────────────────────────────────────────────────────────

function VersionRow({
  layerName,
  version: v,
  onDelete,
}: {
  layerName: string
  version: LayerVersion
  onDelete: () => void
}) {
  const versionNumber = v.Version ?? 0
  const { data: metadata, isLoading: metadataLoading, isError: metadataError } = useQuery(
    layerVersionMetadataQueryOptions(layerName, versionNumber),
  )

  return (
    <TableRow>
      <TableCell>
        <Badge variant="default">{v.Version}</Badge>
      </TableCell>
      <TableCell className="font-mono text-xs text-fg-muted">{v.LayerVersionArn}</TableCell>
      <TableCell>{v.Description || "—"}</TableCell>
      <TableCell>
        {(v.CompatibleRuntimes?.length ?? 0) > 0 ? (
          <div className="flex flex-wrap gap-1">
            {v.CompatibleRuntimes!.map((rt) => (
              <Badge key={rt} variant="default">
                {rt}
              </Badge>
            ))}
          </div>
        ) : (
          "—"
        )}
      </TableCell>
      <TableCell>
        {metadataLoading ? (
          <span className="text-sm text-fg-muted">Checking…</span>
        ) : metadataError ? (
          <Badge variant="warning">Metadata unavailable</Badge>
        ) : metadata?.hasExternalExtensions ? (
          <div className="flex flex-wrap gap-1">
            <Badge variant="accent">Lambda extension</Badge>
            {metadata.externalExtensions.map((name) => (
              <Badge key={name} variant="outline" className="font-mono">
                {name}
              </Badge>
            ))}
          </div>
        ) : (
          <span className="text-sm text-fg-muted">—</span>
        )}
      </TableCell>
      <TableCell className="text-sm text-fg-muted">
        {v.CreatedDate ? new Date(v.CreatedDate).toLocaleString() : "—"}
      </TableCell>
      <TableCell>
        <Button
          size="sm"
          variant="ghost"
          className="text-danger hover:text-danger"
          onClick={(e) => {
            e.stopPropagation()
            onDelete()
          }}
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      </TableCell>
    </TableRow>
  )
}

// ─── Attached function row ────────────────────────────────────────────────────

function AttachedFunctionRow({ fn, layerName }: { fn: LambdaFunction; layerName: string }) {
  const navigate = useNavigate()
  const matchingLayer = (fn.Layers ?? []).find((l) =>
    (l.Arn ?? "").includes(`:layer:${layerName}:`),
  )
  return (
    <TableRow
      className="cursor-pointer"
      onClick={() => navigate({ to: "/lambda/$name", params: { name: fn.FunctionName ?? "" } })}
    >
      <TableCell className="font-medium">{fn.FunctionName}</TableCell>
      <TableCell className="font-mono text-xs text-fg-muted">{matchingLayer?.Arn ?? "—"}</TableCell>
    </TableRow>
  )
}
