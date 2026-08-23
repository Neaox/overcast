/**
 * StorageActivity — Metrics & Health page's "Storage Activity" section.
 *
 * Sourced from GET /_overcast/debug/metrics's `stores` array (debug-gated — see
 * debugMetricsQueryOptions; the health strip and advisories list already
 * explain when that endpoint is unavailable, so this section simply renders
 * nothing rather than repeating that explanation a third time).
 *
 * Shows cumulative reads/writes since process start for each reporting
 * store (internal/state.StoreCounters). A "hybrid" mode store additionally
 * breaks reads down by which tier actually served them — memory vs a
 * fall-through to SQLite — since hybrid is the only backend that serves from
 * two distinct tiers at all.
 *
 * Flushed op-rows are reported on their own rather than as a fraction of
 * Writes: the two count different units (pending-op rows committed by the
 * background flusher vs logical write calls), and rows replayed from a
 * previous process's journal are flushed by this one, so flushed can exceed
 * writes and is not a subset of it.
 */
import type { DebugMetrics } from "@/types"
import { Spinner } from "@/components/ui/primitives"
import { fieldLabel, sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

function formatCount(n: number): string {
  return n.toLocaleString()
}

function TierBar({
  label,
  value,
  total,
  colorClass,
}: {
  label: string
  value: number
  total: number
  colorClass: string
}) {
  const pct = total > 0 ? Math.round((value / total) * 100) : 0
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between text-xs">
        <span className="text-fg-muted">{label}</span>
        <span className="font-mono text-fg tabular-nums">
          {formatCount(value)} ({pct}%)
        </span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-bg-muted">
        <div className={cn("h-full rounded-full", colorClass)} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function StoreActivityCard({ store, index }: { store: DebugMetrics; index: number }) {
  const { counters } = store
  // The generated type says the server always sends counters (it does), but
  // the console can be pointed at an older emulator binary that predates
  // them — render nothing for this store rather than crashing the whole
  // Metrics & Health page. The runtime check is deliberate, hence the lint
  // waiver: the type describes the current server, not every server.
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
  if (!counters) return null
  // readsMemory is only populated for a hybrid-mode store (see
  // StoreCounters's Go doc comment) — checking it directly, rather than
  // branching on store.mode, keeps this generic against a future backend
  // that grows a second tier.
  const hasTierSplit = counters.readsMemory !== undefined || counters.readsSQLite !== undefined

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-elevated p-4">
      <div className="flex items-center justify-between">
        <span className="font-mono text-sm font-medium text-fg">
          {store.mode || `store ${index + 1}`}
        </span>
        <span className="text-xs text-fg-muted">since process start</span>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div>
          <p className={cn(fieldLabel, "text-fg-muted")}>Reads</p>
          <p className="font-mono text-xl font-semibold text-fg tabular-nums">
            {formatCount(counters.reads)}
          </p>
        </div>
        <div>
          <p className={cn(fieldLabel, "text-fg-muted")}>Writes</p>
          <p className="font-mono text-xl font-semibold text-fg tabular-nums">
            {formatCount(counters.writes)}
          </p>
        </div>
      </div>

      {hasTierSplit && (
        <div className="flex flex-col gap-2 border-t border-border pt-3">
          <p className={cn(fieldLabel, "text-fg-muted")}>Memory vs disk (SQL)</p>
          <TierBar
            label="Memory reads"
            value={counters.readsMemory ?? 0}
            total={counters.reads}
            colorClass="bg-cat-6"
          />
          <TierBar
            label="SQLite reads"
            value={counters.readsSQLite ?? 0}
            total={counters.reads}
            colorClass="bg-cat-8"
          />
          {counters.writesFlushedRows !== undefined && (
            <p className="text-xs text-fg-muted">
              {formatCount(counters.writesFlushedRows)} op-rows flushed to disk
            </p>
          )}
        </div>
      )}
    </div>
  )
}

export function StorageActivity({
  stores,
  isLoading,
}: {
  stores: DebugMetrics[] | undefined
  isLoading: boolean
}) {
  const list = stores ?? []
  // Debug mode off, the query failed, or (in principle) no reporting store
  // at all — the health strip's badge and the advisories list already
  // explain unavailability, so stay quiet instead of a third empty state.
  if (!isLoading && list.length === 0) return null

  return (
    <div className="flex flex-col gap-3">
      <h2 className={cn(sectionLabel, "text-fg-muted")}>Storage Activity</h2>
      {isLoading ? (
        <div className="flex justify-center py-10">
          <Spinner className="h-5 w-5" />
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {list.map((store, i) => (
            <StoreActivityCard key={`${store.mode}-${i}`} store={store} index={i} />
          ))}
        </div>
      )}
    </div>
  )
}
