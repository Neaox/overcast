import { useState, useRef, useCallback, useEffect, useMemo } from "react"
import {
  keepPreviousData,
  useInfiniteQuery,
  useQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query"
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
  Timer,
  History,
  FileX,
  SearchX,
} from "lucide-react"
import { Route } from "@/routes/s3/$bucket/index"
import {
  s3ObjectsQueryOptions,
  s3ObjectVersionsQueryOptions,
  s3ObjectMetaQueryOptions,
  s3BucketLifecycleQueryOptions,
  s3BucketVersioningQueryOptions,
  s3Keys,
  deleteObjectMutationOptions,
  deleteObjectVersionMutationOptions,
  deleteByPrefixMutationOptions,
  deleteBucketMutationOptions,
  type ListScope,
} from "@/features/s3/data"
import {
  DEFAULT_SORT,
  buildRows,
  highlightSlices,
  isServerOrder,
  nextSort,
  splitSearch,
  type NameSlice,
  type ObjectSort,
  type SortColumn,
} from "@/features/s3/object-browser"
import { HighlightedName, ObjectSearchBar, SortHead } from "./object-controls"
import { useDebouncedValue } from "@/hooks/use-debounced-value"
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
import { TableCell, TableHead } from "@/components/ui/table"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useEndpoint } from "@/hooks/use-endpoint"
import { CopyUrlButton } from "@/components/ui/copy-url-button"
import { s3CopyFormats } from "@/features/s3/copy-formats"
import { useToast } from "@/components/ui/toast"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { formatBytes, formatDate, formatStorageClass } from "@/lib/format"
import {
  estimateExpiry,
  formatExpiryDistance,
  type NoncurrentPosition,
  type LifecycleCandidate,
} from "@/features/s3/lifecycle"
import { BucketTabs } from "./bucket-tabs"
import { cn } from "@/lib/utils"
import { ObjectPreviewDialog } from "./object-preview-dialog"
import type { S3LifecycleRule, S3ObjectVersion } from "@/types"

/** Long enough to swallow a burst of typing, short enough to feel live. */
const SEARCH_DEBOUNCE_MS = 200

/**
 * Where an automatic scan stops.
 *
 * A filter or a non-default sort makes the browser read the listing to the end,
 * and a bucket has no end worth waiting for. Stopping here keeps a mistyped
 * search from walking a million keys; the summary line says the view is partial
 * and points at the prefix that would narrow it.
 */
const MAX_SCAN_ENTRIES = 20_000

export function BucketDetail() {
  "use no memo"
  const { bucket } = Route.useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const endpoint = useEndpoint()
  const { toast } = useToast()

  const [prefix, setPrefix] = useState("")
  const [search, setSearch] = useState("")
  const [scope, setScope] = useState<ListScope>("folder")
  const [sort, setSort] = useState<ObjectSort>(DEFAULT_SORT)
  const [metaTarget, setMetaTarget] = useState<string>()
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [deletePrefixTarget, setDeletePrefixTarget] = useState<string>()
  const [isDragOver, setIsDragOver] = useState(false)
  const [showDeleteBucket, setShowDeleteBucket] = useState(false)
  const [showVersions, setShowVersions] = useState(false)
  const [deleteVersionTarget, setDeleteVersionTarget] = useState<S3ObjectVersion>()
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

  // Moving folders drops the search with it. A filter carried into a folder the
  // user only reached by following a breadcrumb reads as an empty folder, and
  // the query that emptied it is two controls away from where they are looking.
  const goToPrefix = useCallback((next: string) => {
    setPrefix(next)
    setSearch("")
  }, [])

  const handleSort = useCallback((column: SortColumn) => {
    setSort((current) => nextSort(current, column))
  }, [])

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

  // Which versioning state the bucket is in decides whether there is anything
  // to show beyond the current objects. A bucket that was never versioned has
  // no history, so the toggle is not offered at all.
  const { data: versioningStatus } = useQuery(s3BucketVersioningQueryOptions(bucket))
  const isVersioned = versioningStatus === "Enabled" || versioningStatus === "Suspended"
  const viewingVersions = showVersions && isVersioned

  // The search box is answered in two places. Everything up to its last "/" is
  // a prefix S3 can filter on for free, so it goes into the request; the rest
  // is a substring match S3 has no API for, so it is applied to what comes back.
  const debouncedSearch = useDebouncedValue(search, SEARCH_DEBOUNCE_MS)
  const { prefixPart, term } = useMemo(() => splitSearch(debouncedSearch), [debouncedSearch])
  const listPrefix = prefix + prefixPart

  // Typing a prefix changes the query key on every keystroke that lands a "/".
  // Keeping the previous page means the table dims and refills rather than
  // collapsing to a spinner and back, which at emulator latencies is the
  // difference between a filter and a flicker.
  const objectsQuery = useInfiniteQuery({
    ...s3ObjectsQueryOptions(bucket, listPrefix, scope),
    enabled: !viewingVersions,
    placeholderData: keepPreviousData,
  })
  const versionsQuery = useInfiniteQuery({
    ...s3ObjectVersionsQueryOptions(bucket, listPrefix, scope),
    enabled: viewingVersions,
    placeholderData: keepPreviousData,
  })
  const {
    data,
    isLoading,
    isFetching,
    isPlaceholderData,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = viewingVersions ? versionsQuery : objectsQuery

  const { data: meta, isLoading: metaLoading } = useQuery({
    ...s3ObjectMetaQueryOptions(bucket, metaTarget ?? ""),
    enabled: !!metaTarget,
  })

  // One request per bucket, not per row: the expiry hint on each object row is
  // computed from these rules rather than a HeadObject apiece.
  const { data: lifecycle } = useQuery(s3BucketLifecycleQueryOptions(bucket))
  const lifecycleRules = lifecycle?.rules ?? []

  const deleteMutation = useMutation({
    ...deleteObjectMutationOptions(bucket),
    onSuccess: (_, key) => {
      void qc.invalidateQueries({ queryKey: s3Keys.objects() })
      setDeleteTarget(undefined)
      toast({ title: "Object deleted", description: key })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const deleteVersionMutation = useMutation({
    ...deleteObjectVersionMutationOptions(bucket),
    onSuccess: (_, { key }) => {
      void qc.invalidateQueries({ queryKey: s3Keys.versions() })
      void qc.invalidateQueries({ queryKey: s3Keys.objects() })
      setDeleteVersionTarget(undefined)
      toast({ title: "Version deleted", description: key })
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
      onClick: () => goToPrefix(""),
    },
    ...prefix
      .split("/")
      .filter(Boolean)
      .map((seg, i, arr) => ({
        label: seg,
        onClick: () => goToPrefix(arr.slice(0, i + 1).join("/") + "/"),
      })),
  ]

  const prefixes = useMemo(() => data?.pages.flatMap((p) => p.prefixes) ?? [], [data])
  const objects = useMemo(
    () => objectsQuery.data?.pages.flatMap((p) => p.objects) ?? [],
    [objectsQuery.data],
  )
  const versions = useMemo(
    () => versionsQuery.data?.pages.flatMap((p) => p.versions) ?? [],
    [versionsQuery.data],
  )

  // Flat list of all rows for the virtualizer: folders first, then either the
  // current objects or the full version history, never both. Memoised because
  // this filters and sorts every loaded entry, which is thousands of them once
  // a scan has run — not something to redo on a hover.
  const allItems = useMemo(
    () =>
      buildRows({
        browsePrefix: prefix,
        listPrefix,
        term,
        sort,
        prefixes,
        objects: viewingVersions ? undefined : objects,
        versions: viewingVersions ? versions : undefined,
      }),
    [prefix, listPrefix, term, sort, prefixes, objects, versions, viewingVersions],
  )

  // How much came back, before filtering — the denominator of the summary line
  // and what the scan cap is measured against.
  const scanned = viewingVersions ? versions.length : objects.length
  const reachedScanCap = scanned >= MAX_SCAN_ENTRIES

  // Both a filter and a non-default sort are answered over rows already in
  // hand, so an unread page is a row that could belong at the top. Neither is
  // truthful until the listing has been read to the end.
  const needsFullListing = term !== "" || !isServerOrder(sort)
  const isScanning = needsFullListing && !!hasNextPage && !reachedScanCap

  useEffect(() => {
    if (!isScanning || isFetchingNextPage) return
    void fetchNextPage()
  }, [isScanning, isFetchingNextPage, fetchNextPage])

  // Offset of the part of each row name the term applies to: the segment before
  // it came from the search's own prefix, which S3 matched exactly.
  const nameOffset = listPrefix.length - prefix.length

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
        actions={
          <>
            <CopyUrlButton
              compact
              formats={s3CopyFormats(endpoint.baseUrl, bucket)}
              noun="bucket URL"
            />
            <Button
              variant="ghost"
              size="icon"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            {isVersioned && (
              <Button
                variant={viewingVersions ? "default" : "ghost"}
                size="icon"
                onClick={() => setShowVersions((v) => !v)}
                title={
                  viewingVersions
                    ? "Show current objects only"
                    : "Show every version and delete marker"
                }
              >
                <History className="h-4 w-4" />
              </Button>
            )}
            <RawStateLink namespace="s3:buckets" stateKey={bucket} />
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

      {isVersioned && (
        <div className="flex items-center gap-2 text-sm text-fg-muted">
          <Badge variant={versioningStatus === "Enabled" ? "success" : "warning"}>
            Versioning {versioningStatus}
          </Badge>
          <span>
            {viewingVersions
              ? "Showing every version and delete marker. Deleting a version here removes it permanently."
              : "Showing current versions only — deleted keys and older versions are hidden."}
          </span>
        </div>
      )}

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

      <ObjectSearchBar
        value={search}
        onChange={setSearch}
        scope={scope}
        onScopeChange={setScope}
        matches={allItems.length}
        scanned={scanned}
        isScanning={isScanning}
        capped={reachedScanCap && !!hasNextPage}
        noun={viewingVersions ? "versions" : "objects"}
      />

      {/* Object list — virtual-scrolled for 1000s of items */}
      <div className="overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {isLoading ? (
          <div className="flex items-center justify-center py-16">
            <Spinner className="h-6 w-6" />
          </div>
        ) : allItems.length === 0 ? (
          <div className="py-12 text-center text-sm text-fg-muted">
            {debouncedSearch.trim() ? (
              <EmptyState
                icon={<SearchX className="h-8 w-8" />}
                title="No matches"
                description={
                  scope === "folder"
                    ? "Nothing in this folder matches. Try searching all nested objects."
                    : "Nothing beneath this prefix matches."
                }
                action={
                  <Button variant="secondary" size="md" onClick={() => setSearch("")}>
                    Clear search
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={<Folder className="h-8 w-8" />}
                title={viewingVersions ? "No versions" : "No objects"}
                description={
                  viewingVersions
                    ? "This prefix has no stored versions or delete markers."
                    : "Upload an object to get started."
                }
              />
            )}
          </div>
        ) : (
          <div
            ref={scrollRef}
            className={cn(
              "max-h-[calc(100vh-220px)] overflow-auto transition-opacity",
              isPlaceholderData && "opacity-60",
            )}
          >
            <table className="w-full border-collapse">
              <thead className="sticky top-0 z-10 bg-bg">
                <tr className="border-b border-border">
                  <SortHead
                    column="name"
                    label="Name"
                    sort={sort}
                    onSort={handleSort}
                    className="w-full"
                  />
                  <SortHead
                    column="size"
                    label="Size"
                    sort={sort}
                    onSort={handleSort}
                    className="w-[1%]"
                  />
                  <SortHead
                    column="modified"
                    label="Last modified"
                    sort={sort}
                    onSort={handleSort}
                    className="w-[1%]"
                  />
                  <TableHead className="w-[1%]">
                    {viewingVersions ? "Version" : "Storage class"}
                  </TableHead>
                  <TableHead className="w-[1%] px-1" />
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
                      className={cn(
                        "transition-colors",
                        !isLastRow && "border-b border-border-muted",
                        // Only folder rows navigate as a whole. On object rows the name
                        // and the action buttons are the click targets, so the row itself
                        // gets neither the pointer nor the interactive hover tint.
                        item.type === "prefix" && "cursor-pointer hover:bg-accent-muted",
                      )}
                      onClick={item.type === "prefix" ? () => goToPrefix(item.prefix) : undefined}
                    >
                      {item.type === "prefix" ? (
                        <>
                          <TableCell colSpan={3}>
                            <div className="flex min-w-0 items-center gap-2">
                              <Folder className="h-3.5 w-3.5 shrink-0 text-yellow-400" />
                              <span className="truncate text-accent hover:underline">
                                <HighlightedName
                                  slices={highlightSlices(item.name, term, nameOffset)}
                                />
                              </span>
                            </div>
                          </TableCell>
                          <TableCell>
                            <Badge>Folder</Badge>
                          </TableCell>
                          <TableCell className="px-1">
                            <div className="flex items-center gap-0.5">
                              <CopyUrlButton
                                compact
                                noun="URL"
                                formats={s3CopyFormats(endpoint.baseUrl, bucket, item.prefix)}
                              />
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
                          </TableCell>
                        </>
                      ) : item.type === "version" ? (
                        <VersionRow
                          bucket={bucket}
                          name={item.name}
                          nameSlices={highlightSlices(item.name, term, nameOffset)}
                          version={item.version}
                          noncurrent={item.noncurrent}
                          rules={lifecycleRules}
                          onInspect={() => setMetaTarget(item.version.key)}
                          onDelete={() => setDeleteVersionTarget(item.version)}
                        />
                      ) : (
                        <>
                          <TableCell className="max-w-0">
                            <div className="flex min-w-0 items-center gap-2">
                              <File className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
                              <button
                                className="truncate text-left text-accent hover:underline"
                                title="Inspect"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  setMetaTarget(item.key)
                                }}
                              >
                                <HighlightedName
                                  slices={highlightSlices(item.name, term, nameOffset)}
                                />
                              </button>
                            </div>
                          </TableCell>
                          <TableCell className="whitespace-nowrap text-fg-muted">
                            {formatBytes(item.size)}
                          </TableCell>
                          <TableCell className="whitespace-nowrap text-fg-muted">
                            {formatDate(item.lastModified)}
                          </TableCell>
                          <TableCell className="whitespace-nowrap">
                            <div className="flex items-center gap-1">
                              <Badge variant="default">
                                {formatStorageClass(item.storageClass)}
                              </Badge>
                              <ExpiryBadge rules={lifecycleRules} object={item} />
                            </div>
                          </TableCell>
                          <TableCell className="px-1">
                            <div className="flex items-center gap-0.5">
                              <CopyUrlButton
                                compact
                                noun="URL"
                                formats={s3CopyFormats(endpoint.baseUrl, bucket, item.key)}
                              />
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
                          </TableCell>
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

      <ObjectPreviewDialog
        bucket={bucket}
        objectKey={metaTarget}
        metadata={meta}
        loading={metaLoading}
        onClose={() => setMetaTarget(undefined)}
      />

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

      {/* Delete version confirmation — this one really removes bytes */}
      <Dialog
        open={!!deleteVersionTarget}
        onOpenChange={(o) => !o && setDeleteVersionTarget(undefined)}
      >
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>
              {deleteVersionTarget?.isDeleteMarker ? "Remove delete marker?" : "Delete version?"}
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm break-all text-fg-muted">
            {deleteVersionTarget?.isDeleteMarker ? (
              <>
                Removing this delete marker restores{" "}
                <span className="font-medium text-fg">{deleteVersionTarget.key}</span> to whatever
                version sits beneath it.
              </>
            ) : (
              <>
                Version{" "}
                <span className="font-medium text-fg">{deleteVersionTarget?.versionId}</span> of{" "}
                <span className="font-medium text-fg">{deleteVersionTarget?.key}</span> will be
                permanently removed. Other versions are untouched.
              </>
            )}
          </p>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setDeleteVersionTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleteVersionMutation.isPending}
              onClick={() =>
                deleteVersionTarget &&
                deleteVersionMutation.mutate({
                  key: deleteVersionTarget.key,
                  versionId: deleteVersionTarget.versionId,
                })
              }
            >
              {deleteVersionMutation.isPending ? "Deleting…" : "Delete"}
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

// ─── Version row ───────────────────────────────────────────────────────────

/**
 * One entry of the version listing.
 *
 * A delete marker is drawn differently from an object on purpose: without that
 * distinction a tombstone and a key that was never there look identical, which
 * is exactly the question the version view exists to answer. Noncurrent
 * versions are dimmed so the current one stands out in a long history.
 *
 * The expiry hint is shown for noncurrent versions only. A current version in a
 * versioned bucket is not deleted when its Expiration rule fires — it gets a
 * delete marker — so an "expires" badge there would say the wrong thing; the
 * object list, which is the view of current versions, carries that hint already.
 */
function VersionRow({
  bucket,
  name,
  nameSlices,
  version,
  noncurrent,
  rules,
  onInspect,
  onDelete,
}: {
  bucket: string
  /** The key relative to the folder being browsed. */
  name: string
  nameSlices: NameSlice[]
  version: S3ObjectVersion
  noncurrent?: NoncurrentPosition
  rules: S3LifecycleRule[]
  onInspect: () => void
  onDelete: () => void
}) {
  return (
    <>
      <TableCell className="max-w-0">
        <div className={cn("flex min-w-0 items-center gap-2", !version.isLatest && "opacity-60")}>
          {version.isDeleteMarker ? (
            <FileX className="h-3.5 w-3.5 shrink-0 text-danger" />
          ) : (
            <File className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
          )}
          {version.isDeleteMarker ? (
            <span className="truncate text-fg-muted line-through" title={name}>
              <HighlightedName slices={nameSlices} />
            </span>
          ) : (
            <button
              className="truncate text-left text-accent hover:underline"
              title="Inspect"
              onClick={(e) => {
                e.stopPropagation()
                onInspect()
              }}
            >
              <HighlightedName slices={nameSlices} />
            </button>
          )}
          {version.isDeleteMarker && <Badge variant="danger">Delete marker</Badge>}
          {version.isLatest ? (
            <Badge variant="success">Current</Badge>
          ) : (
            <Badge variant="default">Noncurrent</Badge>
          )}
        </div>
      </TableCell>
      <TableCell className="whitespace-nowrap text-fg-muted">
        {version.isDeleteMarker ? "—" : formatBytes(version.size)}
      </TableCell>
      <TableCell className="whitespace-nowrap text-fg-muted">
        {formatDate(version.lastModified)}
      </TableCell>
      <TableCell className="whitespace-nowrap">
        <div className="flex items-center gap-1">
          <span className="font-mono text-xs text-fg-muted" title={version.versionId}>
            {version.versionId === "null" ? "null" : version.versionId.slice(0, 8) + "…"}
          </span>
          {!version.isDeleteMarker && version.storageClass !== "STANDARD" && (
            <Badge variant="default">{formatStorageClass(version.storageClass)}</Badge>
          )}
          {noncurrent && (
            <ExpiryBadge
              rules={rules}
              object={{
                key: version.key,
                size: version.size,
                lastModified: version.lastModified,
                noncurrent,
              }}
            />
          )}
        </div>
      </TableCell>
      <TableCell className="px-1">
        <div className="flex items-center gap-0.5">
          {!version.isDeleteMarker && (
            <Button
              variant="ghost"
              size="icon-sm"
              title="Download this version"
              asChild
              onClick={(e) => e.stopPropagation()}
            >
              <a href={s3.getObjectDownloadUrl(bucket, version.key)} download>
                <Download className="h-3.5 w-3.5" />
              </a>
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            title={version.isDeleteMarker ? "Remove delete marker" : "Delete this version"}
            className="hover:text-danger"
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </TableCell>
    </>
  )
}

// ─── Expiry hint ───────────────────────────────────────────────────────────

/**
 * ExpiryBadge — when a lifecycle rule will delete this object.
 *
 * The estimate is computed from the bucket's rules and the listing's own
 * fields. A listing carries no object tags, so a tag-filtered rule cannot be
 * decided here and says so rather than guessing; the object inspector shows
 * the server's authoritative x-amz-expiration instead.
 *
 * A candidate carrying a `noncurrent` position is judged against the rules'
 * NoncurrentVersionExpiration action instead, on the clock that started when
 * its successor was written.
 */
function ExpiryBadge({ rules, object }: { rules: S3LifecycleRule[]; object: LifecycleCandidate }) {
  if (rules.length === 0) return null
  const estimate = estimateExpiry(rules, object)
  const noun = object.noncurrent ? "version" : "object"

  if (estimate.kind === "none") return null
  if (estimate.kind === "unknown") {
    return (
      <Badge variant="default" title={`A tag-filtered lifecycle rule may expire this ${noun}`}>
        <Timer className="mr-1 h-3 w-3" />
        tag rule
      </Badge>
    )
  }
  return (
    <Badge
      variant="warning"
      title={`Lifecycle rule "${estimate.ruleId}" expires this ${noun} at ${estimate.date.toUTCString()}`}
    >
      <Timer className="mr-1 h-3 w-3" />
      expires {formatExpiryDistance(estimate.date)}
    </Badge>
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
