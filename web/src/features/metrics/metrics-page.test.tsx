import { http, HttpResponse } from "msw"
import { render, renderWithRouter, screen, waitFor } from "@/test/render"
import { server } from "@/test/server"
import { MetricsPage } from "./metrics-page"

/** Minimal realistic GET /_metrics fixture — enough for MetricsPage to render past its loading state. */
const metricsSnapshot = {
  timestamp: new Date().toISOString(),
  uptime: "5m0s",
  uptime_secs: 300,
  start_time: new Date().toISOString(),
  startup_duration_ms: 120,
  pre_init_ms: 10,
  heap_alloc_bytes: 1024,
  heap_sys_bytes: 2048,
  heap_inuse_bytes: 1024,
  sys_bytes: 4096,
  stack_inuse_bytes: 512,
  num_gc: 1,
  gc_pause_last_ms: 0.1,
  gc_pause_total_ms: 0.5,
  next_gc_bytes: 8192,
  goroutines: 12,
  go_version: "go1.24",
  num_cpu: 8,
}

const healthyPersistentHealth = {
  status: "ok",
  timestamp: new Date().toISOString(),
  version: "test",
  services: ["s3", "sqs"],
  storage: {
    default: "hybrid",
    persistent: { mode: "hybrid", healthy: true, pendingWrites: 0 },
  },
}

function mockCoreEndpoints() {
  server.use(http.get("/api/metrics", () => HttpResponse.json(metricsSnapshot)))
}

describe("MetricsPage", () => {
  it("renames the page to Metrics & Health", async () => {
    // Given: a healthy storage backend reporting no advisories.
    mockCoreEndpoints()
    server.use(
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json({
          stores: [{ mode: "hybrid", journalMode: "wal", degraded: false }],
          advisories: [],
        }),
      ),
    )

    // When: the page renders. Plain render() (not renderWithRouter) is
    // enough here: nothing in this scenario renders the advisories'
    // docs-link <Link>, so no router context is needed, and render()'s
    // TooltipProvider covers the runtime-metrics cards' info tooltips.
    render(<MetricsPage />)

    // Then: the header reflects the renamed feature.
    expect(await screen.findByRole("heading", { name: "Metrics & Health" })).toBeInTheDocument()
  })

  it("shows a healthy storage badge and live journal mode in the health strip", async () => {
    // Given: a hybrid store correctly running in WAL mode with no issues.
    mockCoreEndpoints()
    server.use(
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json({
          stores: [{ mode: "hybrid", journalMode: "wal", degraded: false }],
          advisories: [],
        }),
      ),
    )

    // When: the page renders.
    render(<MetricsPage />)

    // Then: the health strip shows the storage mode, a "Healthy" badge, and the live journal mode.
    expect(await screen.findByText("hybrid")).toBeInTheDocument()
    expect(await screen.findByText("Healthy")).toBeInTheDocument()
    expect(await screen.findByText("wal")).toBeInTheDocument()
  })

  it("shows a quiet empty state when there are no advisories", async () => {
    // Given: a healthy backend with an empty advisories array.
    mockCoreEndpoints()
    server.use(
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json({
          stores: [{ mode: "hybrid", journalMode: "wal" }],
          advisories: [],
        }),
      ),
    )

    // When: the page renders.
    render(<MetricsPage />)

    // Then: the advisories section shows the quiet "no recommendations" state.
    expect(await screen.findByText("No recommendations")).toBeInTheDocument()
  })

  it("renders a server-computed advisory generically, including its docs link", async () => {
    // Given: the emulator reports a critical journal-mode-not-wal advisory.
    // /api/metrics is made to fail here (unlike mockCoreEndpoints()) so the
    // runtime-metrics cards never render — this test only needs a real
    // router (for the advisory's docs <Link>), not a TooltipProvider, and
    // renderWithRouter (unlike render()) doesn't wrap one in.
    server.use(
      http.get("/api/metrics", () => new HttpResponse(null, { status: 500 })),
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json({
          stores: [{ mode: "hybrid", journalMode: "delete" }],
          advisories: [
            {
              severity: "critical",
              code: "journal-mode-not-wal",
              title: "SQLite is not running in WAL mode",
              detail: 'The live PRAGMA journal_mode readback for the "hybrid" backend is "delete".',
              docsPath: "performance.md",
            },
          ],
        }),
      ),
    )

    // When: the page renders.
    renderWithRouter(MetricsPage, { path: "/metrics" })

    // Then: the advisory's title, severity, and detail render without the UI
    // needing to know anything about this specific rule ("journal-mode-not-wal"
    // never appears as a hardcoded string anywhere in the component tree).
    expect(await screen.findByText("SQLite is not running in WAL mode")).toBeInTheDocument()
    expect(screen.getByText("Critical")).toBeInTheDocument()
    expect(
      screen.getByText(/The live PRAGMA journal_mode readback for the "hybrid" backend/),
    ).toBeInTheDocument()
    const docsLink = screen.getByRole("link", { name: /View docs/ })
    expect(docsLink).toHaveAttribute("href", "/docs?path=performance.md")
  })

  it("shows storage activity reads/writes and the memory-vs-SQL split for a hybrid store", async () => {
    // Given: a hybrid store that has served some reads from memory and some
    // from SQLite, plus a background flush that has persisted some writes.
    mockCoreEndpoints()
    server.use(
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json({
          stores: [
            {
              mode: "hybrid",
              journalMode: "wal",
              counters: {
                reads: 120,
                writes: 40,
                readsMemory: 100,
                readsSQLite: 20,
                writesFlushedRows: 35,
              },
            },
          ],
          advisories: [],
        }),
      ),
    )

    // When: the page renders.
    render(<MetricsPage />)

    // Then: the storage activity card shows total reads/writes plus the
    // hybrid-specific memory/SQLite tier breakdown.
    expect(await screen.findByText("Storage Activity")).toBeInTheDocument()
    expect(screen.getByText("120")).toBeInTheDocument()
    expect(screen.getByText("40")).toBeInTheDocument()
    expect(screen.getByText("Memory reads")).toBeInTheDocument()
    expect(screen.getByText("SQLite reads")).toBeInTheDocument()
    expect(screen.getByText("35 op-rows flushed to disk")).toBeInTheDocument()
  })

  it("omits the memory-vs-SQL split for a non-hybrid store", async () => {
    // Given: a persistent (SQLite-only) store, which has no second tier.
    mockCoreEndpoints()
    server.use(
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json({
          stores: [{ mode: "persistent", counters: { reads: 10, writes: 5 } }],
          advisories: [],
        }),
      ),
    )

    // When: the page renders.
    render(<MetricsPage />)

    // Then: totals show, but no memory/SQLite breakdown is rendered.
    expect(await screen.findByText("Storage Activity")).toBeInTheDocument()
    expect(screen.queryByText("Memory reads")).not.toBeInTheDocument()
  })

  it("explains when storage diagnostics are unavailable because debug mode is off", async () => {
    // Given: OVERCAST_DEBUG is disabled, so /_debug/metrics 404s.
    mockCoreEndpoints()
    server.use(
      http.get("/api/health", () => HttpResponse.json(healthyPersistentHealth)),
      http.get("/api/debug/metrics", () =>
        HttpResponse.json(
          {
            error: "DebugDisabled",
            message: "OVERCAST_DEBUG must be enabled to inspect raw state or storage diagnostics.",
          },
          { status: 404 },
        ),
      ),
    )

    // When: the page renders.
    render(<MetricsPage />)

    // Then: the advisories section explains why, instead of a bare error.
    await waitFor(() =>
      expect(
        screen.getByText(
          "OVERCAST_DEBUG must be enabled to inspect raw state or storage diagnostics.",
        ),
      ).toBeInTheDocument(),
    )
  })
})
