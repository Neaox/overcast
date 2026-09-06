# Lambda in-container init: own the runtime's stdout — plan

> Status: **complete** 2026-08-24 — Phase 0 (#1404, shared
> `internal/containerlogs` follower; ECS adopts it), Phase 1 (#1405, the init
> binary, `initproto`, `initbin` embed, build plumbing), the cursor fix found
> while baselining (#1406), Phase 2 (#1407, the cut-over: every Lambda
> container runs under the init; exact tails and CloudWatch ordering; the
> tail/drain wait machinery of #873/#1160/#1325/#1402 deleted) and Phase 3
> (the `Observer` transitional hook removed, stale comments fixed, the
> bare-checkout `go test` contract restored via `initbin.EnvDistDir`).
>
> What shipped differs from the original design in three measured ways: the
> per-cold-start archive copy was +273–376 ms on Docker Desktop (~20 MB/s
> CopyToContainer), so a **seeded, content-addressed volume** delivers the
> init (cold p50 −13 to +13 ms vs before; one ~580 ms seed per process;
> archive copy retained as a logged fallback); the ordering invariant covers
> **both ends** of an invocation (`X-Overcast-Log-Seq` on `/next` as well as
> `/response`, so INIT-phase and between-invocation output precedes the next
> START); and the init's ~0.3 ms CPU per invoke comes out of the function's
> CFS quota, so 128 MB functions see ~1 in 8 invokes stall ~80 ms in
> sustained back-to-back bursts — faithful to AWS, documented in
> docs/services/lambda.md. End-to-end evidence: the image e2e scores 19/19
> against this architecture vs 16/18 for daemon read-back on the same script,
> with `docker logs` byte-identical via the tee.
>
> **Extended 2026-09-06 (#1799):** the Telemetry API's deliveries now travel
> the same way, over a channel the init opens (§ 3.5). That was the last path
> in Lambda that ran host-to-sandbox, and the last reason a container's bridge
> IP had to be routable from the Overcast process — so extension telemetry
> works on Docker Desktop, which is most developer machines.
>
> This document was written straight after #1402,
> the fourth round of fixes to the `X-Amz-Log-Result` tail wait (#873, #1160,
> #1325, run 32622332545), when the question "is this a flaw in the
> architecture we chose?" was answered *yes*.
>
> Goal: make per-invocation log attribution — and therefore the
> `LogType: Tail` result, CloudWatch ordering and the Telemetry API's view of
> an invocation — **exact and clock-free**, by adopting the process model AWS
> itself uses: an init process inside the container that is the parent of
> the runtime, owns its stdout/stderr, and proxies the Runtime API so it
> knows where each invocation begins and ends. Do it without adding work to
> the invoke hot path, without a second code path for image functions, and
> while deleting the heuristic machinery the current architecture needed.
> Guiding principles: one log transport per container kind, not two; every
> bound replaced by an event; measured, not asserted, performance; shared
> infrastructure for every Docker user in the repo, Lambda-specific only
> where Lambda is genuinely specific.

---

## 1. The problem, and why it is structural

Today a Lambda container's stdout goes to Docker's log driver and Overcast
reads it back over `GET /containers/{id}/logs?follow=true`
(`containerInstance.streamLogs`). START / END / REPORT are written by Overcast
itself. The tail for a `LogType: Tail` invoke is assembled from those two
sources, which share no clock and no sequence. Nothing in that path can say
"this invocation's output ends here": the Runtime API response arrives on one
channel, the bytes on another, and Docker's log stream has no per-request
boundaries. So "done" can only be *inferred* — silence plus a bound — and the
four rounds of fixes have been about making that inference use the right
evidence:

| Round | State that was being timed as "the handler printed nothing" | Fix |
| --- | --- | --- |
| #873 | reader between Reads (away from the stream) | measure silence from when Docker was asked |
| #1160 / #1166 | stream never connected | do not time an unanswered first connect |
| #1325 / #1331 | reader parked in a live Read on an open stream | wider bound than a reconnect backoff |
| run 32622332545 / #1402 | first connect answered late; absolute deadline cut it | measure the bound from the last pipeline event |

After #1402 the only bound left over a genuinely unknowable case is
`parkedReadMax` (80 ms): a handler that printed nothing is indistinguishable
from one whose line is still in Docker's pipe. That is the irreducible part,
and it can only be removed by owning the pipe.

What the current choice buys, and why it was defensible: any image runs
unmodified, nothing of Overcast's runs inside the container, non-tail invokes
pay nothing, and CloudWatch delivery is decoupled from the response exactly as
on AWS. What it costs, beyond the tail: every warm container holds one
long-lived daemon HTTP connection for its log follower (against the client's
64-connection transport cap — `maxDockerConns` exists because of this), a
reconnect/backoff/cursor-dedup/reconcile-on-close pipeline to make that
follower robust, ~1.7 k lines of wait machinery and mock-clock tests, and a
tail that is best-effort under contention.

## 2. Reference model: how AWS and LocalStack do it

**AWS.** Lambda does not run a container image the way Docker does. The image's
filesystem is unpacked into the microVM and the init process — RAPID, of which
`aws-lambda-rie` is the open-sourced emulator — launches `ENTRYPOINT + CMD` as
a child. The process tree is always

```
RAPID  (init; serves the Runtime API on 127.0.0.1:9001; owns the child's stdout/stderr pipes)
 └─ <image or ImageConfig ENTRYPOINT> <CMD>
```

so a custom entrypoint is simply "the command the init runs". The only contract
is that something in that process speaks the Runtime API. `LogResult`,
`platform.*` telemetry events, START/END/REPORT ordering and per-request log
attribution all come from RAPID being the parent. The base images'
`/lambda-entrypoint.sh` is exactly this in shell: if `AWS_LAMBDA_RUNTIME_API`
is unset, exec RIE to play RAPID locally; otherwise exec the runtime
bootstrap, because RAPID is already our parent.

**LocalStack (v2 Lambda provider).** Forked RIE into its own init binary,
copies it into the container over the image's entrypoint, passes the original
`ENTRYPOINT + CMD` as the child command, and the init POSTs each invocation's
collected logs to LocalStack's executor endpoint and *then* the result.
CloudWatch and the tail are fed from that, not from `docker logs`. It pays one
extra round trip per invoke for the ordering guarantee, tail or not.

**Overcast today** already does half of RAPID's job: it plays the Runtime API
*server* (`RuntimeAPIServer`, one `containerListener` per execution
environment, container identity by listening port + remote IP) and already
injects a bootstrap script for zip functions (`/var/overcast/bootstrap`, copied
in via the provisioning archive, which also pipes extensions' stdout through a
`[overcast-extension:<name>]` prefix). It does not play the *parent process*.
This plan brings that role in-container.

## 3. Target architecture

```
host (Overcast)                                  container
────────────────────────────────────────────     ──────────────────────────────────────────────
RuntimeAPIServer                                 /var/overcast/init   (PID 1, static Go, linux/{amd64,arm64})
  containerListener (per environment)     ◄──►     • Runtime API proxy on 127.0.0.1:9001
    /2018-06-01/runtime/*   (as today)               forwards to OVERCAST_RUNTIME_API=<host addr:port>
    /2020-01-01/extension/* (as today)             • parent of the runtime: exec ENTRYPOINT+CMD,
    /overcast/v1/logs  ◄── long-lived NDJSON          stdout/stderr pipes owned here
                            frame stream           • parent of extensions (/opt/extensions/*), same
    /overcast/v1/telemetry ◄── long poll,          • log shipper: one connection, ordered frames,
                    batch handed back (§ 3.5)        seq numbers; drain-then-forward on /response
containerInstance                                  • telemetry relay: long-polls the host, POSTs each
  logSink: per-request buffers, CWL batcher          batch to the extension's own listener (§ 3.5)
  telemetryRelay: per-environment handoff          • PID 1 duties: reap, forward SIGTERM, exit code
  invoke: await seq ≤ X-Overcast-Log-Seq
```

### 3.1 The init (`cmd/lambda-init`, linux-only, stdlib only)

1. **Spawn the runtime** as a child with stdout/stderr pipes. The child command
   is whatever the container would have run without us: for zip functions
   `/lambda-entrypoint.sh <handler>`; for image functions the image's
   `ENTRYPOINT + CMD` (or `ImageConfig` overrides). `WORKDIR`/`USER` are Docker's
   job and unchanged — the init runs as the container's user and inherits
   its cwd. Set `AWS_LAMBDA_RUNTIME_API=127.0.0.1:9001` for the child, exactly
   the value AWS sets.
2. **Spawn extensions** from `/opt/extensions/*` the same way, each with its own
   pipes tagged `ext:<name>`. This replaces the bootstrap shell script and its
   prefix convention (§8).
3. **Proxy the Runtime API** on `127.0.0.1:9001` to the host's per-environment
   endpoint (env `OVERCAST_RUNTIME_API`). Pure pass-through for everything —
   including the extensions API — except that the proxy *observes*:
   - the `Lambda-Runtime-Aws-Request-Id` header on each `GET /next` response →
     the current request ID for attribution;
   - `POST /response` and `POST /error` for a request → **drain, then
     forward** (§3.3).
4. **Ship logs** over one long-lived, chunked `POST /overcast/v1/logs` to the
   same host endpoint, as newline-delimited JSON frames:
   `{"seq":n,"req":"<request-id>|""","src":"stdout|stderr|ext:<name>","t":<unix ms>,"msg":"…"}`.
   `req` is empty before the first `/next` (INIT phase) and between
   invocations. Frames are assigned `seq` in the order the init observed
   them, which is the order the process wrote them per pipe. The connection is
   re-established on loss with a bounded in-memory backlog (§5).
5. **Be PID 1**: reap zombies, forward `SIGTERM`/`SIGKILL` semantics to the
   children, exit with the runtime child's exit code so the existing
   `exitNotifier` path sees exactly what it sees today. On child exit: drain,
   flush the log stream, close it, exit.
6. **Tee to the container's own stdout/stderr.** Owning the pipes means the
   runtime's bytes no longer reach the container's stdout on their own, so
   `docker logs` would go dark. Every line the init reads is therefore also
   written to its own fd 1/2 — which *are* the container's stdout/stderr —
   byte-for-byte, so the daemon's copy looks exactly as it does today. The
   cost is one write per line to the log driver, which is what the runtime
   pays today anyway; the tee is a side channel and does not touch the
   ordering guarantee in §3.3. It is the debugging backup: `docker logs -f`
   keeps working for humans, the startup-exit capture in `awaitContainerIP`
   and the INIT-timeout diagnostic keep their evidence (which matters more,
   not less, with an init in the loop — an init that fails before it can
   spawn the runtime can only explain itself there), and if the log channel
   to the host ever drops and overflows its backlog, the complete record is
   still on the daemon. It is **not** a second transport: the host never
   reconciles CloudWatch or a tail from `docker logs` (that would reintroduce
   the follower this plan deletes); a gap is logged loudly, naming the
   container, and is a human's to consult.
7. **Overcast-only diagnostics, labelled as such.** The init writes what it
   knows about itself to the container's **stderr only**, prefixed
   `[overcast-init]` — `runtime started pid=… cmd=…`, `extension <name>
   started`, `request <id> begin / end (drained N lines)`, `log channel
   reconnected / gap N frames`, `child exited code=…` — and those lines are
   never forwarded to CloudWatch, the tail or the Telemetry API, which stay
   AWS-shaped. This is the kind of extra, clearly-labelled information
   Overcast can surface and AWS cannot; it lives in the daemon copy and
   nowhere else. Annotating the teed lines themselves (a `req=<id>` prefix)
   is opt-in via an init env var and **off** by default, so the daemon copy
   stays byte-identical to today for anyone scripting against it. An
   emulator-only "container logs" view for Lambda in the web UI, the way ECS
   already has `GetTaskContainerLogs`, falls out of the same feed and is a
   natural follow-up (not this plan).

Reasons for stdlib-only and `CGO_ENABLED=0 -ldflags "-s -w" -trimpath`:
~2–3 MB static binary per arch; no dependency on the image's libc
(distroless/`FROM scratch` images are fine); no second logging stack to keep
in sync.

### 3.2 Getting the init into the container

The init is **embedded in the Overcast binary** and added to the existing
provisioning archive (`var/overcast/init`, mode 0755) that is already copied
into every zip-function container before start; image-function containers
start receiving that archive too (it becomes "always at least the init").

- `internal/services/lambda/initbin`: `//go:embed all:dist` with a committed
  `dist/.gitkeep` (the `cmd/compat` UI precedent), so a bare checkout still
  builds, vets and tests. `initbin.For(arch)` returns the bytes or an explicit
  error naming the missing artefact and the command that produces it.
- `make lambda-init` builds `dist/lambda-init-linux-{amd64,arm64}`
  (gitignored). `build`, `build-all`, the Dockerfile and the release workflow
  depend on it; the test workflow runs it before `go test`. A missing init is
  a **loud** error at the first function create/invoke that needs it ("this
  overcast build has no Lambda init for linux/arm64 — run `make lambda-init`"),
  never a silent fallback. Docker-gated tests call `requireLambdaInit(t)`, which
  `go build`s it into `dist/` when absent (one-off, cached by source hash), so
  `go test` just works locally and in `scripts/docker-go.sh`.
- Architecture: the function's `Architectures` already selects the image
  platform (`dockerPlatformForLambdaArchitectures`); the same value selects
  the init.

**Why embed rather than publish an init image** (the LocalStack shape): no new
registry artefact, no pull, works offline and in containerised Overcast alike,
no version skew between host and init, and native-mode Overcast on
Windows/macOS needs no host-visible files (no bind mounts — see
docs/dev/container-networking.md for why those are a trap here). Cost: ~5 MB
on every Overcast binary (web/dist is larger).

**Why copy per cold start rather than a seeded named volume**: it is one code
path that already exists (copy phase measures 7–14 ms with code + bootstrap
on Docker Desktop, docs/plans/lambda-cold-start.md §"cold-start anatomy"); a
~3 MB addition is expected to cost low single-digit ms against a ~300 ms cold
start. Phase 2 measures it; a per-(version, arch) labelled volume seeded once
(`CreateContainer` with the volume mounted over the archive path seeds it from
the image without a start) is the recorded follow-up if the measurement
disagrees.

### 3.3 Ordering without a clock — and without an extra round trip

The guarantee we need: every byte the runtime wrote before it POSTed the
response is attributed to that request, and the host has it before it
finalises the invoke.

- Pipe writes and the response POST come from the same process in program
  order. By the time the proxy receives `POST /response`, the bytes are in
  the kernel pipe buffer (Node writes to pipes synchronously on Linux; Python
  flushes on newline and `sys.stdout.flush()`-s around invocations in the
  RIC; other RICs likewise — the same assumption RAPID makes).
- **Drain** = the init's reader goroutine for each pipe has hit `EAGAIN` at
  least once *after* the response arrived. The reader owns the fd through
  `RawConn.Read`: loop `read()` until `EAGAIN`, publish frames, record
  "drained up to seq N", then wait for readability. A drain request is
  satisfied by the next such mark. This is poll-based, event-driven, and
  independent of how fast the handler ran. A partial line (no newline yet)
  stays buffered and is attributed when it completes — the same visible
  behaviour as AWS.
- The proxy then forwards the response with one added header,
  `X-Overcast-Log-Seq: N` — "every frame with `seq ≤ N` was sent on the log
  stream before this". The host's invoke path, on receiving the RIC response,
  waits until its ingest has seen `seq ≥ N` for that container (a channel
  wait, bounded by a small deadline — §5). Because the frames were written to
  an already-open connection *before* the response was sent on another, the
  wait is normally zero: no added round trip, no body rewriting, no
  HTTP trailers. LocalStack's two sequential calls buy the same ordering for
  a full RTT per invoke.
- END and REPORT are still written by the host, now strictly after the
  request's last frame, so CloudWatch ordering is exact for *every* invoke,
  not only tail-requesting ones. Non-tail invokes pay the same (normally
  zero) wait; if measurement shows otherwise it can be gated on `LogTail`
  exactly as today.

**The telemetry channel does not enter this invariant** (§ 3.5, #1799). It is a
separate connection carrying no frames and no `seq`, and it runs in the
opposite direction: everything on it is a record the *host* has already
marshalled, from a fact this invariant has already ordered. `platform.initStart`
and its two companions are marshalled from frames the host had ingested;
`platform.start`, `runtimeDone` and `report` are written by the invoke path
after its `awaitLogSeq`. A delivery is what happens to those bytes afterwards,
so there is nothing it can reorder — and nothing about it that the host needs to
wait for before writing anything. It is also why the delivery may lag: a
subscriber is entitled to the records, not to them by a deadline, exactly as on
AWS.

Inside the container the relay is a goroutine of its own with its own HTTP
clients. It never reads a pipe, never enters `drain`, and shares no connection
with the proxy — so a batch in flight cannot delay a `/next` or a `/response`,
and the drain still completes one poll cycle after the bytes are in the pipe.
What it does spend is a little of the init's CPU inside the function's CFS
quota, in the same class as the ~0.3 ms per invoke the proxy already costs, and
only for an environment something has actually subscribed on (§ 6).

### 3.4 Host side

- `RuntimeAPIServer`: the `/overcast/v1/logs` ingest on the per-environment
  mux, identified like `/next` is (listening port, then remote IP). An emulator-
  internal channel between two Overcast components, not a user-facing AWS API
  (the "no custom endpoints" rule in AGENTS.md guards the service surface;
  this lives on the container-facing Runtime API endpoint, under a prefix no
  AWS path uses, like LocalStack's executor endpoint).
- `containerInstance.logSink` replaces `tailBuf`/`streamLogs`: per-request
  line buffers keyed by request ID (ring-bounded at the tail's 4 KB plus
  headroom), an INIT-phase buffer, and the CloudWatch batcher (§4). The tail
  for request R is: START, R's frames in seq order, END, REPORT — exact.
- Invoke: `resultCh` → `awaitLogSeq(n)` → emit END/REPORT → snapshot R's
  buffer. `discardUnclaimedOutput`, `tailMark`, `beginTail` and every wait
  constant go (§8).
- Crash/timeout paths: the init drains and closes the log stream on child
  exit; the host's "drain before END/REPORT" becomes "wait for the stream to
  close or the deadline", event-driven — `waitForLogDrain` goes.
- INIT-timeout diagnostics (`logInitTimeout`) quote the INIT-phase buffer the
  host already holds instead of fetching `docker logs`; the daemon copy
  (teed, §3.1 item 6) stays the fallback when the buffer is empty because the
  init itself never got going.
- Extensions: frames with `src: ext:<name>` publish to the Telemetry/Logs API
  as `extension` records directly; `classifyRuntimeLogLine` and the prefix
  convention go.
- Container create: `Entrypoint: [/var/overcast/init]`, `Cmd: <original
  entrypoint…> <original cmd…>`; `AWS_LAMBDA_RUNTIME_API=127.0.0.1:9001`,
  `OVERCAST_RUNTIME_API=<rapiListener addr>`. For image functions without
  `ImageConfig` overrides, read `Config.Entrypoint/Cmd/WorkingDir` from the
  image (`ImageInspect` grows those fields; one call, cached with the image
  resolution that already happens). `awaitContainerIP`, the per-environment
  listener, INIT-burst throttling and proactive init are untouched.

### 3.5 Telemetry the other way (#1799, 2026-09-06)

The Telemetry API and the Logs API are the one Lambda surface whose traffic runs
host-to-sandbox. An extension stands its listener up inside the execution
environment and subscribes a loopback destination
(`http://127.0.0.1:<port>`, `http://localhost:<port>`,
`http://sandbox.localdomain:<port>`), and AWS's platform POSTs the record
batches to it from in there. Overcast made that POST from the host process,
rewriting the loopback to the container's bridge IP
(`lambda.normalizeExtensionLogURI`) — which needs the host and the daemon on one
kernel. Docker Desktop runs the engine in a VM the host has no route into, so
every delivery timed out, the emulator logged "dropping a telemetry delivery"
and the Telemetry API was inert on Windows and macOS. It was the last reason a
container's address had to be *dialable* rather than merely identifying.

So the delivery turns around, the way everything else here already runs:

- **`initproto.TelemetryPath`** (`POST /overcast/v1/telemetry`), on the same
  per-environment listener and attributed the same way. The init holds one poll
  open; the host answers it with a `TelemetryDelivery` — an ID, the destination
  *exactly as subscribed*, and the batch as `json.RawMessage` — when a
  subscriber's batch is cut. The init POSTs those bytes to that address and
  reports the outcome as a `TelemetryResult` on its **next** poll, so one
  exchange both acknowledges and collects and the init needs no second path.
- **`telemetryRelay`** (host, `telemetry_relay.go`) is a rendezvous, not a
  queue: `telemetryBuffer` and the bounded `logsDeliveries` queue already decide
  what is sent and what is shed, and a second buffer would be a second place for
  records to pile up outside the accounting. `postExtensionLog` hands one
  attempt over and waits for the verdict exactly as it waited for an HTTP
  response, so the three attempts, the backoff, the shedding and the
  `platform.logsDropped` report are untouched. Only the transport moved.
- **Capability is the relay's presence**, not a version field. A relay is
  created with the per-environment listener, so every real environment has one
  and an init that never polls costs each batch its attempts and reports a drop
  — the behaviour Desktop already had, and never a POST at a loopback address
  that means something else entirely on this side of the sandbox. (Skew is
  anyway what § 3.1 says it is: the init is embedded in the Overcast binary and
  shipped with the host that speaks to it.) The direct POST survives for a
  Runtime API server with no per-environment listener, which is no container at
  all — the shape the Logs/Telemetry unit tests drive.
- **Buffers are keyed by (environment, URI).** Not rewriting the destination
  costs the uniqueness the rewrite used to supply: two environments' extensions
  routinely subscribe the same loopback address and would otherwise share one
  buffer and one delivery. Being per-environment, buffers are now also dropped
  with the environment instead of accumulating for the life of the process.
- **One batch in flight per environment.** The init's relay is a single loop, so
  an environment's deliveries are strictly ordered — which the four workers
  sharing one queue never were. It costs a slow destination delaying the batch
  behind it, bounded by the init's one-second POST timeout; the host's own
  attempt budget is two seconds, so the init's timeout decides a slow
  destination rather than the two budgets racing.

Left for its own issue at the time and settled by #1837: `sandbox.localdomain`
did not resolve inside an Overcast sandbox, so a subscription using that
spelling was accepted and never delivered. The Lambda container is now created
with an `/etc/hosts` entry pointing the name at `127.0.0.1`
(`lambda.ContainerRuntime.extraHosts`), which is where the name lives on AWS —
the init still dials the destination exactly as subscribed and parses nothing.

## 4. Everywhere Docker is used — what is shared, what is not

Survey (non-test importers of `internal/docker`): ecs, rds, ec2, lambda,
elasticache, efs, msk, eks, ecr, router, containerendpoint, dataplane,
serviceutil, cmd/overcast, cmd/tsgen. Live log consumers: **Lambda**
(`streamLogs`) and **ECS** (`pumpContainerLogs`, for `awslogs` task
definitions). RDS and ECS also take one-shot `ContainerLogs(…, "200")`
captures on exit/health; ElastiCache, MSK, EFS, EKS, EC2 do not read logs.

The init is Lambda-specific — it exists to proxy the Runtime API, and ECS
tasks have no Runtime API; an ECS task's logs reach CloudWatch through the
`awslogs` log driver on real AWS, i.e. daemon-side, which is what following
`docker logs` models correctly. So ECS keeps daemon read-back — but gets the
hardened follower Lambda built, instead of its own un-hardened copy:

- **`internal/containerlogs`** (new, Phase 0): `Follower` — open/follow with
  `since`, demux, bounded line assembly (`readBoundedLogLine`), reconnect with
  backoff and the exact timestamp+count cursor dedup (`logCursor` /
  `logCursorAdmission`), reconcile-on-close, `clock.Clock`-injected,
  `LineSink` interface; and `CloudWatchBatcher` — the 25-line / 5 ms batching
  writer with bounded retry (`writeEventsWithRetry`). Both move out of
  `container_runtime.go` unchanged in behaviour, with their tests.
- ECS `pumpContainerLogs` becomes a `Follower` + `CloudWatchBatcher` (gains
  reconnect, dedup, batching; loses its `time.Now()` — CONTRIBUTING's clock
  rule — and its unbounded-line risk).
- Lambda uses the shared `CloudWatchBatcher` from its init-fed sink and stops
  using `Follower` entirely in Phase 2. A `containerlogs.TailLines(ctx,
  docker, id, n)` helper serves the one-shot captures in ECS/RDS/Lambda's
  `logInitTimeout` (until Phase 2 removes the latter).

Nothing else changes for the other services; `containerendpoint` and
`dataplane` are unaffected.

## 5. Reliability

| Failure | Today | After |
| --- | --- | --- |
| Daemon slow to open/serve the log follow | tail truncated; reconnect/backoff; the whole #873→#1402 history | no daemon log connection for Lambda at all |
| Handler line arrives after the response | bounded wait, heuristic | exact: drained before the response is forwarded |
| Handler prints nothing | 80 ms silence bound on tail invokes | zero wait (nothing to drain) |
| Init crashes / cannot start | n/a | container exits → `exitNotifier` → the same "container exited" error path; `ContainerLogs` one-shot for the diagnostic (the init writes `[overcast-init]` errors to the container's stderr before exiting, so they are in `docker logs`, and anything the runtime managed to print is there too, teed) |
| Log stream connection drops mid-life | n/a (reconnect logic on the daemon side) | init reconnects with backoff and replays from a bounded backlog (ring of N frames / M bytes); the host dedups by `seq`; if the backlog overflowed, a `gap` frame is sent and the host logs it naming the container — the complete record is still in `docker logs` via the tee (§3.1 item 6); `awaitLogSeq` is bounded by a short deadline (100 ms — now a fact about a *broken* channel, not a guess about a slow one) and the tail is marked truncated rather than blocking the invoke |
| Telemetry channel drops mid-life (#1799, § 3.5) | n/a — the host dialled the container and the delivery simply failed | the init reconnects with backoff; the delivery in flight is not acknowledged, so the host's attempt times out and retries it, and an endpoint that stays unreachable is dropped with `platform.logsDropped` exactly as before. At-least-once, which is what the Telemetry API path already was |
| Telemetry destination inside the sandbox is not listening | delivery from the host timed out (or, on Desktop, always did) | the init's POST is refused on loopback immediately; the host sees a failed attempt and retries, so a dead endpoint costs its three attempts rather than three timeouts |
| Host restart | containers are torn down with `logCtx` | unchanged |
| Runtime writes > pipe capacity | daemon buffers | reader goroutines always drain; the runtime never blocks on a full pipe longer than today |
| Image whose ENTRYPOINT is not a Runtime API client (e.g. RIE hard-coded as entrypoint) | broken today too (RIE would serve its own 9001) | same — unsupported, as on AWS; documented |
| Non-Lambda base image (distroless, custom) | works | works: static init, no libc, copied via the API before start |
| `ReadonlyRootfs` / non-root `USER` | n/a | copy happens before start through the daemon; init runs as the container user; `/var/overcast` created by the archive with 0755 |
| Missing init artefact in a dev build | n/a | loud error at first use; Docker-gated tests build it |

Identity and security: the init listens on `127.0.0.1` inside the container's
netns only; the ingest endpoint is on the existing per-environment listener
and is identified exactly like `/next` (a container cannot write into
another's log sink).

## 6. Performance

Baselines to measure against (docs/plans/lambda-cold-start.md, same machine
and protocol): hello-world cold p50 ~300 ms, warm p50 ~6 ms; copy phase
7–14 ms; `invoke_metrics_bench_test.go` and the paced bench harness exist.

Expected deltas (to be measured, gates in Phase 2):

- **Cold start**: + ~3 MB in the provisioning tar (copy), + one `exec` and
  the proxy listener, + INIT-phase frames (streamed, off the critical path).
  Budget: ≤ +10 ms p50; the volume alternative (§3.2) if exceeded.
- **Tee**: one write per line to the container's stdout (the log driver)
  — the same write the runtime makes today, moved into the init; not a new
  cost.
- **Warm, non-tail**: + two local proxy hops per invoke (`/next`,
  `/response`; ~50–150 µs each on a loopback inside the netns) + a normally-zero
  `awaitLogSeq`. Budget: ≤ +0.5 ms p50 on the ~6 ms baseline; if the seq wait
  shows up, gate it on `LogTail` as today.
- **Warm, tail**: *faster* — today `idleThreshold` 5 ms minimum, up to 80 ms
  for a silent handler; after, ~0.
- **Host**: no per-container daemon connection held open (64-conn cap no
  longer contended by Lambda), no 1 ms ticker loops, no per-line `docker
  logs` demux. Batched CWL writes unchanged.
- **Telemetry channel** (§ 3.5, measured 2026-09-06): the init binary grows
  16 KB, 7 336 096 → 7 352 480 bytes (linux/amd64, `CGO_ENABLED=0 -trimpath
  -ldflags "-s -w"`), against a ~7 MB binary delivered once per content hash
  through the seeded volume. At runtime it is one parked HTTP request per
  execution environment for its lifetime, alongside the log stream already
  held — an accepted connection on the host, not a daemon one, and idle unless
  something has subscribed. Nothing is added to the invoke hot path: a record
  is published into a buffer as before, and the delivery happens on the
  delivery workers.
- **Throughput under contention**: log delivery no longer competes with
  create/start/stats calls on the daemon — the exact condition that produced
  every one of the four flakes.

Rule from the cold-start plan still applies: never add work to the invoke hot
path that a measurement did not justify; benchmark the shape that exposes
the cost (a burst of sequential warm invokes with and without `LogTail`).

## 7. Phased delivery (each phase one PR or a short stack; failing-test-first)

**Phase 0 — shared follower (no behaviour change).**
Extract `Follower`, `logCursor`, bounded line reader, `CloudWatchBatcher`,
`TailLines` into `internal/containerlogs` from `container_runtime.go`, with
their tests moved. Lambda and ECS both consume it; ECS's pump is deleted.
DoD: ECS awslogs integration test passes with a forced mid-stream
disconnect (new, Docker-gated); Lambda's existing tail/drain tests untouched
and green; `container_runtime.go` shrinks by the moved code. Changelog: `~ [ecs]`
awslogs gains reconnect/dedup/batching.

**Phase 1 — the init, with no host wiring.**
`internal/services/lambda/initproto` (frame type, header and env names,
paths — the single definition both sides import), `cmd/lambda-init`,
`initbin` embed + `make lambda-init` + CI/Dockerfile/release plumbing,
`requireLambdaInit(t)`. Linux-only unit tests (`//go:build linux`, run via
`scripts/docker-go.sh`): proxy pass-through incl. extensions API; request-ID
observation; drain-then-forward ordering under a handler that writes 64 KB+
without newline then responds; PID 1 reaping and exit-code propagation;
reconnect + backlog replay + gap frame; SIGTERM forwarding. DoD: binary
builds for both arches; all tests green under `-race` in the container; no
host code references it yet (changelog `/no-changelog` is wrong here — it
ships in the binary — so `+ [lambda]` describing nothing user-visible is
also wrong; fold the entry into Phase 2's).

**Phase 2 — cut over.**
Ingest endpoint, `logSink`, `awaitLogSeq`, container create via the init
(zip and image functions, `ImageInspect` config), INIT diagnostics from the
buffer, extension frames to the Telemetry API. Integration (Docker-gated):
`TestInvoke_logTail` as-is; a new `TestInvoke_logTail_exactAttribution`
(two back-to-back invokes with interleaved multi-line output, assert each
tail holds exactly its own lines); image-function invoke with a custom
`ImageConfig` entrypoint; extension stdout reaches the Telemetry API as
`extension` records; crash-path logs land before END/REPORT. Benchmarks
before/after on the §6 shapes, numbers in the PR. DoD: all Lambda
integration tests and the compat suites' `lambda-invoke*` groups green;
budgets in §6 met. Changelog: `~ [lambda]` (the user-visible change: exact
tails, exact CloudWatch ordering) and `*` for the tail fidelity class of bug.

**Phase 3 — deletions (§8) and docs.**
Could be the same PR as Phase 2; kept separate so the Phase 2 diff reviews as
an addition and this one as a pure removal. DoD: everything in §8 gone,
`go vet`/lint clean on all three tag sets, every row of §9 done, this plan
marked complete (then
converted to issues and deleted per repo convention if it is ever paused).

**Rollout decision — no kill switch.** A flag keeping the daemon read-back
path alive for one release would mean both transports coexist, which is the
opposite of the point and doubles the test surface. The project is alpha, the
integration tests and every compat suite's `lambda-invoke` groups exercise the
proxy on every PR, and the proxy is transparent to any Runtime API client. The
rollback is a revert of Phase 2 (Phase 3 lands after Phase 2 has soaked at
least one release cut, so a revert of 2 needs no revert of 3). If a reviewer
wants a one-release `OVERCAST_LAMBDA_LOG_TRANSPORT=docker` escape hatch
anyway, Phase 3 is where it is deleted.

## 8. Deletion ledger — what this removes (the cleanup)

`internal/services/lambda/container_runtime.go` (3 292 lines today):

- `streamLogs`, `streamOnce`, `logReadTracker`, `readBoundedLogLine`,
  `logCursor` / `logCursorAdmission` — moved to `containerlogs` (Phase 0),
  then Lambda's use of `Follower` removed (Phase 2).
- `reconcileLogs` (Lambda's close-time backfill; the init closes the stream
  after its final drain).
- `waitForScannerIdle`, `dockerSilentSince`, `logPipelineState`, `tailMark`,
  `beginTail`, `discardUnclaimedOutput`, `waitForLogDrain`,
  `logDrainFirstReadGrace`, the `logReaderNotReading` / `logReaderBetweenReads`
  constants, and every wait constant (`idleThreshold`, `progressMax`,
  `deadlineMax`, `firstReadMax`, `parkedReadMax`).
- Fields: `tailBuf`, `tailAppendAt`, `logReadAt`, `logInFlight`,
  `logParkedAt`, `logStreamEverAnswered`, `logCursor`, `tailUnclaimed`,
  `tailUnclaimedMark`.
- `logInitTimeout`'s `ContainerLogs` fetch (§3.4).
- `lambdaBootstrapScript`, `buildLambdaBootstrapTar`, `lambdaBootstrapTar`
  and the `[overcast-extension:` convention with `classifyRuntimeLogLine`
  (logging_json.go); `ingestContainerLine` reshaped around frames.
- The `maxDockerConns` rationale in `internal/docker/client.go` (the comment,
  and possibly the number).
- `normalizeExtensionLogURI` in `runtime_api.go`, and
  `TestNormalizeExtensionLogURI_rewritesLoopbackToContainerIP` with it (#1799,
  § 3.5): nothing rewrites an extension's destination any more, because nothing
  posts to it from this side of the sandbox. That was the last use of a
  container's address as a *destination*; it remains an environment's identity,
  as it is for `/next`.

Tests: `log_tail_wait_test.go` (779 lines) and `log_drain_wait_test.go` (114)
in full; the `streamLogs`/reconnect cases in `container_runtime_test.go`
(moved with the follower in Phase 0, then kept as `containerlogs` tests);
the extension-prefix cases in `logging_json_test.go`; the bootstrap entries in
`provisioning_tar_test.go`. ECS: `pumpContainerLogs`, `splitDockerTimestamp`
(Phase 0).

Net: roughly 1.5–2 k lines removed against ~1.2–1.5 k added (init ~700,
protocol ~100, host ingest/sink ~300, shared package is moved code). More
importantly the *kind* of code changes: from heuristics about timing to a
protocol with an invariant.

## 9. Documentation impact (part of Phase 3's definition of done)

Grepped for the mechanisms this plan changes (`lambda-entrypoint`, bootstrap,
Runtime API addressing, `AWS_LAMBDA_RUNTIME_API`, `X-Amz-Log-Result`,
extensions, Telemetry API, `awslogs`, `docker logs`, build steps). Each entry is
a specific passage, not a directory:

| Document | What changes |
| --- | --- |
| [docs/services/lambda.md](../services/lambda.md) § Limitations | Delete "Lambda extensions support currently covers Docker-backed zip functions. Image function extension startup is not yet wrapped." — the init launches extensions for image functions too. Reword the `X-Amz-Log-Result` paragraph (§ log-level filtering) if it describes the tail as best-effort; it now holds exactly the invocation's lines. The operation tables are generated from `capabilities_dev.go` and do not change. |
| [docs/dev/architecture.md](../dev/architecture.md) § "A Lambda invocation, end to end" (steps 2–4) and the component diagram | Step 2: the tar now carries the init, not a bootstrap shim; step 3: the runtime client polls the **init's** Runtime API at `127.0.0.1:9001`, which proxies to Overcast; add the log stream. Diagram label ":9001 Lambda Runtime API" stays (it is the host port the init dials). |
| [docs/dev/container-networking.md](../dev/container-networking.md) § execution-environment identity (the `AWS_LAMBDA_RUNTIME_API` paragraphs) | The container is told the per-environment port through `OVERCAST_RUNTIME_API` (read by the init); `AWS_LAMBDA_RUNTIME_API` becomes the AWS value `127.0.0.1:9001`. The "no header or token can be injected — the RIC builds its own requests" sentence is no longer the whole story: the init *can* add headers, which is how `X-Overcast-Log-Seq` travels; identity still comes from the listener, unchanged. |
| [docs/dev/networking.md](../dev/networking.md) § port table and § "The Lambda Runtime API narrows deliberately" | Port 9001 / `LAMBDA_RUNTIME_API_PORT` semantics unchanged; note that the value inside the container is now the loopback proxy. |
| [docs/services/ecs.md](../services/ecs.md) § `awslogs` | One sentence: output is followed with reconnect and de-duplication, so a daemon hiccup no longer drops the rest of a task's logs (Phase 0). |
| [AGENTS.md](../../AGENTS.md) § Generated files → "`web/dist` — the one thing you may still have to build" | Becomes two things: add the init artefact (`make lambda-init`, `initbin/dist/.gitkeep`, loud error at first use, Docker-gated tests build it themselves, `-tags slim` does **not** drop it because slim images run Lambda). Same section's "A bare `git clone` builds" bullet stays true and should say so explicitly for the init. |
| [CONTRIBUTING.md](../../CONTRIBUTING.md) § build instructions (`make build`, `make build-cross`) and [docs/dev/development-setup.md](../dev/development-setup.md) § build table, [docs/dev/performance.md](../dev/performance.md) § build step | Mention `make lambda-init` where the SPA build is mentioned; `make build` depends on it. |
| [docs/plans/lambda-cold-start.md](./lambda-cold-start.md) § cold-start anatomy table and § invoke-path | Add the init's measured cost to the copy phase and the proxy hop to the warm path, with the Phase 2 numbers; cross-reference this plan. |
| `Makefile`, `Dockerfile`, `.github/workflows/test.yml` (build step before `go test`; the "Lambda invoke (native host binary)" job's `X-Amz-Log-Result` check stays and becomes a stronger assertion), the release workflow | Build plumbing, not prose — listed so it is not forgotten. |
| [docs/dev/manual-testing.md](../dev/manual-testing.md) | `docker logs` still shows function output byte-for-byte (the tee, §3.1 item 6); add a line that `[overcast-init]` stderr lines are Overcast's own diagnostics and never reach CloudWatch or the tail. |
| [docs/services/lambda/logging.md](../services/lambda/logging.md) § "Where Overcast differs" | The extension-telemetry-destinations row says batches are POSTed from inside the execution environment, as AWS's platform posts them, so the loopback address an extension subscribes is the one it listens on — on every host, Docker Desktop included (#1799, § 3.5). |
| [tests/AGENTS.md](../../tests/AGENTS.md) § Lambda platform table | The "Windows / macOS, Docker Desktop" row loses its one exception; `skipIfHostCannotReachContainerIPs` is deleted (#1799). |
| `.changelog/` | Phase 0: `~ [ecs]`; Phase 2: `~ [lambda]` exact tails and ordering, `+ [lambda]` extensions for image functions, `* [lambda]` for the tail-fidelity bug class; Phase 3: nothing user-visible; #1799: `* [lambda]` telemetry reaching subscribers on Docker Desktop. |

Not affected, checked: docs/performance.md (its `docker logs --timestamps`
is about Overcast's own container), docs/cdk/*, docs/local-dev.md (the ECS
`awslogs` CDK example still applies), docs/https.md, docs/services/rds.md /
msk.md / ecr.md (one-shot captures or unrelated), STATUS.md (no capability
flips — unless the extension limitation is tracked there; check on the day).

## 10. Testing strategy

- **Protocol**: table tests on frame encode/decode and header parsing in
  `initproto`, shared by both sides — plus, for the telemetry channel (§ 3.5),
  that a delivery's body round-trips byte for byte and a poll with no result
  omits it.
- **Init**: Linux-only unit tests (Phase 1 list), run under `-race` via
  `scripts/docker-go.sh`; the init's proxy and drain are testable in-process
  with a fake Runtime API and pipes — no Docker needed for those.
- **Host**: `logSink` and `awaitLogSeq` with a mock clock and a scripted frame
  source (the successor of today's mock-clock wait tests — but asserting
  ordering facts, not durations).
- **Telemetry channel** (§ 3.5): on the init side, that the batch reaches the
  destination byte-identical, that batches arrive in the order they were handed
  out, and that an unreachable destination is reported rather than swallowed;
  on the host side, that a subscriber's batch is handed to the init with the
  destination unrewritten, that **no connection is opened to the container's
  address** even with a server listening there, that a reported failure is
  retried the same three times and then becomes `platform.logsDropped`, and
  that two environments on one loopback destination get two buffers.
- **Integration**: the Docker-gated Lambda suite already runs locally from
  the Go container with the socket mounted (verified on #1402; needs
  `MSYS_NO_PATHCONV=1` on Git Bash) and on CI's three tag sets; the compat
  suites' `lambda-invoke` groups cover python3.12 and nodejs20.x across seven
  SDKs and go through the proxy.
- **Benchmarks**: the §6 shapes, paced per the cold-start plan's protocol,
  before/after in the Phase 2 PR.

## 11. Decisions to confirm before Phase 1

1. **Embed vs publish** the init (§3.2) — plan says embed.
2. **Copy vs volume** (§3.2) — plan says copy, measure, volume as follow-up.
3. **Non-tail invokes wait for `seq`** (§3.3) — plan says yes (exact CloudWatch
   ordering for free), gate on `LogTail` only if measured.
4. **No kill switch** (§7) — plan says none.
5. Whether Phase 0 (shared follower for ECS) ships regardless of the rest —
   recommended yes; it stands on its own.

## 12. Out of scope

- Faithful `platform.*` Telemetry API events (initStart/initRuntimeDone/
  runtimeDone with metrics): the init makes them straightforward — it knows
  every phase boundary — and they were the natural Phase 4, but not this plan.
  (Since shipped: #1410 → #1419 delivered `platform.initStart` /
  `initRuntimeDone` / `initReport` with init-measured timings; `runtimeDone`
  stays host-owned, deliberately — the host is the only side that can emit it
  on the crash and timeout paths.)
- Running ECS tasks under an init (no Runtime API; the awslogs-driver model
  is the correct one there).
- Changing how the Runtime API is addressed from the container
  (`containerendpoint`, the per-environment listener, hostnames) — unchanged.
- Carrying the Telemetry API's *deliveries* over the init. (Since shipped:
  #1799 — § 3.5. It was not in scope here because this plan was about owning
  the runtime's stdout, and the delivery direction only became the last
  outstanding one once everything else ran through the init.)
