import { useCallback, useEffect, useRef, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import {
  Search,
  Star,
  X,
  ChevronRight,
  Clock,
  Loader2,
  LayoutDashboard,
  BookOpen,
} from "lucide-react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { cva } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { useFavourites } from "@/hooks/use-favourites"
import { useSearch } from "@/hooks/use-search"
import {
  ALL_SERVICES,
  CATEGORY_LABELS,
  CATEGORY_ORDER,
  type ServiceDefinition,
} from "@/lib/nav-services"
import { matchesQuery, type SearchResult } from "@/lib/search"
import { CATALOG, type CatalogEntry } from "@/lib/unsupported-services"
import { Tooltip } from "@/components/ui/tooltip"

// ─── Star toggle variants ──────────────────────────────────────────────────

const starVariants = cva(
  "absolute top-1 right-1 rounded p-1.5 transition-all opacity-0 group-hover:opacity-100",
  {
    variants: {
      active: {
        true: "opacity-100 text-yellow-400 hover:text-yellow-500",
        false: "text-fg-subtle hover:text-fg-muted",
      },
    },
    defaultVariants: { active: false },
  },
)

const starVariantsSmall = cva(
  "ml-0.5 rounded p-0.5 transition-opacity opacity-0 group-hover:opacity-100",
  {
    variants: {
      active: {
        true: "opacity-100 text-yellow-400 hover:text-yellow-500",
        false: "text-fg-subtle hover:text-fg",
      },
    },
    defaultVariants: { active: false },
  },
)

// ─── Types ─────────────────────────────────────────────────────────────────

interface GlobalSearchProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// ─── Keyboard shortcut hook ────────────────────────────────────────────────
// eslint-disable-next-line react-refresh/only-export-components
export function useGlobalSearchShortcut(onOpen: () => void) {
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault()
        onOpen()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [onOpen])
}

// ─── Service card (used in mega menu) ─────────────────────────────────────

function ServiceCard({
  service,
  isFavourite,
  onToggleFavourite,
  onSelect,
}: {
  service: ServiceDefinition
  isFavourite: boolean
  onToggleFavourite: (key: string, e: React.MouseEvent) => void
  onSelect: (service: ServiceDefinition) => void
}) {
  const Icon = service.icon
  return (
    <button
      onClick={() => onSelect(service)}
      className={cn(
        "group relative flex flex-col items-start gap-1 rounded-lg border border-border bg-bg p-3",
        "text-left transition-colors hover:border-accent/50 hover:bg-bg-subtle",
        "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
      )}
    >
      {/* Star toggle — visible on hover or when already favourited; hidden for non-favouritable services */}
      {service.favouritable !== false && (
        <button
          onClick={(e) => onToggleFavourite(service.key, e)}
          className={starVariants({ active: isFavourite })}
          title={isFavourite ? "Remove from sidebar" : "Pin to sidebar"}
          aria-label={isFavourite ? "Remove from sidebar" : "Pin to sidebar"}
        >
          <Star
            className="h-3.5 w-3.5"
            fill={isFavourite ? "currentColor" : "none"}
            strokeWidth={isFavourite ? 0 : 1.5}
          />
        </button>
      )}

      <Icon className={cn("h-5 w-5 shrink-0", service.color)} />
      <span className="text-sm font-medium text-fg">{service.label}</span>
      <span className="line-clamp-1 text-xs text-fg-subtle">{service.description}</span>
    </button>
  )
}

// ─── Mega menu (shown when query is empty) ─────────────────────────────────

function MegaMenu({ onSelectService }: { onSelectService: (service: ServiceDefinition) => void }) {
  const { isFavourite, recentServices, toggleFavourite } = useFavourites()

  function handleToggleFavourite(key: string, e: React.MouseEvent) {
    e.stopPropagation()
    toggleFavourite(key)
  }

  // Recently used — limited to services in ALL_SERVICES
  const recentItems = recentServices
    .map((key) => ALL_SERVICES.find((s) => s.key === key))
    .filter((s): s is ServiceDefinition => s !== undefined)
    .slice(0, 6)

  return (
    <div>
      {/* Recently used */}
      {recentItems.length > 0 && (
        <section className="px-4 py-3">
          <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-fg-subtle">
            <Clock className="h-3 w-3" />
            Recently Used
          </div>
          <div className="grid grid-cols-3 gap-2 sm:grid-cols-4 md:grid-cols-6">
            {recentItems.map((s) => (
              <ServiceCard
                key={s.key}
                service={s}
                isFavourite={isFavourite(s.key)}
                onToggleFavourite={handleToggleFavourite}
                onSelect={onSelectService}
              />
            ))}
          </div>
        </section>
      )}

      {/* Services by category */}
      {CATEGORY_ORDER.map((cat) => {
        const services = ALL_SERVICES.filter((s) => s.category === cat)
        if (services.length === 0) return null
        return (
          <section key={cat} className="px-4 py-3">
            <div className="mb-2 text-xs font-medium text-fg-subtle">{CATEGORY_LABELS[cat]}</div>
            <div className="grid grid-cols-3 gap-2 sm:grid-cols-4 md:grid-cols-5">
              {services.map((s) => (
                <ServiceCard
                  key={s.key}
                  service={s}
                  isFavourite={isFavourite(s.key)}
                  onToggleFavourite={handleToggleFavourite}
                  onSelect={onSelectService}
                />
              ))}
            </div>
          </section>
        )
      })}

      {/* Hint */}
      <div className="border-t border-border px-4 py-3 text-xs text-fg-subtle">
        <Star className="mr-1 inline h-3 w-3 text-yellow-400" fill="currentColor" strokeWidth={0} />
        Star a service to pin it to the sidebar
      </div>
    </div>
  )
}

// ─── Catalog chip (greyed-out entry for unsupported/stub services) ──────────

function CatalogChip({ entry, onSelect }: { entry: CatalogEntry; onSelect: () => void }) {
  return (
    <button
      onClick={onSelect}
      className={cn(
        "group relative flex shrink-0 items-center gap-2 rounded-lg border border-border-muted bg-bg px-3 py-2",
        "opacity-60 transition-colors hover:border-border hover:bg-bg-subtle hover:opacity-80",
        "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
      )}
    >
      <BookOpen className="h-4 w-4 shrink-0 text-fg-subtle" />
      <span className="text-sm font-medium whitespace-nowrap text-fg-muted">{entry.label}</span>
      <span className="shrink-0 rounded-full border border-border-muted px-1.5 py-0.5 text-[10px] text-fg-subtle">
        {entry.tier === "stub" ? "Stub" : "Unsupported"}
      </span>
    </button>
  )
}

// ─── Search results (shown when query is non-empty) ────────────────────────

function ResultRow({
  result,
  isSelected,
  onSelect,
  onPointerMove,
}: {
  result: SearchResult
  isSelected: boolean
  onSelect: (r: SearchResult) => void
  onPointerMove: () => void
}) {
  const service = ALL_SERVICES.find((s) => s.key === result.serviceKey)
  const isDocsResult = result.serviceKey === "/docs"
  const Icon = service?.icon ?? (isDocsResult ? BookOpen : LayoutDashboard)

  return (
    <a
      href={result.href}
      onPointerMove={onPointerMove}
      onClick={(e) => {
        if (
          e.defaultPrevented ||
          e.button !== 0 ||
          e.metaKey ||
          e.ctrlKey ||
          e.shiftKey ||
          e.altKey
        ) {
          return
        }
        e.preventDefault()
        onSelect(result)
      }}
      className={cn(
        "flex w-full items-center gap-3 rounded-md px-3 py-2 text-left transition-colors",
        isSelected ? "bg-accent-muted" : "hover:bg-bg-subtle",
      )}
    >
      <Icon
        className={cn(
          "h-4 w-4 shrink-0",
          service?.color ?? (isDocsResult ? "text-accent" : "text-fg-muted"),
        )}
      />
      <Tooltip
        content={
          <div className="space-y-1">
            <div className="font-medium break-all">{result.label}</div>
            {result.sublabel && (
              <div className="font-mono break-all text-fg-muted">{result.sublabel}</div>
            )}
          </div>
        }
      >
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium text-fg">{result.label}</div>
          {result.sublabel && (
            <div className="truncate text-xs text-fg-subtle">{result.sublabel}</div>
          )}
        </div>
      </Tooltip>
      <span className="shrink-0 rounded bg-bg-muted px-1.5 py-0.5 text-xs text-fg-subtle">
        {result.type}
      </span>
      {isSelected && <ChevronRight className="h-3.5 w-3.5 shrink-0 text-accent" />}
    </a>
  )
}

function SearchResults({
  grouped,
  flat,
  isLoading,
  query,
  selectedIndex,
  onSelect,
  onSetSelectedIndex,
  onSelectService,
  onSelectCatalogEntry,
}: {
  grouped: Map<string, SearchResult[]>
  flat: SearchResult[]
  isLoading: boolean
  query: string
  selectedIndex: number
  onSelect: (r: SearchResult) => void
  onSetSelectedIndex: (i: number) => void
  onSelectService: (service: ServiceDefinition) => void
  onSelectCatalogEntry: (id: string) => void
}) {
  const { isFavourite, toggleFavourite } = useFavourites()

  const matchedServices = ALL_SERVICES.filter((s) =>
    matchesQuery(query, s.label, s.key, s.description),
  )

  const matchedCatalog = CATALOG.filter((e) => matchesQuery(query, e.label, e.id, e.description))

  if (isLoading) {
    return (
      <div className="flex items-center justify-center gap-2 px-4 py-10 text-sm text-fg-subtle">
        <Loader2 className="h-4 w-4 animate-spin" />
        Searching…
      </div>
    )
  }

  if (flat.length === 0 && matchedServices.length === 0 && matchedCatalog.length === 0) {
    return (
      <div className="px-4 py-10 text-center text-sm text-fg-subtle">
        No results for <span className="font-medium text-fg">"{query}"</span>
      </div>
    )
  }

  // Build groups with running total index for keyboard selection
  const sections: Array<{ serviceKey: string; items: SearchResult[]; startIndex: number }> = []
  let runningIndex = 0
  for (const [serviceKey, items] of grouped) {
    sections.push({ serviceKey, items, startIndex: runningIndex })
    runningIndex += items.length
  }

  return (
    <div className="py-2">
      {/* Matching services — horizontal scrollable strip */}
      {(matchedServices.length > 0 || matchedCatalog.length > 0) && (
        <div className="px-4 pb-3">
          <div className="mb-1.5 text-xs font-medium text-fg-subtle">Services</div>
          <div className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {matchedServices.map((s) => {
              const Icon = s.icon
              const isFav = isFavourite(s.key)
              return (
                <button
                  key={s.key}
                  onClick={() => onSelectService(s)}
                  className={cn(
                    "group relative flex shrink-0 items-center gap-2 rounded-lg border border-border bg-bg px-3 py-2",
                    "transition-colors hover:border-accent/50 hover:bg-bg-subtle",
                    "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
                  )}
                >
                  <Icon className={cn("h-4 w-4 shrink-0", s.color)} />
                  <span className="text-sm font-medium whitespace-nowrap text-fg">{s.label}</span>
                  {s.favouritable !== false && (
                    <span
                      role="button"
                      tabIndex={-1}
                      onClick={(e) => {
                        e.stopPropagation()
                        toggleFavourite(s.key)
                      }}
                      className={starVariantsSmall({ active: isFav })}
                    >
                      <Star
                        className="h-3 w-3"
                        fill={isFav ? "currentColor" : "none"}
                        strokeWidth={isFav ? 0 : 1.5}
                      />
                    </span>
                  )}
                </button>
              )
            })}
            {matchedCatalog.map((entry) => (
              <CatalogChip
                key={entry.id}
                entry={entry}
                onSelect={() => onSelectCatalogEntry(entry.id)}
              />
            ))}
          </div>
        </div>
      )}

      {sections.map(({ serviceKey, items, startIndex }) => {
        const service = ALL_SERVICES.find((s) => s.key === serviceKey)
        const isDocsSection = serviceKey === "/docs"
        const Icon = service?.icon ?? (isDocsSection ? BookOpen : LayoutDashboard)
        return (
          <div key={serviceKey} className="px-2 pb-3">
            {/* Group header */}
            <div className="mb-1 flex items-center gap-1.5 px-1 py-1 text-xs font-medium text-fg-subtle">
              <Icon
                className={cn(
                  "h-3.5 w-3.5",
                  service?.color ?? (isDocsSection ? "text-accent" : "text-fg-muted"),
                )}
              />
              {service?.label ?? (isDocsSection ? "Documentation" : serviceKey)}
              <span className="ml-auto rounded-full bg-bg-muted px-1.5 text-fg-subtle">
                {items.length}
              </span>
            </div>
            {items.map((result, i) => (
              <ResultRow
                key={result.id}
                result={result}
                isSelected={selectedIndex === startIndex + i}
                onSelect={onSelect}
                onPointerMove={() => onSetSelectedIndex(startIndex + i)}
              />
            ))}
          </div>
        )
      })}

      {flat.length > 0 && (
        <div className="border-t border-border px-4 pt-2 text-xs text-fg-subtle">
          {flat.length} result{flat.length !== 1 ? "s" : ""}
          <span className="float-right hidden sm:block">
            ↑↓ Navigate · Enter Select · Esc Close
          </span>
        </div>
      )}
    </div>
  )
}

// ─── GlobalSearch dialog ───────────────────────────────────────────────────

export function GlobalSearch({ open, onOpenChange }: GlobalSearchProps) {
  const navigate = useNavigate()
  const { addRecentService } = useFavourites()
  const { query, setQuery, grouped, flat, isLoading, clear } = useSearch()
  const [selectedIndex, setSelectedIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const isSearching = query.trim().length > 0

  // Reset selection when results change (wrapped to avoid synchronous setState lint rule)
  useEffect(() => {
    queueMicrotask(() => setSelectedIndex(0))
  }, [grouped])

  // Focus input when opened
  useEffect(() => {
    if (open) setTimeout(() => inputRef.current?.focus(), 50)
    else clear()
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps -- clear is stable; intentionally only react to open/close transitions

  const handleClose = useCallback(() => {
    onOpenChange(false)
    clear()
  }, [onOpenChange, clear])

  const navigateTo = useCallback(
    (href: string, serviceKey: string) => {
      addRecentService(serviceKey)
      handleClose()
      void navigate({ to: href })
    },
    [navigate, addRecentService, handleClose],
  )

  function handleSelectResult(result: SearchResult) {
    navigateTo(result.href, result.serviceKey)
  }

  function handleSelectService(service: ServiceDefinition) {
    navigateTo(service.to, service.key)
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (!isSearching) return

    if (e.key === "ArrowDown") {
      e.preventDefault()
      setSelectedIndex((i) => Math.min(i + 1, flat.length - 1))
    } else if (e.key === "ArrowUp") {
      e.preventDefault()
      setSelectedIndex((i) => Math.max(i - 1, 0))
    } else if (e.key === "Enter" && flat[selectedIndex]) {
      e.preventDefault()
      handleSelectResult(flat[selectedIndex])
    }
  }

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        {/* Backdrop */}
        <DialogPrimitive.Overlay className="data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0 z-50 bg-black/50 backdrop-blur-sm" />

        {/* Panel — top-aligned, full palette style */}
        <DialogPrimitive.Content
          className={cn(
            "fixed top-[10vh] left-1/2 z-50 w-full max-w-3xl -translate-x-1/2 overflow-hidden",
            "rounded-xl border border-border bg-bg-elevated shadow-2xl",
            "flex max-h-[75vh] flex-col",
            "data-[state=open]:animate-in data-[state=closed]:animate-out",
            "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
            "data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          )}
          onKeyDown={onKeyDown}
        >
          <DialogPrimitive.Title className="sr-only">Global search</DialogPrimitive.Title>

          {/* Search bar */}
          <div className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-3">
            <div className="flex flex-1 items-center gap-3 rounded-xl border border-transparent bg-bg px-3 py-1.5 transition-all focus-within:ring-2 focus-within:ring-accent">
              <Search className="h-4 w-4 shrink-0 text-fg-muted" />
              <input
                ref={inputRef}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search services and resources…"
                className="flex-1 bg-transparent text-sm text-fg placeholder:text-fg-subtle focus-visible:outline-none"
                style={undefined}
                autoComplete="off"
                spellCheck={false}
              />
              {query && (
                <button
                  onClick={() => setQuery("")}
                  className="shrink-0 rounded p-0.5 text-fg-subtle transition-colors hover:text-fg"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
            <kbd className="shrink-0 rounded border border-border bg-bg-muted px-1.5 py-0.5 text-xs text-fg-subtle">
              Esc
            </kbd>
          </div>

          {/* Body */}
          <div className="min-h-0 flex-1 overflow-y-auto">
            {isSearching ? (
              <SearchResults
                grouped={grouped}
                flat={flat}
                isLoading={isLoading}
                query={query}
                selectedIndex={selectedIndex}
                onSelect={handleSelectResult}
                onSetSelectedIndex={setSelectedIndex}
                onSelectService={handleSelectService}
                onSelectCatalogEntry={(id) => {
                  addRecentService("/" + id)
                  handleClose()
                  void navigate({ to: ("/" + id) as "/" })
                }}
              />
            ) : (
              <MegaMenu onSelectService={handleSelectService} />
            )}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

// ─── Search trigger button (used in the header) ────────────────────────────

export function GlobalSearchTrigger({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex h-8 w-64 items-center gap-2 rounded-md border border-border bg-bg-subtle px-2.5 text-sm text-fg-subtle",
        "transition-colors hover:border-accent/50 hover:bg-bg-muted hover:text-fg",
        "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
      )}
    >
      <Search className="h-3.5 w-3.5 shrink-0" />
      <span className="flex-1 text-left">Search…</span>
      <kbd className="shrink-0 rounded border border-border bg-bg-muted px-1.5 py-0.5 text-xs">
        ⌘K
      </kbd>
    </button>
  )
}
