import { useState } from "react"
import { useQuery, queryOptions } from "@tanstack/react-query"
import { Tooltip } from "@/components/ui/tooltip"
import { Link } from "@tanstack/react-router"
import type { FileRouteTypes } from "@/routeTree.gen"
import { BookOpen, ChevronDown, type LucideIcon } from "lucide-react"
import { cn } from "@/lib/utils"
import { health } from "@/services/api"
import { type EmulationTier } from "@/types/common"
import { Spinner } from "@/components/ui/primitives"
import { ServiceDocsModal } from "@/features/docs/service-docs-modal"
import { SERVICES, type ServiceEntry } from "@/lib/service-registry"
import { useFavourites } from "@/hooks/use-favourites"
import { CATALOG, CATALOG_CATEGORY_LABELS, type CatalogEntry } from "@/lib/unsupported-services"

interface ServiceCardDef {
  name: string
  label: string
  to: FileRouteTypes["to"]
  icon: LucideIcon
  color: string
  bgColor: string
  description: string
  /** Filename stem in docs/services/{docKey}.md. Omit if no docs exist. */
  docKey?: string
}

// Tooltip descriptions for each emulation tier.
const TIER_DESCRIPTIONS: Record<EmulationTier, string> = {
  full: "All operations are implemented and behave like real AWS.",
  partial: "Core operations work. Some endpoints return 501 or have limited behaviour.",
  inert:
    "Service accepts requests but operations have no side effects — always returns success without storing state.",
  stub: "Service is registered but all operations return 501 Not Implemented.",
  unsupported: "Not registered in Overcast.",
}

// Badge config keyed by emulation tier.
const TIER_BADGE: Record<EmulationTier, { label: string; className: string } | null> = {
  full: null, // no badge for fully-implemented services
  partial: {
    label: "Partial",
    className: "border-amber-400/40 bg-amber-400/10 text-amber-400",
  },
  inert: {
    label: "Inert",
    className: "border-sky-400/40 bg-sky-400/10 text-sky-400",
  },
  stub: {
    label: "Stub",
    className: "border-border-muted bg-bg-muted text-fg-subtle",
  },
  unsupported: {
    label: "Unsupported",
    className: "border-border-muted bg-bg-muted text-fg-subtle",
  },
}

const ALL_SERVICES: ServiceCardDef[] = Object.entries(
  SERVICES as Record<string, ServiceEntry>,
)
  .filter(([, e]) => e.dashboardCard !== false && e.to != null)
  .map(([name, e]) => ({
    name,
    label: e.dashboardLabel ?? e.label,
    to: e.to as FileRouteTypes["to"],
    icon: e.icon,
    color: e.color,
    bgColor: e.bg,
    description: e.dashboardDescription ?? e.description ?? "",
    ...(e.docKey ? { docKey: e.docKey } : {}),
  }))

export function Dashboard() {
  const { data, isLoading, isError } = useQuery(
    queryOptions({
      queryKey: ["health"],
      queryFn: () => health.check(),
      staleTime: 30_000,
      retry: 2,
    }),
  )

  const enabledSet = new Set(data?.services ?? [])
  const { recentServices, addRecentService } = useFavourites()

  const tierMap = data?.serviceTiers ?? null

  // Partition into supported (any tier except "unsupported") and unsupported.
  // When tierMap is not yet known, treat all as supported.
  const isUnsupportedTier = (name: string) => tierMap !== null && tierMap[name] === "unsupported"

  const supportedServices = ALL_SERVICES.filter((s) => !isUnsupportedTier(s.name))
  const unsupportedServices = ALL_SERVICES.filter((s) => isUnsupportedTier(s.name))

  // recentServices stores the route path (e.g. "/s3") as the key, matching service.to.
  // Recent supported services in recency order (most-recent first).
  const recentSupported = recentServices
    .map((key) => supportedServices.find((s) => s.to === key))
    .filter((s): s is ServiceCardDef => s !== undefined)

  // Never-used supported services sorted alphabetically by label (stable).
  const neverUsedSupported = supportedServices
    .filter((s) => !recentServices.includes(s.to))
    .sort((a, b) => a.label.localeCompare(b.label))

  // Unsupported services sorted alphabetically (pinned to the bottom).
  const unsupportedSorted = [...unsupportedServices].sort((a, b) => a.label.localeCompare(b.label))

  const sortedServices = [...recentSupported, ...neverUsedSupported, ...unsupportedSorted]
    .sort((a, b) => {
      const aEnabled = enabledSet.has(a.name)
      const bEnabled = enabledSet.has(b.name)
      if (aEnabled !== bEnabled) return aEnabled ? -1 : 1
      return 0
    })

  return (
    <div className="mx-auto max-w-7xl py-8">
      <div className="mb-8">
        <h1 className="text-2xl font-semibold text-fg">Dashboard</h1>
        <p className="mt-1 text-sm text-fg-muted">
          AWS service emulator &mdash; select a service to get started.
        </p>
      </div>

      <div className="flex flex-col gap-6">
        {/* Service grid */}
        <div>
          {isLoading ? (
            <div className="flex items-center justify-center py-20">
              <Spinner className="h-6 w-6" />
            </div>
          ) : isError ? (
            <ServiceGrid
              services={sortedServices}
              enabledServices={null}
              tierMap={null}
              onNavigate={addRecentService}
            />
          ) : (
            <ServiceGrid
              services={sortedServices}
              enabledServices={enabledSet}
              tierMap={data?.serviceTiers ?? null}
              onNavigate={addRecentService}
            />
          )}
        </div>

        {/* Other AWS services — collapsible */}
        <OtherServicesSection />
      </div>

      {data && (
        <p className="mt-6 text-center text-xs text-fg-subtle">
          Emulator {data.version} &middot;{" "}
          {ALL_SERVICES.filter((s) => enabledSet.has(s.name)).length} of {ALL_SERVICES.length}{" "}
          services enabled &middot; storage: {data.storage.default}
          {data.storage.serviceOverrides &&
            Object.keys(data.storage.serviceOverrides).length > 0 && (
              <Tooltip
                content={
                  <ul className="space-y-0.5 text-left">
                    {Object.entries(data.storage.serviceOverrides).map(([svc, mode]) => (
                      <li key={svc}>
                        {svc}: {mode}
                      </li>
                    ))}
                  </ul>
                }
              >
                <span className="ml-1 cursor-help border-b border-dotted border-fg-subtle">
                  ({Object.keys(data.storage.serviceOverrides).length} override
                  {Object.keys(data.storage.serviceOverrides).length !== 1 ? "s" : ""})
                </span>
              </Tooltip>
            )}
        </p>
      )}
    </div>
  )
}

function ServiceGrid({
  services,
  enabledServices,
  tierMap,
  onNavigate,
}: {
  services: ServiceCardDef[]
  /** null = unknown (show all as enabled) */
  enabledServices: Set<string> | null
  tierMap: Record<string, EmulationTier> | null
  onNavigate: (key: string) => void
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {services.map((svc) => {
        const enabled = enabledServices === null || enabledServices.has(svc.name)
        const tier: EmulationTier = tierMap?.[svc.name] ?? "full"
        return (
          <ServiceCard
            key={svc.name}
            service={svc}
            enabled={enabled}
            tier={tier}
            onNavigate={onNavigate}
          />
        )
      })}
    </div>
  )
}

function ServiceCard({
  service,
  enabled,
  tier,
  onNavigate,
}: {
  service: ServiceCardDef
  enabled: boolean
  tier: EmulationTier
  onNavigate: (key: string) => void
}) {
  const Icon = service.icon
  const [docsOpen, setDocsOpen] = useState(false)

  const badge = !enabled ? null : TIER_BADGE[tier]
  const isStub = enabled && tier === "stub"

  const card = (
    <div
      className={cn(
        "group relative flex h-full flex-col gap-3 rounded-xl border p-5 transition-all",
        enabled
          ? cn(
              "border-border bg-bg-elevated shadow-sm hover:border-accent/40 hover:shadow-md",
              isStub && "opacity-75",
            )
          : "cursor-not-allowed border-border-muted bg-bg-muted opacity-50",
      )}
    >
      <div className={cn("flex h-10 w-10 items-center justify-center rounded-lg", service.bgColor)}>
        <Icon className={cn("h-5 w-5", !enabled || isStub ? "text-fg-subtle" : service.color)} />
      </div>
      <div>
        <h3 className={cn("text-sm font-semibold", enabled ? "text-fg" : "text-fg-subtle")}>
          {service.label}
        </h3>
        <p
          className={cn(
            "mt-1 text-xs leading-relaxed",
            enabled ? "text-fg-muted" : "text-fg-subtle",
          )}
        >
          {service.description}
        </p>
      </div>
      {!enabled && (
        <span className="absolute top-3 right-3 rounded-full border border-border-muted px-2 py-0.5 text-[10px] font-medium text-fg-subtle">
          Disabled
        </span>
      )}
      {/* Tier badge — fades out on hover if a docs button will appear */}
      {badge && (
        <Tooltip content={TIER_DESCRIPTIONS[tier]} side="top">
          <span
            className={cn(
              "absolute top-3 right-3 cursor-default rounded-full border px-2 py-0.5 text-[10px] font-medium transition-opacity",
              badge.className,
              service.docKey && "group-hover:opacity-0",
            )}
          >
            {badge.label}
          </span>
        </Tooltip>
      )}
      {enabled && service.docKey && (
        <button
          onClick={(e) => {
            e.preventDefault()
            e.stopPropagation()
            setDocsOpen(true)
          }}
          className="absolute top-3 right-3 flex items-center gap-1 rounded-md border border-border px-2 py-1 text-[11px] font-medium text-fg-muted opacity-0 transition-opacity group-hover:opacity-100 hover:bg-bg-muted hover:text-fg"
          title={`View ${service.label} docs`}
        >
          <BookOpen className="h-3 w-3" />
          Docs
        </button>
      )}
    </div>
  )

  return (
    <>
      {enabled ? (
        <Link
          to={service.to}
          onClick={() => onNavigate(service.to)}
          className="rounded-xl focus-visible:outline-accent"
        >
          {card}
        </Link>
      ) : (
        card
      )}
      {service.docKey && (
        <ServiceDocsModal
          service={service.docKey}
          label={service.label}
          open={docsOpen}
          onClose={() => setDocsOpen(false)}
        />
      )}
    </>
  )
}

function OtherServicesSection() {
  const [open, setOpen] = useState(false)

  // Group CATALOG by category
  const byCategory = CATALOG.reduce<Record<string, CatalogEntry[]>>((acc, entry) => {
    ;(acc[entry.category] ??= []).push(entry)
    return acc
  }, {})

  return (
    <div className="mt-2 rounded-xl border border-border-muted">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between px-5 py-3.5 text-left"
      >
        <span className="text-sm font-medium text-fg-muted">
          Other AWS Services
          <span className="ml-1.5 text-xs font-normal text-fg-subtle">({CATALOG.length})</span>
        </span>
        <ChevronDown
          className={cn("h-4 w-4 text-fg-subtle transition-transform", open && "rotate-180")}
        />
      </button>
      {open && (
        <div className="space-y-5 border-t border-border-muted px-5 py-4">
          {Object.entries(byCategory).map(([cat, entries]) => (
            <div key={cat}>
              <p className="mb-2 text-[11px] font-semibold tracking-wider text-fg-subtle uppercase">
                {CATALOG_CATEGORY_LABELS[cat as keyof typeof CATALOG_CATEGORY_LABELS]}
              </p>
              <div className="flex flex-wrap gap-2">
                {entries.map((entry) => (
                  <Link
                    key={entry.id}
                    to="/$service"
                    params={{ service: entry.id }}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-border-muted bg-bg-muted px-2.5 py-1.5 text-xs text-fg-subtle transition-colors hover:border-border hover:bg-bg-elevated hover:text-fg-muted"
                  >
                    {entry.label}
                  </Link>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
