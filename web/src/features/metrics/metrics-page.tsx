/**
 * MetricsPage — live emulator runtime metrics at /metrics.
 *
 * Polls /_metrics every 3 seconds and renders rolling sparklines for:
 * - Heap allocated memory
 * - Total system memory
 * - Goroutine count
 * - GC pause time
 *
 * Static info cards show: uptime, Go version, CPU count, GC count, start time.
 */
import { BarChart2, AlertCircle, Info } from "lucide-react"
import { useMetrics } from "@/hooks/use-metrics"
import type { MetricsSnapshot } from "@/types"
import { Sparkline } from "@/components/ui/sparkline"
import { Tooltip } from "@/components/ui/tooltip"
import { PageHeader } from "@/components/ui/primitives"
import { Spinner } from "@/components/ui/primitives"
import { StartupCard } from "./startup-timeline"

// ─── Formatters ────────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function formatMs(ms: number): string {
  if (ms < 0.01) return "< 0.01 ms"
  if (ms < 1) return `${ms.toFixed(2)} ms`
  return `${ms.toFixed(1)} ms`
}

// ─── MetricCard ────────────────────────────────────────────────────────────

interface MetricCardProps {
  title: string
  value: string
  sub?: string
  info?: string
  sparkData: number[]
  color: string
}

function MetricCard({ title, value, sub, info, sparkData, color }: MetricCardProps) {
  return (
    <div className="bg-bg-card flex flex-col gap-3 rounded-lg border border-border p-4">
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-1">
          <p className="text-xs font-medium tracking-wider text-fg-muted uppercase">{title}</p>
          {info && (
            <Tooltip content={info}>
              <button type="button" className="text-fg-muted transition-colors hover:text-fg">
                <Info size={12} />
              </button>
            </Tooltip>
          )}
        </div>
      </div>

      <div className="flex items-end justify-between gap-2">
        <div>
          <p className="text-2xl font-semibold text-fg tabular-nums">{value}</p>
          {sub && <p className="mt-0.5 text-xs text-fg-muted">{sub}</p>}
        </div>
        <div className={color} style={{ minWidth: 100 }}>
          <Sparkline data={sparkData} color="currentColor" width={120} height={48} />
        </div>
      </div>
    </div>
  )
}

// ─── StatPill ──────────────────────────────────────────────────────────────

function StatPill({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-bg-card flex flex-col gap-0.5 rounded-md border border-border px-3 py-2">
      <span className="text-[10px] font-medium tracking-wider text-fg-muted uppercase">
        {label}
      </span>
      <span className="font-mono text-sm font-medium text-fg">{value}</span>
    </div>
  )
}

// ─── Component ─────────────────────────────────────────────────────────────

export function MetricsPage() {
  const { snapshots, latest, error } = useMetrics()

  const extract = (fn: (s: MetricsSnapshot) => number) => snapshots.map(fn)

  return (
    <div className="flex w-full flex-col gap-6">
      <PageHeader
        title="Metrics"
        description="Live Go runtime statistics — sampled every 3 seconds."
        actions={
          latest ? (
            <div className="flex items-center gap-1.5 rounded-full bg-green-500/10 px-2.5 py-1 text-xs font-medium text-green-400">
              <span className="h-1.5 w-1.5 rounded-full bg-green-400" />
              Live
            </div>
          ) : error ? (
            <div className="flex items-center gap-1.5 rounded-full bg-red-500/10 px-2.5 py-1 text-xs font-medium text-red-400">
              <AlertCircle className="h-3 w-3" />
              Disconnected
            </div>
          ) : (
            <div className="flex items-center gap-1.5 text-xs text-fg-muted">
              <Spinner className="h-3 w-3" />
              Connecting…
            </div>
          )
        }
      />

      {error && (
        <div className="flex items-center gap-2 rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-400">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      )}

      {/* ── Static info pills ─────────────────────────────────────────── */}
      {latest && (
        <div className="flex flex-wrap gap-2">
          <StartupCard totalMs={latest.startup_duration_ms} phases={latest.startup_phases} />
          <StatPill label="Uptime" value={latest.uptime} />
          <StatPill label="Go Version" value={latest.go_version} />
          <StatPill label="CPUs" value={String(latest.num_cpu)} />
          <StatPill label="GC Runs" value={String(latest.num_gc)} />
          <StatPill label="Goroutines" value={String(latest.goroutines)} />
          <StatPill label="Started" value={new Date(latest.start_time).toLocaleTimeString()} />
        </div>
      )}

      {/* ── Sparkline metric cards ───────────────────────────────────── */}
      {!latest && !error && (
        <div className="flex items-center justify-center py-20">
          <Spinner className="h-6 w-6" />
        </div>
      )}

      {latest && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-2 xl:grid-cols-4">
          <MetricCard
            title="Heap Allocated"
            value={formatBytes(latest.heap_alloc_bytes)}
            sub={`of ${formatBytes(latest.heap_sys_bytes)} heap sys`}
            info="The heap is a region of memory used for data that needs to live beyond a single function call — like request objects, cached items, and queued messages. This shows how much heap memory is currently in use by live data. 'Heap sys' is the total amount the runtime has reserved from the operating system for heap use (some of it may be free, waiting to be reused)."
            sparkData={extract((s) => s.heap_alloc_bytes)}
            color="text-sky-400"
          />
          <MetricCard
            title="System Memory"
            value={formatBytes(latest.sys_bytes)}
            sub={`${formatBytes(latest.heap_inuse_bytes)} heap in-use`}
            info="Total memory the emulator process has obtained from the operating system. This includes everything: the heap (long-lived data), the stack (short-lived function call data), and internal bookkeeping. 'Heap in-use' is the portion of the heap that currently holds live data, as opposed to free space waiting to be reused."
            sparkData={extract((s) => s.sys_bytes)}
            color="text-violet-400"
          />
          <MetricCard
            title="Goroutines"
            value={String(latest.goroutines)}
            sub="concurrent goroutines"
            info="Goroutines are lightweight threads managed by the Go runtime. Each handles a concurrent task like serving a request or running a background job."
            sparkData={extract((s) => s.goroutines)}
            color="text-emerald-400"
          />
          <MetricCard
            title="Last GC Pause"
            value={formatMs(latest.gc_pause_last_ms)}
            sub={`${formatMs(latest.gc_pause_total_ms)} total`}
            info="Garbage collection (GC) is the process that automatically finds and frees memory that is no longer being used. During a GC pause, the program is briefly stopped while the runtime cleans up. This shows how long the most recent pause lasted. Lower is better — pauses above 1 ms may cause noticeable latency spikes in request handling."
            sparkData={extract((s) => s.gc_pause_last_ms)}
            color="text-amber-400"
          />
        </div>
      )}

      {/* ── Secondary row ───────────────────────────────────────────── */}
      {latest && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <MetricCard
            title="Stack In-use"
            value={formatBytes(latest.stack_inuse_bytes)}
            sub="goroutine stacks"
            info="The stack is a region of memory where each goroutine stores its local variables and tracks which functions it is currently executing. Every goroutine gets its own small stack that grows automatically as needed. This shows the total memory used by all goroutine stacks combined. High values usually mean there are many active goroutines or deeply nested function calls."
            sparkData={extract((s) => s.stack_inuse_bytes)}
            color="text-pink-400"
          />
          <MetricCard
            title="Next GC Target"
            value={formatBytes(latest.next_gc_bytes)}
            sub="heap threshold for next GC"
            info="Garbage collection (GC) runs automatically when the heap grows large enough. This value is the heap size threshold that will trigger the next GC cycle. The Go runtime adjusts this target dynamically — by default, it allows the heap to roughly double in size before collecting again. A rising target means the program is holding more live data over time."
            sparkData={extract((s) => s.next_gc_bytes)}
            color="text-orange-400"
          />
        </div>
      )}

      {latest && (
        <p className="text-xs text-fg-subtle">
          Last sample:{" "}
          {new Date(latest.timestamp).toLocaleTimeString(undefined, {
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
          })}{" "}
          &middot; {snapshots.length} samples collected
        </p>
      )}
    </div>
  )
}

export { BarChart2 as MetricsIcon }
