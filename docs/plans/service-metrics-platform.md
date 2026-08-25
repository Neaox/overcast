# Service metrics platform plan (Lambda pilot + service rollout)

> **Status:** **Lambda pilot (phase 1) shipped 2026-08-22; SQS/SNS/DynamoDB/
> API Gateway rollout (phase 2) shipped 2026-08-22; the web Monitor tab/
> section and the 300s/3600s resolution rollup ladder (phase 3) shipped
> 2026-08-22; Lambda's async P1 metric tier, WALStore/`-tags nosqlite`
> persistence, and the SNS/DynamoDB Monitor tabs (phase 4) shipped
> 2026-08-23**, tracked by
> [#1181](https://github.com/Neaox/overcast/issues/1181) (priority/p1, RICE ≈ 100).
> #1181 was closed by an earlier commit before phase 2/3/4 shipped — see the
> issue's own comment thread for the correction; it stays closed here because
> every phase-4 item is now either shipped or tracked as its own issue: the
> remaining Lambda ESM/P2 tiers and API Gateway's Monitor tab are #1307,
> DynamoDB throttling modeling is #1305, and SNS's `fanOut` silent-delivery
> gap is #1306 — not because the whole plan is finished in one shot.
> `internal/metrics` exists and is wired: Lambda automatically records
> `Invocations`/`Errors`/`Duration`/`Throttles`/`ConcurrentExecutions`; SQS
> records its message-operation and queue-depth-gauge catalogue; SNS records
> its publish/delivery catalogue; DynamoDB records its per-operation
> latency/capacity/error catalogue; API Gateway records its per-request
> execution catalogue for both REST (v1) and HTTP (v2) APIs. CloudWatch's
> `PutMetricData`/`ListMetrics`/`GetMetricStatistics`/`GetMetricData` — and
> its alarm evaluator — see all of this merged alongside user-supplied custom
> metrics, through the same namespace-agnostic read-through phase 1 built: no
> per-namespace CloudWatch code was needed for phase 2's four new namespaces.
> An `AWS/Lambda Errors` alarm and an `AWS/SQS ApproximateNumberOfMessagesVisible`
> alarm both evaluate and fire end-to-end from automatic observations alone
> (see `internal/services/cloudwatch/metrics_bridge_test.go`'s
> `TestLambdaErrorsAlarm_FiresFromAutomaticMetrics` and
> `TestSQSApproximateNumberOfMessagesVisibleAlarm_FiresFromAutomaticMetrics`).
> Cost/benefit recorded in the issue: Overcast already evaluates CloudWatch
> alarms but no service emitted the metrics they watch, so configured alarms
> silently never fired — the §2.1 "shape 2" divergence.
>
> **What phase 1 deliberately narrowed from this document, and why** (see the
> "Phase 1 delivered scope" section below for the full list): CloudWatch's own
> `PutMetricData`-backed storage was **not** migrated onto `internal/metrics`
> — the design below sketches that as one option ("same store"), and phase 1
> took the explicitly-permitted alternative instead: a read-through merge in
> `internal/services/cloudwatch/metrics_bridge.go`, chosen to avoid touching
> the CloudWatch storage engine's own already-extensive test suite for a pilot.
> The generic K/V-backed persistence path for `WALStore`/`-tags nosqlite`
> builds (`metrics:series:v1`/`metrics:buckets:v1`) was not built; those
> configurations get correct in-memory-only automatic metrics (no restart
> continuity). The fuller 300s/3600s resolution rollup ladder for 7d/30d
> graphs and the web Monitor tab remain phase 3+ — see the "Phase 2 delivered
> scope" section below for the complete phase-3 remainder.
>
> **What phase 2 deliberately narrowed, and why** (see "Phase 2 delivered
> scope" below for the full list): DynamoDB's `ConsumedReadCapacityUnits`/
> `ConsumedWriteCapacityUnits` are computed from an item's DynamoDB-JSON
> encoded byte length, not AWS's precise per-attribute-type size algorithm —
> a disclosed, proportionate approximation, not a wire-exact replica of AWS's
> billing math. DynamoDB `ThrottledRequests`/`ReadThrottleEvents`/
> `WriteThrottleEvents` are not recorded because this emulator does not model
> throttling at all — there is no underlying fact to observe. API Gateway
> recorded only the most granular documented dimension combination per request
> (never the coarser `ApiName+Stage`-only aggregate AWS also publishes),
> mirroring Lambda phase 1's own `FunctionName`-only narrowing — a narrowing
> #1307 later lifted; see the phase 2 delivered-scope entry.
>
> **What phase 3 delivered** (see "Phase 3 delivered scope" below for the full
> list): a Lambda **Monitor tab** and an SQS **Monitor section**, each reading
> through its own new `GET /_overcast/<service>/.../metrics` allowlist
> endpoint — never CloudWatch protocol, never a second query engine, built
> entirely on `internal/metrics`'s own new `QueryAuto`/`ChartQuery` read path —
> and the 60s/300s/3600s resolution rollup ladder the design always specified,
> extending the 60s tier's retention from phase 1's 1h to the full 24h the
> design's table calls for.
>
> **What phase 4 delivered** (see "Phase 4 delivered scope" below for the full
> list): Lambda's async P1 metric tier (`AsyncEventsReceived`/`AsyncEventAge`/
> `AsyncEventsDropped`/`DeadLetterErrors`/`DestinationDeliveryFailures`); a
> generic `state.Store`-backed metrics repository (`internal/metrics/kv_backend.go`)
> for `*state.WALStore` — and hence every `-tags nosqlite` build — so automatic
> metrics now survive a restart on those configurations, closing the one gap
> phases 1-3 all explicitly deferred; and SNS/DynamoDB Monitor tabs reusing
> phase 3's `MonitorPanel`/`MetricLineChart` unchanged. **What remains**:
> Lambda's ESM/P2 tiers and `Resource`/`ExecutedVersion` dimensions — tracked
> as #1307, not phase 4 scope. #1307's other item, API Gateway's Monitor tab
> (blocked on a coarse-aggregate-series design decision phase 2 never made),
> has since shipped: the coarse `{ApiName}`/`{ApiName, Stage}` (REST) and
> `{ApiId}`/`{ApiId, Stage}` (HTTP) series are now recorded per request and
> both API detail pages gained a Monitor tab — see the phase 4 delivered-scope
> entry. Migrating
> `PutMetricData`'s own storage onto `internal/metrics` was evaluated and
> **not done** — see "Phase 4 delivered scope" for why.
>
> **Scope:** an AWS-shaped metrics substrate shared by every emulated service,
> delivered first for Lambda (phase 1), then for SQS, SNS, DynamoDB, and API
> Gateway (phase 2), then the web Monitor tab/section and resolution rollup
> ladder (phase 3), then Lambda's async tier, WALStore/`-tags nosqlite`
> persistence, and SNS/DynamoDB Monitor tabs (phase 4). This is a plan, not an
> API commitment.

## Outcome

Overcast should automatically publish the service metrics that AWS publishes
where the emulator can observe the underlying fact. A Lambda invocation should
therefore appear in the existing CloudWatch APIs and in a Lambda monitoring
screen without application code publishing a custom metric. The same small
recording API must allow SQS, SNS, DynamoDB, API Gateway, and future services
to contribute their native AWS namespaces without each implementing storage,
aggregation, retention, query semantics, or charts again.

The initial compatibility target is AWS's one-minute service-metric model:

- Metric identity is `(namespace, metric name, complete dimension set)`.
- Count metrics are emitted as `1` or a meaningful batch count and queried with
  `Sum`; duration-like metrics preserve value/count/sum/min/max and are queried
  with `Average`, `Minimum`, `Maximum`, and `Sum`.
- Service metrics have AWS names, namespaces, units, dimensions, timing, and
  inclusion/exclusion semantics. Emulator-only data stays on `/_` endpoints;
  it is never added to an AWS response.
- A missing metric means no observation was emitted, not a synthetic zero.
  Charts may render a zero-filled _display_ series only where AWS's console
  convention warrants it.

The plan deliberately builds on the currently implemented CloudWatch metric
APIs (`PutMetricData`, `ListMetrics`, `GetMetricStatistics`, and
`GetMetricData`) rather than creating a second query API.

## Architectural decision: event bus is an input, not the ledger

The shared `internal/events.Bus` is valuable infrastructure: it has typed
events, bounded workers, history, SSE fan-out, and already exposes Lambda
instance and event-source-mapping transitions. It should be extended with
small, stable **semantic outcome events** when those events are useful to more
than one consumer.

It cannot, however, be the sole source of AWS-visible metrics:

1. Delivery is asynchronous and handlers have no acknowledgement/error result.
   A recorder cannot prove an observation was persisted or retry it.
2. It uses a shared worker queue; making metrics writes a subscriber couples
   every event producer to a potentially slow storage projection.
3. The rolling history and SSE slow-client policy are intentionally lossy and
   non-durable. They are correct for operator activity, not accounting.
4. Existing events often describe lifecycle/UI topology, not the precise
   execution outcome, dimensions, and timestamp rules required by CloudWatch.

**Decision:** service code records a metric observation directly at the
authoritative lifecycle boundary. The same boundary may publish a typed domain
event for the event stream, but neither subsystem derives correctness from the
other. To keep this DRY, both calls are made by a narrow service-local outcome
helper (for example, Lambda's `recordInvocationOutcome`), not duplicated across
HTTP, async, function-URL, streaming, and event-source paths.

The metrics package may subscribe to selected existing events only for
best-effort, non-accounting projections (live UI invalidation, trace-style
activity feed, or future derived diagnostics). A future reliable event-log
project is separate work; do not turn `events.Bus` into a durable event store
as part of metrics.

## Target design

### Package boundaries

Extract metric-domain storage and aggregation out of
`internal/services/cloudwatch` into a new dependency-neutral
`internal/metrics` package. CloudWatch becomes the AWS protocol adapter over
that package; service packages depend only on a narrow injected recorder
interface.

```text
Lambda / SQS / SNS / ...
        │  Observe(ctx, Observation)       (no CloudWatch service dependency)
        ▼
internal/metrics.Recorder ──► bucketed repository ──► state.Store
        │                              ▲
        │                              │
        └── internal events (optional) │
                                       CloudWatch service
                                  AWS query protocol adapter
                                       ▲
                                       │
                           Web BFF/internal metrics API
```

Proposed initial public types (names are illustrative and should be refined
while extracting tests):

```go
type Observation struct {
    Namespace string
    Name      string
    Dimensions []Dimension // canonicalized, immutable after Observe
    Timestamp time.Time
    Unit      Unit
    Value     float64
}

type Recorder interface {
    Observe(context.Context, Observation) error
}

type Repository interface {
    Put(context.Context, Observation) error
    ListMetrics(context.Context, MetricFilter) (Page, error)
    Query(context.Context, Query) ([]Series, error)
}
```

`Observation` intentionally represents one measured fact. Metric-definition
code, not handlers, expands one invocation into `Invocations`, `Errors`,
`Duration`, and concurrency samples. This prevents arbitrary strings and
dimension variants from leaking across services, makes the AWS metric catalogue
reviewable, and keeps caller code obvious.

`metrics.NewRecorder(...)` must accept `state.Store`, `clock.Clock`, and a
logger; it has no globals and does no I/O in construction. When collection is
effective, the router creates one instance, injects it into CloudWatch and
opt-in services, starts its flush loop after construction, and stops/flushed it
before the state store closes. The existing CloudWatch alarm evaluator remains
a CloudWatch concern.

### Storage and performance model

Use aligned aggregate buckets keyed by canonical namespace/name/dimension-set
and time bucket. Each bucket stores count, sum, min, max, and unit. This makes
normal CloudWatch statistics constant-space per active series per bucket rather
than one durable row per invocation. Preserve the current dimension
canonicalisation and range-read lessons from the existing CloudWatch store.

- `Observe` is synchronous only through a short sharded-lock in-memory update;
  it performs no `state.Store` I/O, HTTP, JSON encoding, goroutine creation, or
  event-bus publish. It records with the injected clock when callers omit time.
- A bounded, coalescing background flusher persists dirty buckets in batches.
  Reads merge persisted buckets with current in-memory buckets, so successful
  observations are query-visible before a flush. `Flush` on shutdown is
  bounded and reports failures in health/logging; it must not silently discard
  them.
- Retention is a single package policy, initially matching the existing
  CloudWatch one-hour local-development retention. A periodic paged sweeper
  physically removes expired buckets; queries also exclude expired data. Make
  retention/configuration explicit only if a concrete use case requires it.
- Back-pressure is intentional: no silent metric drop. If the dirty-bucket cap
  is reached, briefly flush/coalesce rather than allocating without bound.
  Instrument this internal condition in logs/health, but never fabricate AWS
  service metrics for it.
- Dimensions are sorted once and copied at the API boundary. Per-service metric
  definitions use predeclared names/dimension layouts and avoid maps/reflection
  on the invocation hot path.

The repository must be implemented and tested against every `state.Store`
mode. It must isolate malformed persisted buckets (warn and skip) exactly as
other list/scan paths do. It should use a dedicated `metrics:` namespace (or
migrate the current `cloudwatch:metrics`/`cloudwatch:metricdata` keys in a
backward-compatible read path); the migration decision must be made before the
first release to avoid orphaning user-supplied custom metrics.

### Concrete storage contract across tiers

This is the proposed v1 format. It makes the hot recording path independent of
the configured storage mode while retaining predictable, pageable persistent
keys. It deliberately stores aggregates, not a per-invocation event log.

#### Canonical series identity

Before any read or write, validate and canonicalise an observation:

1. Preserve namespace, metric name, unit, and dimension name/value bytes as
   supplied after CloudWatch validation; do not lowercase them.
2. Drop no dimensions and reject invalid/duplicate names using the same rules
   as `PutMetricData`.
3. Sort dimensions by `(name, value)` and encode each length-delimited value.
   Do **not** build persistent identifiers by concatenating raw values with
   `/`, `|`, or `=`: those characters are legal enough to make ambiguous keys.
4. Compute `seriesID = sha256(v1 + namespace + metricName + canonicalDimensions)`.
   The digest is an opaque key component, not a replacement for the full
   identity: every persisted record includes/points to the canonical identity,
   so a corrupt record or theoretical collision is detected and skipped rather
   than silently merged.
5. Floor the timestamp to the UTC minute for the bucket key. The original
   timestamp is not required for standard minute statistics; it remains needed
   only if a later, explicitly designed percentile sample store is added.

#### Logical records and namespaces

| Namespace            | Key                                      | Value                                                                                | Tier / access pattern                                                                                            |
| -------------------- | ---------------------------------------- | ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| `metrics:series:v1`  | `<seriesID>`                             | Version, namespace, metric name, sorted dimensions, unit, first/last-seen timestamps | In-memory backend representation and migration metadata only; SQLite uses the `metric_series` table below.       |
| `metrics:buckets:v1` | `<seriesID>/<resolution>/<bucket-start>` | Version, canonical identity check fields, bucket start, count, sum, min, max, unit   | In-memory backend representation and migration compatibility only; SQLite uses the `metric_buckets` table below. |
| `metrics:meta:v1`    | `<seriesID>` (optional)                  | Last flushed minute / diagnostic schema marker only                                  | `TierCached`; add only if it proves necessary for migration or sweeps.                                           |

`<bucket-start>` is a fixed-width, lexically sortable UTC epoch encoding. It
preserves the existing CloudWatch range-read property for non-SQL backends: a
query starts at its first requested bucket and stops once the key is past the
end, rather than decoding an entire metric history. Store keys remain internal;
the full series metadata—not a key reconstruction—is always the source of truth
returned by CloudWatch.

Avoid a `metrics:observations` namespace in v1. It would turn every Lambda
invocation into a durable write, recreate the current CloudWatch per-point cost,
and make retention proportional to request rate. If exact percentile support is
approved later, add a separately bounded, versioned sample namespace with its
own retention and documented approximation/error behavior; do not quietly
change bucket semantics.

#### Runtime tier by backend

| Layer                        | Memory mode                                                      | Hybrid / persistent / WAL mode                                                                                         | Responsibilities                                                                                                   |
| ---------------------------- | ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| Process-local active buckets | Required; sharded map of dirty minute buckets                    | Required; same implementation                                                                                          | The only structure touched by `Observe`; bounded by active series and dirty-cap policy.                            |
| Metrics repository backend   | In-memory series/dimension/bucket maps; disappears on restart    | Dedicated SQLite tables for SQLiteStore/HybridStore; paged generic `state.Store` records for WAL and `nosqlite` builds | Source for historical queries and restart continuity where the configured backend supports it.                     |
| In-process read overlay      | Required                                                         | Required                                                                                                               | Merges active buckets over persisted buckets so a just-recorded metric is visible before the metrics flusher runs. |
| Metrics flusher              | Coalesces maps; commits to in-memory backend for behavior parity | Coalesces maps; writes one SQLite transaction per bounded batch                                                        | Runs on interval/size signal, never in `Observe`; flushes on orderly service shutdown.                             |
| Retention/rollup job         | Evicts expired buckets from maps                                 | Creates coarse rollups then range-deletes expired fine buckets using indexed SQL                                       | Physical cleanup backstopped by read-time expiry filtering.                                                        |

**Decision: use dedicated SQLite tables where SQLite is the configured storage
engine.** The generic K/V store remains excellent for resource metadata, but it
is the wrong primary physical layout for metrics: it stores aggregate fields as
JSON; `ListMetrics` needs a namespace/dimension discovery scan; a graph needs
multiple bounded time ranges; and retention otherwise scans/decode-checks
key-value rows. The existing CloudWatch benchmarks correctly protect prefix
range reads, but they do not make generic K/V as selective as a composite SQL
index or as cheap for multi-series dashboard reads.

The repository remains backend-neutral. `MemoryStore` uses maps; `SQLiteStore`
and `HybridStore` use the table design below; `WALStore` and `nosqlite` builds
use the versioned K/V records above, retaining their normal persistence and
replay semantics. Do not pretend every backend has identical crash atomicity:
SQLite makes a flushed series/dimensions/bucket batch transactional, while the
K/V fallback must tolerate an interrupted batch by skipping an orphan or
malformed bucket until the next flush repairs it. Their successful API results,
statistics, ordering, and retention semantics must remain identical.

#### Dedicated SQLite schema and indexes

Reserve migration versions **40–49** for the metrics repository and register
the schema migration in `internal/metrics`; update `internal/state/migrate.go`'s
range documentation at implementation time. The initial schema is:

```sql
CREATE TABLE metric_series (
  series_id       BLOB PRIMARY KEY, -- SHA-256 v1 identity
  namespace       TEXT NOT NULL,
  metric_name     TEXT NOT NULL,
  dimensions_json TEXT NOT NULL,    -- canonical ordered API response shape
  unit            TEXT NOT NULL,
  first_seen_ms   INTEGER NOT NULL,
  last_seen_ms    INTEGER NOT NULL,
  UNIQUE(namespace, metric_name, dimensions_json)
);
CREATE INDEX idx_metric_series_namespace_name
  ON metric_series(namespace, metric_name, series_id);

CREATE TABLE metric_series_dimensions (
  series_id BLOB NOT NULL,
  name      TEXT NOT NULL,
  value     TEXT NOT NULL,
  PRIMARY KEY (series_id, name, value)
);
CREATE INDEX idx_metric_series_dimensions_lookup
  ON metric_series_dimensions(name, value, series_id);

CREATE TABLE metric_buckets (
  series_id        BLOB NOT NULL,
  resolution_sec   INTEGER NOT NULL,
  bucket_start_ms  INTEGER NOT NULL,
  sample_count     INTEGER NOT NULL,
  sum_value        REAL NOT NULL,
  min_value        REAL NOT NULL,
  max_value        REAL NOT NULL,
  PRIMARY KEY (series_id, resolution_sec, bucket_start_ms)
);
CREATE INDEX idx_metric_buckets_expiry
  ON metric_buckets(resolution_sec, bucket_start_ms);
```

The bucket primary key directly serves the dominant chart query:
`series_id IN (...) AND resolution_sec = ? AND bucket_start_ms BETWEEN ? AND ?`.
`metric_series` serves namespace/name discovery; `metric_series_dimensions`
serves CloudWatch dimension filters and Lambda's `FunctionName` selection
without JSON scanning. Store the ordered JSON once for lossless AWS response
formatting, but never use it as a query predicate. Do not add an index for every
possible metric dimension: the inverted dimension table is the generic index.

The flusher uses `INSERT ... ON CONFLICT(series_id, resolution_sec,
bucket_start_ms) DO UPDATE` to add count/sum and choose min/max. It must batch
series upserts, dimension inserts, and bucket upserts in one bounded SQLite
transaction. This avoids one transaction per invocation while making a flushed
bucket atomically discoverable with its dimensions.

### Access patterns, retention, and graph time spans

The UI and AWS query API drive the physical design; they are not an afterthought.

| Access pattern                                                                | Frequency                           | Query/index                                                                              | Design consequence                                                                                                                       |
| ----------------------------------------------------------------------------- | ----------------------------------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Record one observed fact                                                      | Every service operation             | In-memory active bucket only                                                             | No SQLite or JSON work on the request path.                                                                                              |
| Lambda Monitor panel (four to six exact series, one function, selected range) | Poll/refetch while visible          | Resolve `FunctionName` through dimension index once; bounded bucket range by primary key | Use one repository `QueryMany`, then one `GetMetricData`-equivalent BFF/CloudWatch response; never issue one request per rendered point. |
| CloudWatch `GetMetricStatistics`                                              | Exact metric/dimensions, one series | Unique series lookup + bucket primary-key range                                          | Aggregate selected stored resolution into requested period server-side.                                                                  |
| CloudWatch `GetMetricData`                                                    | Up to many known series             | Batch series IDs and range queries, bounded/chunked                                      | Avoid N+1 queries; preserve result ordering and AWS pagination limits.                                                                   |
| `ListMetrics` / metric picker                                                 | Infrequent, paged                   | `idx_metric_series_namespace_name`; dimension join/group for supplied filters            | Never scan metric bucket rows to discover a metric.                                                                                      |
| Retention                                                                     | Background                          | `idx_metric_buckets_expiry`                                                              | Roll up/delete in pages; no full-table scan or work in a request.                                                                        |

Persist the following resolution ladder, computed by the maintenance job from
immutable bucket aggregates (count/sum/min/max compose exactly):

| Stored resolution | Retention | UI/API use                                      |
| ----------------- | --------- | ----------------------------------------------- |
| 60 seconds        | 24 hours  | Last 1h, 6h, and 24h graphs; current debugging. |
| 300 seconds       | 7 days    | 24h and 7d graphs once minute buckets expire.   |
| 3600 seconds      | 30 days   | 7d and 30d trend graphs.                        |

> **Revised by #1307:** the 300s tier is now retained for the full 30 days
> (still ~8640 constant-size buckets per active series at most), so the 30d
> view charts 15-minute display buckets instead of 1-hour ones; the 3600s
> tier remains as the final fallback. The UI's coherent controls are
> therefore 1h/1m, 6h/1m, 24h/5m, 7d/5m, and **30d/15m**.

The query planner selects the finest resolution whose retention fully covers
the requested interval, then uses a period that is a multiple of that
resolution. The UI exposes only coherent controls: **1h/1m, 6h/1m, 24h/5m,
7d/5m, and 30d/1h** (30d since revised — see the #1307 note above). It asks for a complete half-open UTC interval and the
server returns chronologically sorted, bucket-aligned points with missing data
omitted. Rendering may show gaps; it must not interpolate a metric value or
mistake an absent service emission for zero. This corrects the present
CloudWatch screen's 1h/6h/24h options, which sit above its one-hour raw
retention without a corresponding rollup tier.

The 24h/7d/30d figures are initial local-development budgets, not AWS retention
claims. Make them constants behind repository policy and benchmark realistic
Lambda/SQS cardinality before exposing configuration. A high-cardinality
dimension catalogue or a need for exact pNN changes this storage decision and
must be separately designed.

### Performance model and feature controls

Metrics are a cross-cutting subsystem, so the default must be useful without
making every emulated operation meaningfully slower. The expected cost model is:

| Mode                                 | Request-path work                                                                                                                                | Background work                                        | Observable result                                                                                                                        |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Collection disabled                  | One predictable branch at each instrumented operation; no outcome construction, map lookup, allocation, event, goroutine, query, or storage work | No recorder, flusher, sweeper, or metrics table access | Existing manual CloudWatch metrics remain queryable if CloudWatch is enabled; no new automatic service datapoints.                       |
| Collection enabled, normal           | Build the already-needed service outcome once; one sharded active-bucket update per emitted metric; no I/O                                       | Coalesced bounded flushes, rollups, and retention      | Fresh data is query-visible through the overlay, then durable according to the selected state backend.                                   |
| Collection enabled, pressure/failure | Never synchronously wait on SQLite from the AWS handler; apply bounded dirty-cap/back-pressure policy                                            | Retry/health/logging and bounded batches               | A local persistence degradation is visible to operators but does not turn a successful Lambda invocation into an artificial AWS failure. |

Do not promise nanoseconds before measurement. Phase 0 must establish a baseline
and Phase 1/2 must compare it under `memory`, `hybrid`, `persistent`, and `wal`
using stub and Docker-backed Lambda invocations. The acceptance gate is a
measured delta, not an intuition:

- Disabled collection adds no allocations and no background goroutines; its
  benchmark delta must remain within noise against an otherwise identical
  invocation path.
- Enabled collection does no storage I/O on the request path and has bounded
  allocations/lock contention. Measure p50/p95/p99 `Observe`, allocations/op,
  max active buckets, flush duration, and query latency against 1, 100, and
  high-cardinality function/queue series.
- Flushes use a maximum bucket count and transaction duration, yield between
  batches, and are benchmarked alongside normal service state writes to reveal
  SQLite contention rather than hiding it in an isolated benchmark.
- Rollup/retention work is time-sliced and page-bounded. It cannot load all
  metric rows, run inside `router.New()`, or stall graph requests.

Metrics are an **internal subsystem**, not an AWS service. Do not put `metrics`
in `OVERCAST_SERVICES`: it has no AWS wire endpoint and doing so would conflate
an implementation control with AWS service emulation. Add typed configuration:

```text
OVERCAST_SERVICE_METRICS=auto|enabled|disabled    # default: auto
OVERCAST_SERVICE_METRICS_SERVICES=lambda,sqs,...  # optional later allowlist
```

`auto` follows whether the `cloudwatch` AWS service is enabled in
`OVERCAST_SERVICES`; this is the default because a user who disables CloudWatch
normally expects to avoid its storage and CPU cost. `enabled` and `disabled`
are explicit overrides, and the effective state is reported in health/debug
diagnostics. Start with the global switch; add the service allowlist only when
the first non-Lambda rollout proves it is needed, rather than baking a broad
configuration surface into the pilot.

The resulting matrix is intentional:

| CloudWatch AWS service | Automatic collection | Behavior                                                                                                                                                                                                                             |
| ---------------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| enabled                | `auto` / enabled     | Collect automatic service metrics; CloudWatch APIs and the web monitor query them.                                                                                                                                                   |
| enabled                | disabled             | CloudWatch remains available for `PutMetricData`, alarms, and queries; automatic Lambda/SQS/etc. emission stops. Existing persisted data remains readable until retention.                                                           |
| disabled               | `auto` / disabled    | No automatic collection, recorder goroutines, or metrics-table activity. CloudWatch requests continue to receive the normal service-disabled response.                                                                               |
| disabled               | enabled              | Collect and retain automatic metrics for an explicitly requested internal monitor/debug use case; AWS CloudWatch routes remain disabled. Any web display uses a clearly internal BFF endpoint, never a fake AWS CloudWatch response. |

Changing the setting is process-start configuration for the pilot. Do not add a
runtime toggle until its synchronization, flush, and UI invalidation semantics
are explicitly designed. Re-enabling collection starts fresh observations; it
does not backfill invocations that occurred while disabled.

#### Write, read, failure, and shutdown semantics

1. `Observe` updates one active bucket under its shard lock and marks it dirty.
   It returns an error only for invalid input or a recorder that is shutting
   down; a normal storage outage is not synchronously coupled to a Lambda
   response.
2. The flusher snapshots dirty buckets, releases locks, serialises the compact
   aggregate records, and writes a batch. A newer concurrent observation stays
   dirty for the next flush; it must never be overwritten by an older snapshot.
3. Queries read persisted range records, discard malformed/expired/version-wrong
   records with a warning, then merge the active overlay by `(seriesID, minute)`
   without double counting. The merge rule is explicit: an overlay bucket
   replaces its persisted predecessor only when it contains the corresponding
   persisted baseline, otherwise use a generation/version counter to merge
   deltas safely. This concurrency contract needs focused race tests.
4. On a flush failure, retain dirty aggregates, exponentially/backoff retry in
   the background, expose degraded state in health/logs, and bound memory using
   the configured dirty-cap policy. Never report a successful flush merely
   because a batch was queued.
5. On graceful shutdown, stop accepting new observations, drain/flush until a
   bounded deadline, then let the state backend close. On forced process death,
   the unflushed tail may be lost; this local-emulator limitation must be
   documented rather than hidden. Persisted records retain the durability of
   the selected `state.Store` mode.

#### Migration and compatibility

The Phase 1 extraction first reads existing `cloudwatch:metrics` and
`cloudwatch:metricdata` records exactly as today so custom metric users do not
lose visible history. Do not run blocking migration in `router.New()`.

Choose one of these only after characterization benchmarks:

- **Preferred:** repository reads legacy records plus v1 buckets during a
  bounded compatibility window; new writes use v1; the regular sweeper removes
  expired legacy points naturally.
- **If evidence requires it:** a lazy, `sync.Once`-guarded migration runs on
  first metrics query/write and is resumable/idempotent, records schema progress
  in `metrics:meta:v1`, and never prevents the emulator from serving unrelated
  requests.

Tests must cover both storage representations, all store modes, restart
continuity where supported, corrupt records, mixed legacy/v1 query results,
same-minute concurrent observations, and recovery after a failed flush.

### Query and compatibility boundary

Keep custom `PutMetricData` fully supported through the same repository. The
CloudWatch service owns AWS request validation, response serialization,
pagination, alarm behavior, and any future metric math; it delegates metric
identity, writes, retention, and range aggregation to `internal/metrics`.

Start with statistics already supported by the CloudWatch adapter. Do not claim
extended percentile statistics until the repository has a bounded, documented
sample strategy and compatibility tests. Exact pNN over unbounded traffic is
not compatible with aggregate-only storage; either retain bounded per-bucket
samples specifically for metrics that need it, or return the existing AWS-shaped
unsupported behavior. Metric math is likewise a CloudWatch-query phase, not a
service-recorder concern.

## Lambda pilot

### Dimensions and attribution

Use the AWS/Lambda namespace. The base function series has the `FunctionName`
dimension. Where the invocation resolves a version or alias, additionally
record AWS-compatible `Resource` and `ExecutedVersion` dimension combinations
only after documentation/tests establish the exact combinations. Do not invent
an `Overcast*` AWS dimension. Region-wide series have no function dimension;
they should be emitted only where their AWS meaning is known.

Associate every invocation with a compact, internal `InvocationOutcome` built
once at execution completion:

```text
function identity + qualifier/executed version + invoke source/type
+ accepted/throttled + function/runtime error + startedAt + execution duration
+ concurrency snapshot + event-source mapping id/batch outcome (when present)
```

All Lambda entry points must end up here: direct `Invoke` (sync/async),
function URLs, `InvokeWithResponseStream`, runtime API/container results, and
event-source mapping invocations. `DryRun` does not invoke code and must not
emit invocation metrics. A rejected request and a function error are distinct;
do not record `Invocations`/`Errors` for a throttle.

### Pilot catalogue and delivery order

| Tier | Metric                                                                                                          | Collection point / rule                                                                                                                                                            |
| ---- | --------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P0   | `Invocations`                                                                                                   | One per invocation that reaches function code, including function errors; timestamped at invocation start.                                                                         |
| P0   | `Errors`                                                                                                        | One when function code/runtime returns an error or times out; timestamp remains invocation start.                                                                                  |
| P0   | `Duration`                                                                                                      | Execution time in milliseconds after function code begins; excludes cold-start/init time. Store normal statistics.                                                                 |
| P0   | `Throttles`                                                                                                     | One for an invocation rejected for no available concurrency; never also `Invocations` or `Errors`.                                                                                 |
| P0   | `ConcurrentExecutions`                                                                                          | Sample active executions on acquire/release; query with `Maximum`.                                                                                                                 |
| P1   | `AsyncEventsReceived`, `AsyncEventAge`, `AsyncEventsDropped`, `DeadLetterErrors`, `DestinationDeliveryFailures` | Add only with the async queue/retry/DLQ behavior that makes these facts real; do not infer them from HTTP 202.                                                                     |
| P1   | `IteratorAge` and ESM `EventCount`/`ErrorCount` metrics                                                         | Emit from the ESM poll/batch/delete/retry boundaries, gated by `MetricsConfig` exactly as AWS documents. Existing ESM events are helpful UI signals but not sufficient accounting. |
| P2   | Provisioned-concurrency metrics and spillover                                                                   | Add when provisioned concurrency represents actual executable capacity rather than a READY metadata stub.                                                                          |
| P2   | Function URL request metrics and advanced deployment/recursive-loop metrics                                     | Add only when Overcast implements the corresponding AWS behavior and can observe the fact faithfully.                                                                              |

`Duration` needs explicit start/end measurements in the execution layer; the
HTTP handler duration includes parsing, routing, and response writing and is
not a Lambda Duration substitute. Instance acquire/release already provides a
natural concurrency transition, but the outcome helper must use the same
execution identity so duplicate release paths cannot double-count.

## Web UI plan

Add a Lambda function **Monitor** tab inspired by AWS's Lambda Monitoring tab,
without impersonating the AWS console.

1. Add a BFF-owned `/_metrics` read endpoint (or extend the existing BFF API)
   that calls the shared repository with a fixed allowlist of Lambda metric
   definitions. It accepts resource, relative time range, period, and
   statistic—not arbitrary namespace/metric expressions. This avoids exposing
   CloudWatch protocol parsing to the SPA and avoids a parallel storage query.
2. Show four compact summary charts first: invocations/errors, duration
   (average and maximum), throttles, and concurrent executions (maximum).
   Use a one-hour default, period selection compatible with the stored minute
   resolution, accessible labels/tooltips, empty-state copy, and a visible
   local-retention disclaimer.
3. Render no-data as "No metric data in this range," not as failure. Show
   errors as an in-card retry state. Use polling initially; optionally listen
   to a dedicated `metrics:bucket-updated` internal SSE event solely to
   invalidate/refetch visible charts. Never stream raw observations to the
   browser.
4. On the Lambda resource page, provide a link to CloudWatch metric identity
   (namespace, names, dimensions) and later an ESM monitoring panel when ESM
   metrics are implemented. Keep raw invocation history/instance status in
   their existing operational views; charts are aggregation, not a replacement
   for the event timeline.
5. Build one reusable `MetricChart`/`MetricCard` component and typed query
   definitions so SQS/SNS pages supply a catalogue, labels, and statistics
   rather than duplicating fetch/aggregation/chart code.

Do not edit `web/src/routeTree.gen.ts`; let the running dev server regenerate
it, or regenerate through the established route workflow.

## Phase 1 delivered scope (2026-08-22)

This section records what actually shipped against the design above, so the
plan stays accurate as of this commit (AGENTS.md: plan docs are updated
per-commit, not trued up later).

Delivered:

- `internal/metrics`: `Observation`/`Recorder`/`Metric`/`Bucket` types;
  canonical series identity (`sha256(v1 + namespace + name + canonical
  dimensions)`); a sharded in-memory active-bucket layer that is the only
  thing `Observe` touches; a bounded, coalescing background flusher (5s) and
  retention sweeper (5m, 1h retention — the "initially matching the existing
  CloudWatch one-hour retention" option the design calls out); dedicated
  SQLite tables (`metric_series`, `metric_series_dimensions`,
  `metric_buckets`, migration version 40) for `SQLiteStore`/`HybridStore`,
  selected via `state.SQLiteDBProvider` exactly like
  `internal/services/sqs/message_backend.go`'s precedent; an in-memory-only
  backend for `MemoryStore`/`WALStore`/`-tags nosqlite` builds.
- Lambda: `recordInvocationOutcome` (`internal/services/lambda/metrics_lambda.go`)
  is the one shared outcome helper, called from `invokeSync` (sync HTTP,
  function URLs, `InvokeWithResponseStream`, the SSE invoke path all share
  it), `invokeAsyncOnce` (Event/async, including in-process
  `ServiceInvoker.InvokeEvent`), and `ServiceInvoker.Invoke` (the
  event-source-mapping path). Records `AWS/Lambda`
  `Invocations`/`Errors`/`Duration`/`Throttles` with only the `FunctionName`
  dimension (no `Resource`/`ExecutedVersion` yet — the design's own hedge:
  "only after documentation/tests establish the exact combinations").
  `ConcurrentExecutions` is sampled at `InstancePool`'s real admission
  boundary (`admit`'s success path and `decCheckedOutLocked`) via a
  `concurrencyObserver` callback, deliberately excluding provisioned-concurrency
  prewarm and `ProactiveInit`'s own bookkeeping, which reserve capacity rather
  than represent a real concurrent invocation.
- CloudWatch: `metrics_bridge.go` merges `internal/metrics` into
  `ListMetrics`, `GetMetricStatistics` (both protocols), `GetMetricData` (both
  protocols), and the alarm evaluator's own metric-window read — a
  read-through over the **existing, unmodified** `cloudwatchStore`, not a
  storage migration. A metrics-subsystem read failure degrades to "no
  automatic series this call" rather than failing the AWS request.
- Config: `OVERCAST_SERVICE_METRICS=auto|enabled|disabled` (default `auto`).
  `auto` and `enabled` are currently identical because `OVERCAST_SERVICES` no
  longer gates which services are wired, so CloudWatch (and hence collection)
  is always available; see `config.ServiceMetricsAuto`'s doc comment. Wired
  once in `router.New`, with no runtime toggle, matching the design.
- Benchmarks: `internal/metrics/recorder_bench_test.go` (`Observe` hot path,
  disabled-vs-enabled delta, `QueryRange` read cost) and
  `internal/services/lambda/invoke_metrics_bench_test.go` (`invokeSync`
  end-to-end overhead with metrics on vs off). See the PR for recorded
  numbers and machine/mode.

Explicitly deferred to phase 2+ (not started, not partially built):

- SQS/SNS/DynamoDB/API Gateway metric catalogues (design Phase 5).
- Lambda's P1/P2 tiers: `AsyncEventsReceived`/`AsyncEventAge`/
  `AsyncEventsDropped`/`DeadLetterErrors`/`DestinationDeliveryFailures`,
  `IteratorAge` and ESM `EventCount`/`ErrorCount`, provisioned-concurrency
  metrics, and function URL request metrics (design Phase 4 and the pilot
  catalogue's P1/P2 rows).
  `Resource`/`ExecutedVersion` dimensions on the P0 series.
- The web Lambda Monitor tab and its BFF `/_metrics` allowlist endpoint
  (design "Web UI plan" / Phase 3) — deferred because a web UI agent was
  already active on #1104 in the same working tree; no `web/` changes are in
  this phase.
- The 300s/3600s resolution rollup ladder and its 7d/30d retention tiers
  (design "Access patterns, retention, and graph time spans") — phase 1 keeps
  only the 60s/1h tier, which is sufficient for `GetMetricStatistics`/
  `GetMetricData`/alarm evaluation over the ranges those APIs are exercised
  with today; the ladder is UI-chart-scale work with no phase-1 consumer.
- The generic versioned-K/V persistence path for `WALStore`/`-tags nosqlite`
  (`metrics:series:v1`/`metrics:buckets:v1`) — those configurations currently
  get correct, fully in-memory automatic metrics with no restart continuity.
- Migrating `PutMetricData`'s existing storage onto `internal/metrics` (the
  design's "same store" option) — phase 1 took "read-through" instead; a
  future phase could still converge them once the read-through's operational
  cost (double query per API call) is measured against real cardinality.
- The additive-delta, generation-counted active/persisted merge the design's
  "Write, read, failure, and shutdown semantics" section sketches — phase 1
  instead makes each flush a full-value replace (see `internal/metrics/active.go`'s
  `activeBucket` doc comment), which is simpler to prove race-free at the cost
  of the persisted backend never seeing a smaller total than the largest flush
  so far for a bucket (irrelevant in practice since a bucket only grows).

## Phase 2 delivered scope (2026-08-22)

Design Phase 5's service rollout, prioritized per that section's own order
(SQS, then SNS, then services with clear execution boundaries — DynamoDB and
API Gateway). Each service's outcome helper follows the "Architectural
decision" pattern exactly: one narrow per-service helper called at the
authoritative lifecycle boundary, never through `internal/services/cloudwatch`.

Delivered:

- **SQS** (`internal/services/sqs/metrics_sqs.go`), `AWS/SQS`, `QueueName`
  dimension: `NumberOfMessagesSent`/`SentMessageSize` (SendMessage/
  SendMessageBatch, skipped on a FIFO content-based-dedup resend since no
  message is newly enqueued), `NumberOfMessagesReceived`/
  `NumberOfEmptyReceives` (ReceiveMessage, recorded once per call after any
  long-poll retry settles — never once per internal poll attempt),
  `NumberOfMessagesDeleted` (DeleteMessage/DeleteMessageBatch). Both the
  typed and legacy per-operation implementations record identically (SQS's
  two dispatch paths converge on separate handler functions, unlike
  DynamoDB's, so each of the 5 operations has 2 call sites, not 1).
  `ApproximateNumberOfMessagesVisible`/`NotVisible`/`Delayed` are gauges: a
  new periodic sampler (`Service.sampleQueueGauges`, started from
  `InitMetrics`, stopped alongside the existing `watchVisibility` goroutine)
  samples every existing queue once a minute and always records all three —
  including zero — matching AWS's documented behavior of publishing these
  every minute for every active queue regardless of traffic (a real sampled
  fact, not a synthetic zero standing in for a missing observation).
  `ApproximateAgeOfOldestMessage` is recorded only when the queue is
  non-empty — there is no "age of oldest message" fact for an empty queue.
- **SNS** (`internal/services/sns/metrics_sns.go`), `AWS/SNS`, `TopicName`
  dimension: `NumberOfMessagesPublished`/`PublishSize` (Publish/PublishBatch,
  recorded once per successfully-accepted message before fan-out is
  dispatched). `NumberOfNotificationsDelivered`/`NumberOfNotificationsFailed`
  (once per subscription delivery attempt, from `fanOut`'s per-protocol
  success branches and `failDelivery`'s single failure funnel respectively,
  covering all 7 protocol cases including the no-delivery-implementation
  default). A protocol whose delivery dependency was never wired (nil
  enqueuer/mailer/smsSender/outbound) recorded neither Delivered nor Failed
  at the time this phase shipped — `fanOut` `continue`d silently past it
  without calling `failDelivery` — flagged here as a candidate follow-up
  rather than fixed, since it was a behavior change beyond pure metrics
  wiring. Closed by #1306: `fanOut` now runs that case through `failDelivery`
  like any other delivery failure (NumberOfNotificationsFailed, DLQ redirect
  when configured, plus a one-time Warn per topic+protocol), so this is no
  longer a live gap — see `internal/services/sns/handler_publish.go`'s
  `warnUnwiredOnce`.
- **DynamoDB** (`internal/services/dynamodb/metrics_dynamodb.go`), `AWS/DynamoDB`:
  `SuccessfulRequestLatency` (`TableName`+`Operation`, success only),
  `ConsumedReadCapacityUnits`/`ConsumedWriteCapacityUnits` (`TableName`
  [+`GlobalSecondaryIndexName` on Query/Scan when `IndexName` names a real
  GSI, never an LSI] [+`Source=Customer` on writes — global tables are not
  modeled, so every write is a customer write]), `UserErrors` (account/
  region-wide, no dimensions, matching AWS's own dimensionless publication —
  excludes `ConditionalCheckFailedException` and
  `ProvisionedThroughputExceededException` per AWS's documented carve-outs),
  `SystemErrors` (`TableName`+`Operation`, HTTP 5xx). Covers the 10
  operations AWS's own `Operation` dimension documents that this emulator
  implements: PutItem, GetItem, DeleteItem, UpdateItem, BatchGetItem,
  BatchWriteItem, Scan, Query, TransactWriteItems, TransactGetItems
  (TransactWriteItems/TransactGetItems record the 2x transactional-capacity
  weighting AWS documents). `ThrottledRequests`/`ReadThrottleEvents`/
  `WriteThrottleEvents` are **not recorded** — this emulator does not model
  DynamoDB throttling at all, so there is no underlying fact to observe.
  Since DynamoDB has no single dispatch chokepoint that already knows every
  operation's table name/success/timing (both its dispatch paths converge on
  the same "xxxTyped" business-logic function per operation, but the
  generic `op.Typed`/`TypedAny` layer between them never sees `TableName`),
  each instrumented operation's original function was renamed
  "xxxTypedCore" and a same-named "xxxTyped" wrapper (the name both dispatch
  paths already call) records the outcome around it — one wrapper per
  operation rather than Lambda's one function shared by several call sites,
  but still exactly the plan's "one per-service outcome helper" pattern.
- **API Gateway** (`internal/services/apigateway/metrics_apigateway.go`),
  `AWS/ApiGateway`: `Count`, `4XXError`/`5XXError` (REST) or `4xx`/`5xx`
  (HTTP), `Latency`, `IntegrationLatency`. REST (v1) and HTTP (v2) APIs do
  **not** share one execution seam (`ExecuteRestAPI` and `ExecuteV2API` are
  independent top-level dispatchers with different per-integration-type
  sub-functions), so there are two outcome helpers — `recordRestAPIOutcome`
  and `recordV2APIOutcome` — each installed once via a defer at the top of
  its own dispatcher, over a new `statusCapturingResponseWriter` (neither
  dispatcher had a status-capturing writer or a request timer before this
  phase). As of #1307, each helper records under all three AWS-documented
  dimension combinations for its API type — REST: `ApiName`,
  `ApiName`+`Stage`, `ApiName`+`Stage`+`Method`+`Resource`; HTTP: `ApiId`,
  `ApiId`+`Stage`, `ApiId`+`Stage`+`HttpMethod`+`RouteKey` — rather than only
  the most granular combination phase 2 originally shipped; see phase 4's
  "API Gateway Monitor tab" entry below for why. Both skip the wrapper
  allocation and every clock read entirely when collection is disabled
  (`h.metrics == nil`), matching the plan's "disabled collection adds no
  allocations" acceptance criterion. An unresolvable API (unknown
  restApiId/apiId) records nothing when no dimension-able identity was ever
  resolved (REST) — AWS itself never publishes a datapoint it cannot
  dimension.
- Router wiring (`internal/router/router.go`): `sqsSvc.InitMetrics`,
  `snsSvc.InitMetrics`, `ddbSvc.InitMetrics`, `apigwSvc.InitMetrics` added
  alongside phase 1's `lambdaSvc.InitMetrics`, all gated by the same
  `cfg.MetricsCollectionEnabled()` check — no new configuration surface.
- CloudWatch (`internal/services/cloudwatch`): **zero changes**. Phase 1's
  read-through (`metrics_bridge.go`) and alarm evaluator are namespace- and
  metric-shape-agnostic, so all four new namespaces are served by the exact
  code that already served `AWS/Lambda` — proven by
  `TestSQSApproximateNumberOfMessagesVisibleAlarm_FiresFromAutomaticMetrics`
  in `metrics_bridge_test.go`.
- Tests: a `metrics_*_test.go` per service driving the real operation
  through a real `*metrics.Service` (never a stub) and reading it back via
  `metrics.Service.QueryRange` — the same call CloudWatch's read-through
  itself makes — plus one alarm-fires acceptance test in the cloudwatch
  package. Benchmarks: a `metrics_*_bench_test.go` per service (`-benchmem`,
  collection disabled vs enabled) on each service's representative hot path
  (SendMessage, Publish, PutItem, ExecuteRestAPI). See the PR for recorded
  numbers and machine/mode.

Explicitly deferred to phase 3+ (not started, not partially built):

- The web Lambda Monitor tab and its BFF `/_metrics` allowlist endpoint,
  and the equivalent SQS/SNS/DynamoDB/API Gateway monitor panels (design
  "Web UI plan" / Phase 3) — no `web/` changes are in this phase.
- The 300s/3600s resolution rollup ladder and its 7d/30d retention tiers —
  phase 2 keeps only the 60s/1h tier phase 1 shipped.
- Lambda's P1/P2 tiers (`AsyncEventsReceived`/`AsyncEventAge`/
  `AsyncEventsDropped`/`DeadLetterErrors`/`DestinationDeliveryFailures`,
  ESM `IteratorAge`/`EventCount`/`ErrorCount`, provisioned-concurrency
  metrics, function URL request metrics) and `Resource`/`ExecutedVersion`
  dimensions — unchanged from phase 1's own deferral.
- The generic versioned-K/V persistence path for `WALStore`/`-tags nosqlite`
  — unchanged from phase 1's own deferral; phase 2's new SQS/SNS/DynamoDB/
  API Gateway series inherit the same in-memory-only behavior there.
- DynamoDB throttling modeling (and, downstream of it,
  `ThrottledRequests`/`ReadThrottleEvents`/`WriteThrottleEvents`) — no
  underlying fact exists to observe yet.
- ~~SNS's silent-`continue` gap for an unwired delivery dependency (see
  "Delivered" above) — a candidate follow-up, not phase-2 scope.~~ Closed by
  #1306 — no longer deferred.
- API Gateway's coarser documented dimension combinations (`ApiName+Stage`
  only, etc.) — only the most granular combination is recorded, mirroring
  Lambda phase 1's `FunctionName`-only narrowing.
- Migrating `PutMetricData`'s own storage onto `internal/metrics` — unchanged
  from phase 1's own deferral.

## Phase 3 delivered scope (2026-08-22)

Design "Web UI plan" and the resolution-ladder half of "Access patterns,
retention, and graph time spans", covering the remainder PR #1268 (phase 2)
listed.

Delivered:

- **Resolution rollup ladder** (`internal/metrics/rollup.go`): the design's
  full 60s/24h, 300s/7d, 3600s/30d table (`resolutionTiers`), replacing phase
  1's single 60s/1h tier — extending the fine tier's own retention was needed
  because the design keeps the 6h and 24h chart controls at 1-minute
  resolution (`resolutionTiers`'s comment explains why). A background job
  (`rollupOnce`, run from the existing sweep tick) derives each coarser tier
  from the next-finer persisted tier (never from the active overlay, which
  only ever holds 60s buckets) using the same `bucketBackend.rangeQuery`/
  `upsertBuckets` calls the fine tier already uses — no second storage engine.
  Each rollup window is a full-value replace, not an additive delta (mirroring
  `activeBucket`'s own doc comment), which is what makes recomputing a
  trailing handful of windows every tick (`rollupSpec.catchUpWindows`)
  self-healing against a missed tick or late flush without double-counting.
  An empty window (no fine buckets in it) writes nothing — the plan's "a
  missing metric means no observation was emitted, not a synthetic zero" rule
  applied to the rollup ladder itself, not only to the fine tier.
- **Query planner** (`internal/metrics/planner.go`): `SelectResolution`
  implements "the finest resolution whose retention fully covers the
  requested interval"; `ParseChartRange` implements the design's exact five
  coherent controls (`1h/1m, 6h/1m, 24h/5m, 7d/5m, 30d/1h`), rejecting any
  other range token rather than silently substituting a default;
  `(*Service).QueryAuto` combines both, and — when the caller's requested
  display period is coarser than the resolution actually available for that
  age — regroups the returned buckets at read time
  (`aggregateIntoPeriods`, sharing `rollup.go`'s own `sumBuckets` composition
  logic) rather than requiring the persisted rollup ladder to have already
  caught up to exactly that period. The existing `QueryRange`
  (`internal/services/cloudwatch`'s only read call) is unchanged in signature
  and behavior — it is now a thin wrapper calling the new
  resolution-parameterized `queryRangeAt` at the fixed 60s tier, so
  CloudWatch's `GetMetricStatistics`/`GetMetricData`/alarm evaluator needed
  **zero changes** for phase 3, exactly as phase 2 needed none for its four
  new namespaces.
- **Chart read API** (`internal/metrics/chart.go`): `Bucket.Value(statistic)`
  derives `Sum`/`Average`/`Minimum`/`Maximum`/`SampleCount` from a stored
  aggregate; `(*Service).ChartQuery` combines this with `QueryAuto` to answer
  one (metric, statistic) series directly as chart-ready `ChartPoint`s — the
  narrow read surface every service's Monitor endpoint calls.
- **Monitor endpoint assembly** (`internal/metrics/monitor.go`):
  `MonitorCatalogEntry`/`MonitorResponse`/`MonitorSeries` and
  `BuildMonitorResponse` are the shared allowlist-response shape and assembly
  logic every service's `GET /_overcast/<service>/.../metrics` handler calls
  with its own fixed catalogue — the backend analog of the Web UI plan's
  "Build one reusable MetricChart/MetricCard component" instruction. A nil
  reader (collection disabled) answers `{"enabled": false}`, never an error;
  an unrecognized `range` token answers 400, never a silently-substituted
  default.
- **Lambda**: `GET /_overcast/lambda/functions/{name}/metrics`
  (`internal/services/lambda/handler_metrics.go`), serving the P0 pilot
  catalogue (`Invocations`/`Errors`/`Duration` average+maximum/`Throttles`/
  `ConcurrentExecutions` maximum) dimensioned by `FunctionName`. The Lambda
  function detail page's existing "Monitor" tab — previously a log viewer
  only (a naming collision from #1104, unrelated to this plan) — now shows
  four metric cards above the log viewer it already had; the log viewer
  itself is unchanged.
- **SQS**: `GET /_overcast/sqs/queues/{name}/metrics`
  (`internal/services/sqs/handler_metrics.go`), serving 7 series (send/
  receive/delete/empty-receive counts, visible/not-visible queue-depth
  gauges as `Average`, oldest-message age as `Maximum`) dimensioned by
  `QueueName`. SQS's queue detail page had no tab system before this phase;
  a third tab ("Monitor", alongside the existing hand-rolled Messages/SNS
  Subscriptions tabs) was added rather than introducing the shared `Tabs`
  component there for the first time, to keep the change's blast radius
  contained to this feature.
- **Web** (`web/src/features/monitoring/`): `MetricLineChart` (a reusable
  inline-SVG multi-series line chart — fixed categorical color order from the
  repo's own `--cat-1`…`--cat-5` ramp, thin 2px lines, gap-aware segment
  splitting so a missing period is never bridged by a straight line, a hover
  crosshair+tooltip, a legend for 2+ series) and `MonitorPanel` (the range
  selector, per-card grid, loading/error states via the shared
  `QueryListState`, the disabled-collection state, and the visible
  local-retention disclaimer the design calls for) are shared by both
  Lambda's tab and SQS's section. `cmd/tsgen`'s manifest gained
  `MonitorResponse`/`MonitorSeries`/`ChartPoint`.
- **BFF** (`internal/bff/bff.go`): `GET /api/lambda/functions/{name}/metrics`
  and `GET /api/sqs/queues/{name}/metrics` are thin proxies to the emulator
  endpoints above, forwarding only `range` and the region header — the SPA
  never constructs or parses CloudWatch protocol, matching the design's "This
  avoids exposing CloudWatch protocol parsing to the SPA" rationale.
- Tests: `internal/metrics/rollup_test.go` (rollup composition, the
  synthetic-zero rule, hourly-from-five-minute composition across simulated
  sweep ticks, idempotent recomputation, `SelectResolution`/`ParseChartRange`/
  `QueryAuto`/`ChartQuery`, plus a dedicated SQLite-backend rollup test — see
  `tests/AGENTS.md` § Build-tag-sensitive tests for why it's skipped rather
  than tagged out under `-tags nosqlite`); `handler_metrics_test.go` per
  service against a real `*metrics.Service` (never a stub, matching phase 2's
  own convention); `internal/bff/metrics_test.go` for the proxy layer; web
  component tests for `MetricLineChart`'s empty/gap/legend behavior and
  `MonitorPanel`'s loading/error/disabled/populated states. No benchmarks were
  added: the rollup ladder runs entirely off the existing background sweep
  tick, not the `Observe`/`Flush` recording hot path phase 1/2's benchmarks
  already cover.

Explicitly deferred to phase 4 (not started, not partially built):

- Lambda's P1/P2 tiers (`AsyncEventsReceived`/`AsyncEventAge`/
  `AsyncEventsDropped`/`DeadLetterErrors`/`DestinationDeliveryFailures`, ESM
  `IteratorAge`/`EventCount`/`ErrorCount`, provisioned-concurrency metrics,
  function URL request metrics) and `Resource`/`ExecutedVersion` dimensions —
  unchanged from phase 1/2's own deferral.
- The generic versioned-K/V persistence path for `WALStore`/`-tags nosqlite`
  builds — unchanged from phase 1/2's own deferral; those configurations get
  correct in-memory-only automatic metrics (including the phase 3 rollup
  ladder, which runs the same in-memory backend fine) with no restart
  continuity.
- Migrating `PutMetricData`'s own storage onto `internal/metrics` — unchanged
  from phase 1/2's own deferral.
- DynamoDB throttling modeling, SNS's silent-`continue` delivery-dependency
  gap, and API Gateway's coarser documented dimension combinations —
  unchanged from phase 2's own deferral; none are web-UI or storage-tier
  concerns this phase touches.
- SNS/DynamoDB/API Gateway Monitor tabs/sections — phase 3 built Lambda and
  SQS only, per this phase's scope; the same `MonitorPanel`/`MetricLineChart`
  components and `internal/metrics/monitor.go` assembly are designed to be
  reused by a future phase's SNS/DynamoDB/API Gateway catalogue and handler,
  not rebuilt.
- A `metrics:bucket-updated` SSE event to invalidate/refetch visible charts —
  the Web UI plan's "optionally listen to" alternative to polling; phase 3
  ships the simpler always-poll (30s) behavior only.

## Phase 4 delivered scope (2026-08-23)

Refs #1181. Picks up phase 3's explicit deferral list in value order: Lambda's
async P1 metric tier, the WALStore/`-tags nosqlite` persistence gap every
prior phase disclosed, then SNS/DynamoDB Monitor tabs reusing phase 3's own
components. Does not close #1181 as "fully complete" — see the status header
for what remains and where it is tracked (#1305, #1306, #1307).

Delivered:

- **Lambda async P1 tier** (`internal/services/lambda/metrics_lambda.go`'s
  "Async invocation P1 tier" section): `AsyncEventsReceived` (`startAsync`,
  once per accepted Event invocation — never for one refused during shutdown,
  since that is this emulator's own process-lifecycle behavior rather than
  the AWS queue-acceptance fact the metric describes), `AsyncEventAge`
  (`invokeAsync`, once per attempt, the elapsed time since acceptance —
  already computed for `eventAgedOut`'s own comparison, not a new
  measurement), `AsyncEventsDropped` (`invokeAsync`'s `eventAgedOut` branch —
  the one case this emulator actually discards an event unrun), and
  `DeadLetterErrors`/`DestinationDeliveryFailures` (the delivery-error
  branches of `deadLetterAsyncFailure`/`deliverAsyncDestination`, both of
  which already logged "the event/record is dropped" but never metered it).
  ESM's own P1 row (`IteratorAge`, `EventCount`, `ErrorCount`) is **not**
  delivered — it needs each event source mapping's `MetricsConfig` opt-in
  parsed and gated on, and that field is today only a stored/echoed raw JSON
  blob (`internal/services/lambda/handler_esm.go`); tracked as #1307 alongside
  Lambda's P2 tier and `Resource`/`ExecutedVersion` dimensions, all unchanged
  since phase 1's own deferral.
- **Generic K/V-backed metrics persistence for WALStore/`-tags nosqlite`**
  (`internal/metrics/kv_backend.go`): a third `bucketBackend` implementation,
  selected in `newBucketBackendFor` for `*state.WALStore` — and hence every
  `-tags nosqlite` build, since `SQLiteStore`/`HybridStore` don't exist under
  that tag and WALStore or MemoryStore is all that remains — writing through
  `state.Store`'s own `Get`/`Set`/`Scan` surface using the plan's own
  versioned namespaces (`metrics:series:v1`, `metrics:buckets:v1`). A
  WALStore's append-log replay on startup is what makes a bucket flushed
  through this backend before a restart visible again immediately after one;
  `TestKVBackend_SurvivesRestart` proves exactly that end to end (Observe →
  Stop → close the store → reopen the same data dir → a fresh
  `metrics.Service` sees the prior observation with no new `Observe` call).
  `MemoryStore` deliberately keeps the existing in-memory `memBucketBackend`
  — there is nothing for it to restart into, so the JSON round-trip would be
  pure overhead with no durability benefit; this is the "tier-aware" part of
  the plan's own phrasing, not an oversight. Bucket writes stay a full-value
  replace (matching `activeBucket`'s existing contract, and `sqlBucketBackend`'s
  own upsert shape); the one disclosed compatibility gap is that a crash
  mid-flush-batch can leave a bucket persisted without its series record (or
  vice versa) — self-healing on the next successful flush, per the plan's own
  "K/V fallback must tolerate an interrupted batch" allowance, since
  `sqlBucketBackend`'s single transaction has no K/V equivalent.
- **SNS Monitor tab** (`internal/services/sns/handler_metrics.go`,
  `GET /_overcast/sns/topics/{topicName}/metrics`): the full phase 2 catalogue
  (`NumberOfMessagesPublished` Sum, `PublishSize` Average+Maximum,
  `NumberOfNotificationsDelivered`/`NumberOfNotificationsFailed` Sum)
  dimensioned by `TopicName` — the same single-dimension-set shape Lambda/SQS
  already used, so no changes to `internal/metrics/monitor.go` were needed.
  The topic detail page had no tab system before this phase (unlike SQS,
  which already had one for phase 3 to extend); a `Subscriptions`/`Monitor`
  pair using the shared `Tabs`/`TabList`/`Tab`/`TabPanel` component
  (`web/src/components/ui/tabs.tsx`) was introduced fresh, rather than
  hand-rolling a third one-off tab bar to match SQS's own (pre-existing,
  now-established) precedent.
- **DynamoDB Monitor tab** (`internal/services/dynamodb/handler_metrics.go`,
  `GET /_overcast/dynamodb/tables/{name}/metrics`): table-scoped, capacity
  metrics only — `ConsumedReadCapacityUnits` (`TableName` only) and
  `ConsumedWriteCapacityUnits` (`TableName`+`Source=Customer`, matching
  `recordConsumedWriteCapacity`'s always-added dimension). This is the
  narrower of the two catalogues this phase built, and deliberately so:
  DynamoDB's other AWS/DynamoDB metrics (`SuccessfulRequestLatency`,
  `UserErrors`, `SystemErrors`) are dimensioned by `Operation` or published
  with *no* dimensions at all (account/region-wide — see
  `metrics_dynamodb.go`'s file doc comment), neither of which fits a single
  per-table series the way the two capacity metrics do; charting them would
  mean either one line per operation or a coarser aggregate that was never
  recorded. `MonitorCatalogEntry` gained an `ExtraDimensions` field
  (`internal/metrics/monitor.go`) purely to let `ConsumedWriteCapacityUnits`
  add its `Source` dimension on top of the per-request base `TableName`
  dimension `BuildMonitorResponse` otherwise applies uniformly across a
  catalogue — the first catalogue phase 3's original single-dims-for-the-whole-catalogue
  shape didn't already fit; Lambda/SNS/SQS all still pass no
  `ExtraDimensions` and are unaffected.
- **API Gateway Monitor tab**: **delivered as #1307**. Phase 2 (#1268)
  deliberately recorded only the most granular documented dimension
  combination per request (REST: `ApiName+Stage+Method+Resource`; HTTP:
  `ApiId+Stage+HttpMethod+RouteKey`), never AWS's own coarser
  `ApiName+Stage`-only (or `ApiId+Stage`-only) aggregate — so there was no
  series a per-API dashboard could query without either charting one line
  per route (a catalogue that grows with the API's own route count, unlike
  every other service's small fixed catalogue) or building a genuinely new
  capability: either a second, coarser recorded series, or cross-series
  aggregation in `internal/metrics`'s query layer (`ChartQuery`/`QueryAuto`
  resolve one exact dimension set per call). #1307 resolved this the first
  way — `recordRestAPIOutcome`/`recordV2APIOutcome` now record every
  AWS-documented combination at observe time (see the phase 2 entry above) —
  so `GET /_overcast/apigateway/restapis/{apiId}/metrics` and
  `GET /_overcast/apigateway/apis/{apiId}/metrics`
  (`internal/services/apigateway/handler_metrics.go`) can each query exactly
  one dimension set per catalogue entry through the same
  `metrics.BuildMonitorResponse` every other service's Monitor endpoint
  uses, with an optional `?stage=` query parameter selecting the
  `{ApiName}`/`{ApiId}`-only series versus the `+Stage` series. HTTP APIs'
  error metrics were also corrected from the previously-recorded (and
  incorrect) `4XXError`/`5XXError` to AWS's real `4xx`/`5xx` names as part of
  the same change.
- **`PutMetricData` storage migration onto `internal/metrics`: evaluated,
  not done.** The plan's own text permits deferring this "once the
  read-through's operational cost (double query per API call) is measured
  against real cardinality" — no such measurement exists, and phases 2/3
  since shipped five namespaces and the full resolution rollup ladder through
  the existing read-through (`internal/services/cloudwatch/metrics_bridge.go`)
  with **zero** changes to CloudWatch's own storage engine or its
  already-extensive test suite. Migrating now would touch that tested engine
  for a benefit nothing has measured; the read-through remains the safer,
  equally-correct option until a concrete performance problem is found.
- **DynamoDB throttling modeling** and **SNS's `fanOut` silent-`continue`
  delivery-dependency gap** — both disclosed since phase 2, filed as their own
  issues this phase (#1305, #1306) with RICE scores per
  `docs/plans/backlog-rice.md` §2, rather than left as an implicit note in
  this plan doc.
- Tests: `internal/metrics/kv_backend_test.go` (restart continuity),
  `backend_test.go`'s parity test gained a `"wal"` entry;
  `internal/services/lambda/metrics_lambda_async_test.go` (all five async
  metrics, including the shutdown-refusal exclusion);
  `internal/services/sns/handler_metrics_test.go` and
  `internal/services/dynamodb/handler_metrics_test.go` (new, following phase
  3's per-service Monitor-endpoint test convention exactly — a real
  `*metrics.Service`, never a stub). Verified under the default build, `-tags
  slim,dev`, `-tags slim,nosqlite`, and `-race` (via `docker-go.sh` on
  `golang:1.25-bookworm`, since `go.mod` requires Go >= 1.25 and the
  devcontainer's pinned `golang:1.24-bookworm` image predates that). Web:
  `pnpm typecheck`/`lint`/`test` all clean; screenshots of the SNS Monitor tab
  in light and dark, captured from a Docker image built off this branch with
  a real seeded topic/publish and table/item (see the PR for the images).

## Implementation phases

### Phase 0 — evidence and contracts

- Create a Lambda/CloudWatch compatibility tracking item and update the
  per-service compatibility files before implementation.
- Read the linked AWS documentation for each metric and record its namespace,
  units, dimensions, statistic, emission timing, and inclusion/exclusion rule.
- Inventory every Lambda invocation path and create a test matrix covering
  success, function error, runtime/init error, timeout, reserved-concurrency
  throttle, direct/URL/stream invocation, async retries, and SQS/DynamoDB ESM.
- Benchmark the current CloudWatch metric write/query paths and establish pilot
  budgets (p50/p99 `Observe`, allocations/op, flush throughput, and query time
  versus number of retained buckets). Record machine, storage mode, command,
  and data shape with every performance claim.

### Phase 1 — extract the generic metric repository

- Characterize the current CloudWatch behavior with failing/characterization
  tests for custom metric writes, dimension ordering, bucket aggregation,
  range boundaries, retention, malformed records, pagination, and all storage
  modes.
- Move types, canonicalization, keys, retention sweep, and query aggregation
  into `internal/metrics` without changing CloudWatch wire bytes. Inject the
  repository into `cloudwatch.New` rather than importing CloudWatch from any
  service.
- Add minute-bucket aggregation and batched flushing behind the repository;
  prove queries merge dirty and persisted buckets and shutdown flushes safely.
- Maintain a compatibility read/migration path for existing custom metric
  state. Verify CloudWatch `PutMetricData` and Lambda automatic observations
  coexist in `ListMetrics` and query APIs.

### Phase 2 — Lambda P0 recording

- Define `lambdaMetrics` as a small dependency injected into the handler /
  invoker execution path. Implement one outcome builder and route every
  invocation mechanism through it.
- Add failing unit tests for classification and timestamping, plus SDK-level
  integration tests that invoke a real local Lambda fixture then query
  CloudWatch APIs. Test counts, duration statistics, dimensions, timestamps,
  and the throttle exclusion rule.
- Add concurrency sampling around instance acquire/release and race-test it.
  Ensure cancellation, container failure, and shutdown cannot leak an active
  concurrency gauge or double-release.

### Phase 3 — Lambda monitor experience (shipped 2026-08-22)

- Implement the BFF query allowlist and typed web client.
- Add reusable chart components, the function Monitor tab, tests for query
  construction/loading/no-data/error states, and an accessibility review.
- Compare the layout and terminology with the relevant AWS Lambda monitoring
  pages while retaining Overcast styling and explicit local limits.

Shipped for Lambda and SQS (the SQS Monitor section was not originally
specified by this phase's own heading, but the reusable component split
["Build one reusable `MetricChart`/`MetricCard` component..."] made adding it
alongside Lambda's tab low-marginal-cost) — see "Phase 3 delivered scope"
above for the exact endpoint shapes, catalogue, and disclosed remainder.

### Phase 4 — async and ESM metrics

- First implement/review the queue, retry, destination, and ESM facts that the
  metrics require; then emit only the documented metrics from those boundaries.
- Respect ESM `MetricsConfig` opt-in and source-specific availability. Add
  scenario tests for empty polls, filters, batches, retries, delete success /
  failure, disabled mappings, and destination failure.

### Phase 5 — service rollout (shipped 2026-08-22)

For each service, add a declarative metric catalogue plus a thin local adapter
rather than new infrastructure. Prioritize SQS (queue depth/age/send/receive/
delete and DLQ transitions from authoritative state transitions), then SNS
(publish/delivery/failure outcomes), followed by services with already clear
execution boundaries. Each rollout requires AWS docs evidence, a scenario
matrix, SDK/API query tests, UI catalogue entries where useful, and capability
documentation updates.

Shipped for SQS, SNS, DynamoDB, and API Gateway — see "Phase 2 delivered
scope" above for the exact catalogue, dimensions, and disclosed narrowing per
service. UI catalogue entries remain phase 3 (no `web/` changes shipped with
this rollout).

## Acceptance criteria and guardrails

- No service imports `internal/services/cloudwatch`; all use injected
  `metrics.Recorder` or a service-local narrow adapter.
- CloudWatch continues to return AWS-shaped protocol responses. No custom
  fields or endpoints appear on AWS routes.
- The recorder hot path has bounded memory and does not perform state I/O or
  create goroutines. The flusher/sweeper has deterministic shutdown and no
  goroutine leaks.
- One failed/corrupt metric record does not fail unrelated series or service
  requests; storage failures are surfaced to logs/health and tested.
- Lambda P0 series agree with direct invocation outcomes across all supported
  invocation paths; rejected throttles are never counted as errors or
  invocations.
- The web UI and CloudWatch APIs query the same repository and display the
  same aggregation for a given range/statistic.
- Documentation states what is implemented, intentionally partial, and absent;
  `make docs` is run whenever capability tables change.

## AWS references

- [Using CloudWatch metrics with Lambda](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics.html)
- [Types of Lambda metrics](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics-types.html)
- [Viewing Lambda metrics and dimensions](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics-view.html)
- [Lambda concurrency monitoring](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-concurrency.html)
- [CloudWatch GetMetricData API](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html)
- [Available CloudWatch metrics for Amazon SQS](https://docs.aws.amazon.com/AmazonSQS/latest/dg/sqs-available-cloudwatch-metrics.html)
- [Monitoring Amazon SNS topics using CloudWatch](https://docs.aws.amazon.com/sns/latest/dg/sns-monitoring-using-cloudwatch.html)
- [DynamoDB metrics and dimensions](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/metrics-dimensions.html)
- [Amazon API Gateway metrics and dimensions](https://docs.aws.amazon.com/apigateway/latest/developerguide/apigateway-metrics-and-dimensions.html)
