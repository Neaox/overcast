import { useQuery, queryOptions } from "@tanstack/react-query"
import { Tooltip } from "@/components/ui/tooltip"
import { Spinner } from "@/components/ui/primitives"
import { health } from "@/services/api"
import { useFavourites } from "@/hooks/use-favourites"
import { useLocalStorage } from "@/hooks/use-local-storage"
import type { EmulationTier, HealthResponse } from "@/types/common"
import { ALL_SERVICES, type ServiceTierEntry } from "./service-defs"
import { AvailableServiceCard } from "./components/available-service-card"
import { DashboardHeader, type DashboardView } from "./components/dashboard-header"
import { DashboardSection } from "./components/dashboard-section"
import { NotEmulatedChips } from "./components/not-emulated-chips"
import { ServiceCard } from "./components/service-card"
import { ServiceListView } from "./components/service-list-view"

export const DASHBOARD_VIEW_STORAGE_KEY = "overcast.dashboard.view"

const healthQueryOptions = queryOptions({
  queryKey: ["health"],
  queryFn: () => health.check(),
  staleTime: 30_000,
  retry: 2,
})

export function Dashboard() {
  const { data, isLoading } = useQuery(healthQueryOptions)
  const { recentServices, addRecentService } = useFavourites()
  const [view, setView] = useLocalStorage<DashboardView>(DASHBOARD_VIEW_STORAGE_KEY, "grid")

  // No health payload (loading or error) means the emulator's view is unknown —
  // fall back to treating every registry service as enabled and fully emulated.
  const tierOf = (name: string): EmulationTier => data?.serviceTiers?.[name] ?? "full"
  const isEnabled = (name: string) => data === undefined || data.services.includes(name)

  // Recently visited services first (recentServices holds route paths), then A-Z.
  const recentRank = new Map(recentServices.map((key, index) => [key, index]))
  const entries: ServiceTierEntry[] = ALL_SERVICES.map((service) => ({
    service,
    tier: tierOf(service.name),
    enabled: isEnabled(service.name),
  })).sort((a, b) => {
    const rankA = recentRank.get(a.service.to) ?? Number.MAX_SAFE_INTEGER
    const rankB = recentRank.get(b.service.to) ?? Number.MAX_SAFE_INTEGER
    return rankA - rankB || a.service.label.localeCompare(b.service.label)
  })

  const isAvailableTier = (tier: EmulationTier) => tier === "partial" || tier === "inert"
  const inUse = entries.filter((e) => e.enabled && e.tier === "full")
  const available = entries.filter((e) => e.enabled && isAvailableTier(e.tier))
  const notEmulated = entries.filter(
    (e) => !e.enabled || (e.tier !== "full" && !isAvailableTier(e.tier)),
  )

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-7xl py-6">
      <DashboardHeader
        activeCount={inUse.length + available.length}
        totalCount={ALL_SERVICES.length}
        view={view}
        onViewChange={setView}
      />

      {view === "list" ? (
        <ServiceListView
          onNavigate={addRecentService}
          groups={[
            { title: "in use", tone: "strong", entries: inUse },
            { title: "available", tone: "muted", entries: available },
            { title: "not emulated", tone: "subtle", entries: notEmulated },
          ]}
        />
      ) : (
        <div className="flex flex-col gap-6">
          <DashboardSection title="in use" count={inUse.length} tone="strong">
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-5">
              {inUse.map((entry) => (
                <ServiceCard key={entry.service.name} entry={entry} onNavigate={addRecentService} />
              ))}
            </div>
          </DashboardSection>

          <DashboardSection title="available" count={available.length} tone="muted">
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-5">
              {available.map((entry) => (
                <AvailableServiceCard
                  key={entry.service.name}
                  entry={entry}
                  onNavigate={addRecentService}
                />
              ))}
            </div>
          </DashboardSection>

          <DashboardSection
            title="not emulated"
            count={notEmulated.length}
            tone="subtle"
            note="registered · returns 501"
          >
            <NotEmulatedChips entries={notEmulated} />
          </DashboardSection>
        </div>
      )}

      {data && <DashboardFooter data={data} enabledCount={inUse.length + available.length} />}
    </div>
  )
}

function DashboardFooter({ data, enabledCount }: { data: HealthResponse; enabledCount: number }) {
  const overrides = data.storage.serviceOverrides ?? {}
  const overrideCount = Object.keys(overrides).length

  return (
    <p className="mt-6 text-center font-mono text-[11px] text-fg-subtle">
      Emulator {data.version} &middot; {enabledCount} of {ALL_SERVICES.length} services enabled
      &middot; storage: {data.storage.default}
      {overrideCount > 0 && (
        <Tooltip
          content={
            <ul className="space-y-0.5 text-left">
              {Object.entries(overrides).map(([svc, mode]) => (
                <li key={svc}>
                  {svc}: {mode}
                </li>
              ))}
            </ul>
          }
        >
          <span className="ml-1 cursor-help border-b border-dotted border-fg-subtle">
            ({overrideCount} override{overrideCount !== 1 ? "s" : ""})
          </span>
        </Tooltip>
      )}
    </p>
  )
}
