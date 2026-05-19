import { useState, useRef, useCallback, useEffect } from "react"
import { useInfiniteQuery, useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useNavigate } from "@tanstack/react-router"
import {
  Folder,
  File,
  ArrowLeft,
  RefreshCw,
  Trash2,
  Download,
  Upload,
  ChevronRight,
  Eye,
} from "lucide-react"
import { Route } from "@/routes/s3/$bucket/index"
import {
  s3ObjectsQueryOptions,
  s3ObjectMetaQueryOptions,
  s3Keys,
  deleteObjectMutationOptions,
  deleteByPrefixMutationOptions,
  deleteBucketMutationOptions,
} from "@/features/s3/data"
import { s3 } from "@/services/api"
import { uploadStore } from "@/lib/upload-store"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Breadcrumb, Spinner, EmptyState, CodeBlock } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useToast } from "@/components/ui/toast"
import { formatBytes, formatDate, formatStorageClass } from "@/lib/format"
import { BucketTabs } from "./bucket-tabs"
import { cn } from "@/lib/utils"

export function BucketDetail() {
  'use no memo'
  const { bucket } = Route.useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [prefix, setPrefix] = useState("")
  const [selected, setSelected] = useState<string>()
  const [metaTarget, setMetaTarget] = useState<string>()
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [deletePrefixTarget, setDeletePrefixTarget] = useState<string>()
  const [isDragOver, setIsDragOver] = useState(false)
  const [showDeleteBucket, setShowDeleteBucket] = useState(false)
  const dragCounterRef = useRef(0)

  const deleteBucketMutation = useMutation({
    ...deleteBucketMutationOptions(),
    onSuccess: () => {
      toast({ title: `Bucket "${bucket}" deleted` })
      void navigate({ to: "/s3" })
    },
    onError: (err: Error) => {
      toast({ title: "Delete failed", description: err.message, variant: "danger" })
    },
  })

  const openUpload = useCallback(
    (files: File[]) => {
      uploadStore.set(files, prefix)
      void navigate({ to: "/s3/$bucket/upload", params: { bucket } })
    },
    [prefix, bucket, navigate],
  )

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    dragCounterRef.current++
    if (e.dataTransfer.types.includes("Files")) setIsDragOver(true)
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    dragCounterRef.current--
    if (dragCounterRef.current === 0) setIsDragOver(false)
  }, [])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
  }, [])

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault()
      dragCounterRef.current = 0
      setIsDragOver(false)
      const files = Array.from(e.dataTransfer.files)
      if (files.length > 0) openUpload(files)
    },
    [openUpload],
  )

  const { data, isLoading, isFetching, refetch, fetchNextPage, hasNextPage, isFetchingNextPage } =
    useInfiniteQuery(s3ObjectsQueryOptions(bucket, prefix))

  const { data: meta, isLoading: metaLoading } = useQuery({
    ...s3ObjectMetaQueryOptions(bucket, metaTarget ?? ""),
    enabled: !!metaTarget,
  })

  const deleteMutation = useMutation({
    ...deleteObjectMutationOptions(bucket),
    onSuccess: (_, key) => {
      void qc.invalidateQueries({ queryKey: s3Keys.objects() })
      setDeleteTarget(undefined)
      if (selected === key) setSelected(undefined)
      toast({ title: "Object deleted", description: key })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const deletePrefixMutation = useMutation({
    ...deleteByPrefixMutationOptions(bucket),
    onSuccess: (res, prefix) => {
      void qc.invalidateQueries({ queryKey: s3Keys.objects() })
      setDeletePrefixTarget(undefined)
      toast({
        title: "Folder deleted",
        description: `Deleted ${res.deleted} object(s) under ${prefix}`,
      })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  // Build breadcrumb from prefix
  const crumbs = [
    {
      label: bucket,
      onClick: () => {
        setPrefix("")
        setSelected(undefined)
      },
    },
    ...prefix
      .split("/")
      .filter(Boolean)
      .map((seg, i, arr) => ({
        label: seg,
        onClick: () => {
          setPrefix(arr.slice(0, i + 1).join("/") + "/")
          setSelected(undefined)
        },
      })),
  ]

  function navigateInto(folderPrefix: string) {
    setPrefix(folderPrefix)
    setSelected(undefined)
  }

  const prefixes = data?.pages.flatMap((p) => p.prefixes) ?? []
  const objects = data?.pages.flatMap((p) => p.objects) ?? []

  // Flat list of all rows for the virtualizer: folders first, then objects.
  type RowItem =
    | { type: "prefix"; prefix: string }
    | { type: "object"; key: string; size: number; lastModified: string; storageClass: string }

  const allItems: RowItem[] = [
    ...prefixes.map((p) => ({ type: "prefix" as const, prefix: p.prefix })),
    ...objects.map((o) => ({
      type: "object" as const,
      key: o.key,
      size: o.size,
      lastModified: o.lastModified,
      storageClass: o.storageClass,
    })),
  ]

  const scrollRef = useRef<HTMLDivElement>(null)

  const rowVirtualizer = useVirtualizer({
    count: allItems.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 41,
    measureElement: (el) => el.getBoundingClientRect().height,
    overscan: 15,
  })

  // Fetch next page when the user scrolls within 10 rows of the end.
  const virtualItems = rowVirtualizer.getVirtualItems()
  useEffect(() => {
    const last = virtualItems.at(-1)
    if (!last) return
    if (last.index >= allItems.length - 10 && hasNextPage && !isFetchingNextPage) {
      void fetchNextPage()
    }
  }, [virtualItems, allItems.length, hasNextPage, isFetchingNextPage, fetchNextPage])

  return (
    <div
      className="relative flex w-full flex-col gap-4"
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {isDragOver && (
        <div className="pointer-events-none fixed inset-0 z-50 flex items-center justify-center bg-accent/10 backdrop-blur-[2px]">
          <div className="flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-accent bg-bg-elevated/90 px-16 py-12 shadow-xl">
            <Upload className="h-10 w-10 text-accent" />
            <p className="text-lg font-semibold text-accent">Drop files to upload</p>
            <p className="text-sm text-fg-muted">
              to{" "}
              <span className="font-medium text-fg">
                {bucket}
                {prefix ? `/${prefix}` : ""}
              </span>
            </p>
          </div>
        </div>
      )}
      <PageHeader
        title={bucket}
        breadcrumb={
          <Breadcrumb
            items={[{ label: "S3", onClick: () => navigate({ to: "/s3" }) }, ...crumbs.slice(1)]}
          />
        }
        actions={
          <>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button variant="secondary" size="md" onClick={() => navigate({ to: "/s3" })}>
              <ArrowLeft className="h-4 w-4" /> Buckets
            </Button>
            <UploadButton onOpen={openUpload} />
            <span className="mx-1 h-5 w-px bg-border" />
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setShowDeleteBucket(true)}
              title="Delete bucket"
            >
              <Trash2 className="h-4 w-4 text-fg-muted" />
            </Button>
          </>
        }
      />

      <ApplicationOwnershipBanner candidates={[`arn:aws:s3:::${bucket}`, bucket]} />

      <BucketTabs bucket={bucket} active="objects" />

      {/* Breadcrumb inside the table area */}
      {prefix && (
        <div className="flex items-center gap-1 text-sm text-fg-muted">
          <Folder className="h-3.5 w-3.5" />
          {crumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              {i > 0 && <ChevronRight className="h-3 w-3 text-fg-subtle" />}
              <button onClick={c.onClick} className="transition-colors hover:text-fg">
                {c.label}
              </button>
            </span>
          ))}
        </div>
      )}

      {/* Object list — virtual-scrolled for 1000s of items */}
      <div className="overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {isLoading ? (
          <div className="flex items-center justify-center py-16">
            <Spinner className="h-6 w-6" />
          </div>
        ) : allItems.length === 0 ? (
          <div className="py-12 text-center text-sm text-fg-muted">
            <EmptyState
              icon={<Folder className="h-8 w-8" />}
              title="No objects"
              description="Upload an object to get started."
            />
          </div>
        ) : (
          <div ref={scrollRef} className="max-h-[calc(100vh-220px)] overflow-auto">
            <table className="w-full border-collapse text-sm">
              <thead className="sticky top-0 z-10 bg-bg-elevated">
                <tr className="border-b border-border">
                  <th className="h-9 w-full px-3 text-left text-sm font-medium text-fg-muted">
                    Name
                  </th>
                  <th className="h-9 w-[1%] px-3 text-left text-sm font-medium whitespace-nowrap text-fg-muted">
                    Size
                  </th>
                  <th className="h-9 w-[1%] px-3 text-left text-sm font-medium whitespace-nowrap text-fg-muted">
                    Last modified
                  </th>
                  <th className="h-9 w-[1%] px-3 text-left text-sm font-medium whitespace-nowrap text-fg-muted">
                    Storage class
                  </th>
                  <th className="h-9 w-[1%] px-1 whitespace-nowrap" />
                </tr>
              </thead>
              <tbody>
                {rowVirtualizer.getVirtualItems().length > 0 && (
                  <tr>
                    <td colSpan={5} style={{ height: rowVirtualizer.getVirtualItems()[0].start }} />
                  </tr>
                )}
                {rowVirtualizer.getVirtualItems().map((vr) => {
                  const item = allItems[vr.index]
                  const isLastRow = vr.index === allItems.length - 1

                  return (
                    <tr
                      key={vr.key}
                      data-index={vr.index}
                      ref={rowVirtualizer.measureElement}
                      className={cn("cursor-pointer transition-colors hover:bg-bg-subtle", !isLastRow && "border-b border-border-muted", (item.type === "object" && selected === item.key) && "bg-accent-muted")}
                      onClick={() => {
                        if (item.type === "prefix") navigateInto(item.prefix)
                        else setSelected(selected === item.key ? undefined : item.key)
                      }}
                    >
                      {item.type === "prefix" ? (
                        <>
                          <td colSpan={3} className="px-3 py-2">
                            <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
                              <Folder className="h-3.5 w-3.5 shrink-0 text-yellow-400" />
                              <span className="truncate text-accent hover:underline">
                                {item.prefix.slice(prefix.length)}
                              </span>
                            </div>
                          </td>
                          <td className="px-3 py-2 text-sm">
                            <Badge>Folder</Badge>
                          </td>
                          <td className="px-1 py-2">
                            <div className="flex items-center gap-0.5">
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                title="Delete folder"
                                className="hover:text-danger"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  setDeletePrefixTarget(item.prefix)
                                }}
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          </td>
                        </>
                      ) : (
                        <>
                          <td className="max-w-0 px-3 py-2">
                            <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
                              <File className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
                              <span className="truncate">{item.key.slice(prefix.length)}</span>
                            </div>
                          </td>
                          <td className="px-3 py-2 text-sm whitespace-nowrap text-fg-muted">
                            {formatBytes(item.size)}
                          </td>
                          <td className="px-3 py-2 text-sm whitespace-nowrap text-fg-muted">
                            {formatDate(item.lastModified)}
                          </td>
                          <td className="px-3 py-2 text-sm whitespace-nowrap">
                            <Badge variant="default">{formatStorageClass(item.storageClass)}</Badge>
                          </td>
                          <td className="px-1 py-2">
                            <div className="flex items-center gap-0.5">
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                title="Inspect"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  setMetaTarget(item.key)
                                }}
                              >
                                <Eye className="h-3.5 w-3.5" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                title="Download"
                                asChild
                                onClick={(e) => e.stopPropagation()}
                              >
                                <a href={s3.getObjectDownloadUrl(bucket, item.key)} download>
                                  <Download className="h-3.5 w-3.5" />
                                </a>
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                title="Delete"
                                className="hover:text-danger"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  setDeleteTarget(item.key)
                                }}
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          </td>
                        </>
                      )}
                    </tr>
                  )
                })}
                {rowVirtualizer.getVirtualItems().length > 0 && (
                  <tr>
                    <td
                      colSpan={5}
                      style={{
                        height:
                          rowVirtualizer.getTotalSize() -
                          (rowVirtualizer.getVirtualItems().at(-1)?.end ?? 0),
                      }}
                    />
                  </tr>
                )}
              </tbody>
            </table>
            {isFetchingNextPage && (
              <div className="flex items-center justify-center gap-2 border-t border-border py-2 text-sm text-fg-muted">
                <Spinner className="h-3.5 w-3.5" /> Loading more…
              </div>
            )}
          </div>
        )}
      </div>

      {/* Object metadata panel */}
      <Dialog open={!!metaTarget} onOpenChange={(o) => !o && setMetaTarget(undefined)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle className="truncate font-mono text-sm">{metaTarget}</DialogTitle>
          </DialogHeader>
          {metaLoading ? (
            <div className="flex justify-center py-8">
              <Spinner />
            </div>
          ) : meta ? (
            <div className="flex flex-col gap-3">
              <MetaRow label="Content-Type" value={meta.contentType} />
              <MetaRow label="Size" value={formatBytes(meta.contentLength)} />
              <MetaRow label="Last Modified" value={formatDate(meta.lastModified)} />
              <MetaRow label="ETag" value={meta.etag} mono />
              {Object.keys(meta.metadata).length > 0 && (
                <div>
                  <p className="mb-1.5 text-sm font-medium text-fg-muted">User metadata</p>
                  <CodeBlock>{JSON.stringify(meta.metadata, null, 2)}</CodeBlock>
                </div>
              )}
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="secondary" onClick={() => setMetaTarget(undefined)}>
              Close
            </Button>
            {metaTarget && (
              <Button asChild>
                <a href={s3.getObjectDownloadUrl(bucket, metaTarget)} download>
                  <Download className="h-4 w-4" /> Download
                </a>
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <Dialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(undefined)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete object?</DialogTitle>
          </DialogHeader>
          <p className="text-sm break-all text-fg-muted">
            Permanently delete <span className="font-medium text-fg">{deleteTarget}</span>?
          </p>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setDeleteTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleteMutation.isPending}
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget)}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete folder (by prefix) confirmation */}
      <Dialog
        open={!!deletePrefixTarget}
        onOpenChange={(o) => !o && setDeletePrefixTarget(undefined)}
      >
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete folder?</DialogTitle>
          </DialogHeader>
          <p className="text-sm break-all text-fg-muted">
            This will permanently delete all objects under{" "}
            <span className="font-medium text-fg">{deletePrefixTarget}</span>.
          </p>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setDeletePrefixTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deletePrefixMutation.isPending}
              onClick={() => deletePrefixTarget && deletePrefixMutation.mutate(deletePrefixTarget)}
            >
              {deletePrefixMutation.isPending ? "Deleting…" : "Delete all"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete bucket confirmation */}
      <Dialog open={showDeleteBucket} onOpenChange={(o) => !o && setShowDeleteBucket(false)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete bucket?</DialogTitle>
          </DialogHeader>
          <p className="text-sm break-all text-fg-muted">
            <span className="font-medium text-fg">{bucket}</span> will be permanently deleted. The
            bucket must be empty before it can be deleted.
          </p>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setShowDeleteBucket(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleteBucketMutation.isPending}
              onClick={() => deleteBucketMutation.mutate(bucket)}
            >
              {deleteBucketMutation.isPending ? "Deleting…" : "Delete bucket"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function MetaRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex gap-3">
      <span className="w-32 shrink-0 text-sm text-fg-muted">{label}</span>
      <span className={cn("text-sm break-all text-fg", mono && "font-mono")}>{value}</span>
    </div>
  )
}

// ─── Upload button ─────────────────────────────────────────────────────────

function UploadButton({ onOpen }: { onOpen: (files: File[]) => void }) {
  return (
    <Button size="md" asChild>
      <label className="cursor-pointer">
        <Upload className="h-4 w-4" />
        Upload
        <input
          type="file"
          multiple
          className="sr-only"
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            if (files.length) onOpen(files)
            e.target.value = ""
          }}
        />
      </label>
    </Button>
  )
}
