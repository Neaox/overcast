/**
 * LayerList — lists all Lambda layers with their latest version.
 * Supports creating a new layer version via a dialog.
 */
import { useState, useCallback } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Archive,
  Check,
  ChevronLeft,
  ChevronRight,
  Cloud,
  Layers,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react"
import {
  layersQueryOptions,
  publishLayerVersionMutationOptions,
  deleteLayerVersionMutationOptions,
  lambdaKeys,
  lambdaRuntimesQueryOptions,
  layerVersionsQueryOptions,
} from "@/features/lambda/data"
import { Button } from "@/components/ui/button"
import { Combobox } from "@/components/ui/combobox"
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
import { FormField } from "@/components/ui/form"
import { Input } from "@/components/ui/input"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { useToast } from "@/components/ui/toast"
import type { LambdaLayer, LambdaRuntimeInfo } from "@/types"
import { cn } from "@/lib/utils"
import { formatDate } from "@/lib/format"
import {
  StepDot,
  WizardOptionCard,
  ZipDropzone,
  ZipS3SourceFields,
  readZipAsBase64,
} from "./wizard-primitives"

// ─── Component ────────────────────────────────────────────────────────────────

export function LayerList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)

  const { data: layers = [], isLoading, isFetching, refetch } = useQuery(layersQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  const publishMut = useResourceMutation({
    options: publishLayerVersionMutationOptions(),
    invalidateKeys: [lambdaKeys.layers()],
    successTitle: "Layer version published",
    successDescription: (params) => params.layerName,
    errorTitle: "Publish failed",
    onSuccess: (lv) => {
      setShowCreate(false)
      void navigate({
        to: "/lambda/layers/$layerName",
        params: { layerName: extractLayerName(lv.LayerVersionArn ?? "") },
      })
    },
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Lambda Layers"
        description="Manage reusable code libraries attached to Lambda functions."
        actions={
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" disabled={isFetching} onClick={() => refetch()}>
              <RefreshCw className={cn("mr-1 h-4 w-4", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Publish version
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : layers.length === 0 ? (
        <EmptyState
          icon={<Layers className="h-8 w-8 opacity-40" />}
          title="No layers"
          description="Publish a layer version to get started."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Latest version</TableHead>
              <TableHead>Compatible runtimes</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {layers.map((layer) => (
              <LayerRow
                key={layer.LayerArn}
                layer={layer}
                onDelete={() => setDeleteTarget(layer.LayerName ?? "")}
              />
            ))}
          </TableBody>
        </Table>
      )}

      {showCreate && (
        <PublishLayerDialog
          onClose={() => setShowCreate(false)}
          onPublish={(params) => publishMut.mutate(params)}
          isPending={publishMut.isPending}
        />
      )}

      {deleteTarget !== null && (
        <DeleteLayerDialog
          layerName={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={() => {
            setDeleteTarget(null)
          }}
        />
      )}
    </div>
  )
}

// ─── Row ──────────────────────────────────────────────────────────────────────

function LayerRow({ layer, onDelete }: { layer: LambdaLayer; onDelete: () => void }) {
  const navigate = useNavigate()
  const lv = layer.LatestMatchingVersion

  return (
    <TableRow
      className="cursor-pointer"
      onClick={() =>
        navigate({ to: "/lambda/layers/$layerName", params: { layerName: layer.LayerName ?? "" } })
      }
    >
      <TableCell className="font-medium">{layer.LayerName}</TableCell>
      <TableCell>{lv?.Version}</TableCell>
      <TableCell>
        {(lv?.CompatibleRuntimes?.length ?? 0) > 0 ? lv!.CompatibleRuntimes!.join(", ") : "—"}
      </TableCell>
      <TableCell className="text-sm text-fg-muted">{formatDate(lv?.CreatedDate ?? "")}</TableCell>
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

// ─── Publish wizard ───────────────────────────────────────────────────────────

type CodeSource = "zip" | "s3"

interface PublishLayerDialogProps {
  layerName?: string
  onClose: () => void
  onPublish: (params: {
    layerName: string
    description?: string
    zipFile?: string
    compatibleRuntimes?: string[]
    compatibleArchitectures?: string[]
  }) => void
  isPending: boolean
}

function PublishLayerDialog({
  layerName: initialLayerName,
  onClose,
  onPublish,
  isPending,
}: PublishLayerDialogProps) {
  const [step, setStep] = useState(1)

  // Step 1 state
  const [name, setName] = useState(initialLayerName ?? "")
  const [description, setDescription] = useState("")
  const [runtimes, setRuntimes] = useState<string[]>([])

  // Step 2 state
  const [codeSource, setCodeSource] = useState<CodeSource>("zip")
  const [zipFile, setZipFile] = useState<File | null>(null)
  const [s3Bucket, setS3Bucket] = useState("")
  const [s3Key, setS3Key] = useState("")
  const [s3ObjectVersion, setS3ObjectVersion] = useState("")

  const { data: runtimesList = [], isLoading: runtimesLoading } = useQuery(lambdaRuntimesQueryOptions())
  const runtimeItems: LambdaRuntimeInfo[] = runtimesList.filter((rt) => !rt.deprecated)

  const handlePublish = useCallback(async () => {
    let zipBase64: string | undefined
    if (codeSource === "zip" && zipFile) {
      zipBase64 = await readZipAsBase64(zipFile)
    }
    onPublish({
      layerName: name.trim(),
      description: description.trim() || undefined,
      compatibleRuntimes: runtimes.length > 0 ? runtimes : undefined,
      zipFile: codeSource === "zip" ? zipBase64 : undefined, // s3 handled via separate field below (future)
    })
  }, [name, description, runtimes, codeSource, zipFile, onPublish])

  const canAdvance = name.trim().length > 0
  const canPublish =
    (codeSource === "zip" && !!zipFile) ||
    (codeSource === "s3" && !!s3Bucket.trim() && !!s3Key.trim())

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            Publish layer version
            <span className="ml-2 text-xs font-normal text-fg-muted">Step {step} of 2</span>
          </DialogTitle>
        </DialogHeader>

        {/* Step indicator */}
        <div className="flex items-center gap-3 pb-1">
          <StepDot label="Configuration" active={step === 1} done={step > 1} />
          <div className="h-px w-6 bg-border" />
          <StepDot label="Code" active={step === 2} done={false} />
        </div>

        {/* ── Step 1: Configuration ──────────────────────────────────────── */}
        {step === 1 && (
          <div className="flex flex-col gap-4 py-1">
            <FormField label="Layer name" required>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-layer"
                disabled={!!initialLayerName}
                autoFocus={!initialLayerName}
              />
            </FormField>
            <FormField label="Description" hint="Optional">
              <Input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Shared utilities"
              />
            </FormField>
            <FormField
              label="Compatible runtimes"
              hint="Select from the list or type to add a custom value"
            >
              <Combobox<LambdaRuntimeInfo>
                multiple
                isLoading={runtimesLoading}
                value={runtimes}
                onChange={setRuntimes}
                items={runtimeItems}
                filterFn={(rt, q) =>
                  rt.id.toLowerCase().includes(q.toLowerCase()) ||
                  rt.family.toLowerCase().includes(q.toLowerCase())
                }
                getItemValue={(rt) => rt.id}
                renderItem={(rt, { selected, active }) => (
                  <span className="flex items-center justify-between gap-2 px-3 py-1.5">
                    <span className="flex flex-col">
                      <span className="text-sm text-fg">{rt.id}</span>
                      <span className="text-xs text-fg-muted">{rt.family}</span>
                    </span>
                    {selected && (
                      <Check
                        className={cn(
                          "h-3.5 w-3.5 shrink-0",
                          active ? "text-white" : "text-accent",
                        )}
                      />
                    )}
                  </span>
                )}
                allowCustom
                placeholder="e.g. nodejs22.x"
                popoverWidth="w-full"
              />
            </FormField>
          </div>
        )}

        {/* ── Step 2: Code source ────────────────────────────────────────── */}
        {step === 2 && (
          <div className="flex flex-col gap-4 py-1">
            {/* Source selector */}
            <div className="flex gap-2">
              <WizardOptionCard
                active={codeSource === "zip"}
                icon={<Archive className="h-4 w-4" />}
                label="Upload .zip"
                description="Upload a .zip archive"
                onClick={() => setCodeSource("zip")}
              />
              <WizardOptionCard
                active={codeSource === "s3"}
                icon={<Cloud className="h-4 w-4" />}
                label="From S3"
                description="Reference an S3 object"
                onClick={() => setCodeSource("s3")}
              />
            </div>

            {/* Zip upload */}
            {codeSource === "zip" && (
              <ZipDropzone
                file={zipFile}
                onChange={setZipFile}
                description="Select a .zip file containing the layer code"
              />
            )}

            {/* S3 source */}
            {codeSource === "s3" && (
              <ZipS3SourceFields
                bucket={s3Bucket}
                onBucket={setS3Bucket}
                s3Key={s3Key}
                onS3Key={setS3Key}
                objectVersion={s3ObjectVersion}
                onObjectVersion={setS3ObjectVersion}
                bucketPlaceholder="my-layer-bucket"
                keyPlaceholder="layers/my-layer.zip"
              />
            )}
          </div>
        )}

        <DialogFooter>
          {step === 1 && (
            <>
              <Button variant="secondary" size="sm" onClick={onClose}>
                Cancel
              </Button>
              <Button size="sm" disabled={!canAdvance} onClick={() => setStep(2)}>
                Next
                <ChevronRight className="ml-1 h-3.5 w-3.5" />
              </Button>
            </>
          )}
          {step === 2 && (
            <>
              <Button variant="secondary" size="sm" onClick={() => setStep(1)}>
                <ChevronLeft className="mr-1 h-3.5 w-3.5" />
                Back
              </Button>
              <Button size="sm" disabled={isPending || !canPublish} onClick={handlePublish}>
                {isPending ? <Spinner className="mr-2 h-3.5 w-3.5" /> : null}
                Publish
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function extractLayerName(layerVersionArn: string): string {
  // arn:aws:lambda:{region}:{account}:layer:{name}:{version}
  const parts = layerVersionArn.split(":")
  return parts[6] ?? layerVersionArn
}

// ─── Delete layer dialog ──────────────────────────────────────────────────────

function DeleteLayerDialog({
  layerName,
  onClose,
  onDeleted,
}: {
  layerName: string
  onClose: () => void
  onDeleted: () => void
}) {
  const { toast } = useToast()
  const qc = useQueryClient()
  const { data: versions = [], isLoading } = useQuery(layerVersionsQueryOptions(layerName))
  const { mutateAsync: deleteMutateAsync } = useMutation(deleteLayerVersionMutationOptions())
  const [deleting, setDeleting] = useState(false)

  const handleDeleteAll = useCallback(async () => {
    setDeleting(true)
    try {
      for (const v of versions) {
        await deleteMutateAsync({ layerName, version: v.Version ?? 0 })
      }
      void qc.invalidateQueries({ queryKey: lambdaKeys.layers() })
      toast({
        title: "Layer deleted",
        description: `All versions of ${layerName} removed`,
      })
      onDeleted()
    } catch {
      setDeleting(false)
    }
  }, [versions, layerName, deleteMutateAsync, qc, toast, onDeleted])

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Delete layer {layerName}?</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-fg-muted">
          This will permanently delete{" "}
          {isLoading ? (
            "all versions"
          ) : (
            <>
              <strong>{versions.length}</strong> version{versions.length !== 1 ? "s" : ""}
            </>
          )}{" "}
          of layer <strong>{layerName}</strong>. Functions still referencing this layer will need
          their configuration updated.
        </p>
        <DialogFooter>
          <Button variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="danger"
            size="sm"
            disabled={deleting || isLoading || versions.length === 0}
            onClick={handleDeleteAll}
          >
            {deleting ? <Spinner className="mr-2 h-3.5 w-3.5" /> : null}
            Delete all versions
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export { PublishLayerDialog }
