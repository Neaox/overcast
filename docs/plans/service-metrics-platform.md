# Service metrics platform plan (Lambda pilot)

> **Status:** proposed
>
> **Scope:** an AWS-shaped metrics substrate shared by every emulated service,
> delivered first for Lambda and designed for subsequent SQS, SNS, and service
> integrations. This is a plan, not an API commitment.

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

The query planner selects the finest resolution whose retention fully covers
the requested interval, then uses a period that is a multiple of that
resolution. The UI exposes only coherent controls: **1h/1m, 6h/1m, 24h/5m,
7d/5m, and 30d/1h**. It asks for a complete half-open UTC interval and the
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

### Phase 3 — Lambda monitor experience

- Implement the BFF query allowlist and typed web client.
- Add reusable chart components, the function Monitor tab, tests for query
  construction/loading/no-data/error states, and an accessibility review.
- Compare the layout and terminology with the relevant AWS Lambda monitoring
  pages while retaining Overcast styling and explicit local limits.

### Phase 4 — async and ESM metrics

- First implement/review the queue, retry, destination, and ESM facts that the
  metrics require; then emit only the documented metrics from those boundaries.
- Respect ESM `MetricsConfig` opt-in and source-specific availability. Add
  scenario tests for empty polls, filters, batches, retries, delete success /
  failure, disabled mappings, and destination failure.

### Phase 5 — service rollout

For each service, add a declarative metric catalogue plus a thin local adapter
rather than new infrastructure. Prioritize SQS (queue depth/age/send/receive/
delete and DLQ transitions from authoritative state transitions), then SNS
(publish/delivery/failure outcomes), followed by services with already clear
execution boundaries. Each rollout requires AWS docs evidence, a scenario
matrix, SDK/API query tests, UI catalogue entries where useful, and capability
documentation updates.

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
