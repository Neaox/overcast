# Migrating MCP to revision 2026-07-28

Status: in progress. Phases 1 through 4 are merged. Phase 5 is not started and
may never be — see below.

| Phase | State | Landed in |
|---|---|---|
| Package split (groundwork) | done | #1015 |
| 1 — per-request `_meta` | done | #1017 |
| 2a — `server/discover` | done | #1018 |
| 2b — `resultType`, cacheable results | done | #1019 |
| 2c — `Mcp-Method` / `Mcp-Name` headers | done | #1020 |
| 3a — `subscriptions/listen` | done | #1021 |
| 3b-i — observation harness, broadcast tests ported | done | #1022 |
| 3b-ii — per-request response stream, request-scoped tests ported | done | #1023, #1024 |
| 4 — sessions, replay, GET stream, handshake | done | this branch |
| 5 — MRTR | not started, and not yet warranted | — |

Phases 1 to 3 were additive: both eras worked, and a request carrying `_meta`
with a protocol version was served statelessly while one without it took the
legacy path. Phase 4 deleted the legacy half, so Overcast's MCP servers now
speak revision `2026-07-28` and nothing else (`internal/mcp/server.go`,
`ProtocolVersion` — there is one such constant now, not two).

This document says what that move cost, what it removed, what it added, and in
what order it was worth doing.

Read `docs/plans/mcp.md` first — it describes the two servers this changes.

## Why this is not a version bump

`2026-07-28` is the "stateless MCP" revision. It does not extend the protocol;
it removes most of its connection model and replaces the rest.

> "There is no negotiation handshake. Every request carries its protocol
> version, and the server accepts or rejects each request independently"
> — 2026-07-28, Versioning

The spec's own terms for the two eras are worth adopting, because the
compatibility rules are written in them:

- **Legacy** — versions that establish a session with an `initialize`
  handshake: `2025-11-25` and earlier. This is what Overcast spoke.
- **Modern** — versions that carry version, identity and capabilities as
  per-request metadata: `2026-07-28` and later. This is what Overcast speaks.
- **Dual-era** — an implementation that serves both. Overcast is not one.

The terms were load-bearing while both eras were live and are now historical.
The code has dropped them: there is one `ProtocolVersion`, and the field that
records whether a request declared one is `requestMeta.versioned` rather than
`.modern`, because "modern" no longer distinguishes it from anything.

### Removed

| Removed | SEP | Consequence here |
|---|---|---|
| `initialize` / `notifications/initialized` handshake | 2575 | The lifecycle gate and `ready`/`initDone` go |
| Protocol-level sessions, `Mcp-Session-Id`, DELETE termination | 2567 | Session map and its validation go |
| The HTTP GET stream | 2575 | `handleSSE` and the whole GET-stream path go |
| SSE resumability — `Last-Event-ID`, event IDs | 2575 | Replay buffer, `MCPReplayLimit`, `OVERCAST_MCP_REPLAY_LIMIT` go |
| `resources/subscribe` / `unsubscribe` | 2575 | Replaced by `subscriptions/listen` |
| `ping`, `logging/setLevel`, `notifications/roots/list_changed` | 2575 | Log level moves to per-request `_meta` |
| Server-initiated JSON-RPC requests | 2322 | Replaced by MRTR |
| Error codes `-32002`, `-32042` | — | Resource-not-found becomes `-32602` |

### Added, and mandatory

- **`server/discover`** — servers **MUST** implement it.
- **`resultType`** on every result. Absent means `"complete"`.
- **`Mcp-Method` and `Mcp-Name` headers** — "REQUIRED for compliance", with
  header/body validation and a new `-32020 HeaderMismatch`.
- **`ttlMs` / `cacheScope`** required on `tools/list`, `prompts/list`,
  `resources/list`, `resources/read`, `resources/templates/list`.
- **`x-mcp-header`** parameter mirroring into `Mcp-Param-*` headers.
- New errors `-32021 MissingRequiredClientCapability`, `-32022
  UnsupportedProtocolVersion`.

### The awkward part

MRTR exists to carry sampling, roots and elicitation now that servers may not
initiate requests — and the same revision **deprecates sampling and roots**
(twelve-month window). Elicitation is not deprecated. Overcast implements none
of the three today, so this costs nothing now, but it means MRTR's durable use
case is narrower than it first appears.

## Do we need dual-era?

**No.** This is the decision that halves the work, and it rests on two facts.

First, the emulator is in alpha and nothing is consuming its MCP surface yet, so
there are no legacy clients to preserve. That matters because the spec is blunt
about the alternative:

> Legacy client → Modern server: **"Fails."** … "Legacy clients have no
> fall-forward mechanism."

Second, the client that actually matters here already speaks it. Claude Code's
changelog (v2.1.233, 2026-08-14) fixes "MCP v2 connections endlessly reopening
the `subscriptions/listen` stream" — a method that exists only in `2026-07-28`.

So the target is **modern-only**, and the removals above are deletions rather
than paths gated behind era detection.

Two frictions to accept knowingly:

- **Up-to-date TypeScript clients still open with `initialize` by default.** The
  v2 TS SDK supports `2026-07-28` but `Client.connect()` performs the 2025
  handshake unless the author sets `ClientOptions.versionNegotiation`. Such a
  client will fail against a modern-only server.
- **The Java SDK is `2025-11-25` only**, with the stateless work not started.

Both are reasons to add a legacy path *later, on evidence*, not to build one
now. GitHub's and Cloudflare's MCP servers kept legacy support — but they had
users.

## Should we adopt the Go SDK instead?

Not as part of this. The SDK (`v1.7.0+`) is genuinely ahead: it shipped full
`2026-07-28` support on release day, implements every hard part, and its HTTP
handler is path-agnostic so it would mount at `/_overcast/mcp` without the
current `StripPrefix` gymnastics. If Overcast were starting today, it would be
the obvious choice.

Three things decide against it *now*:

1. **`Tool.Execution` has nowhere to go.** Overcast puts real behaviour behind
   it — `toolMutationAffectsRuntimeResources` and `defaultToolIcons` read it —
   and the SDK's `Tool` has no such field. Migrating means a visible wire change
   (moving it to `_meta`) or carrying a patch.
2. **`ResourceProvider` is pull-shaped; the SDK is push-shaped.**
   `RuntimeProvider.ListResources` enumerates live emulator state on every call.
   The SDK wants resources registered up front. Every adapter changes either the
   wire output or the emulator's mutation paths. This is the largest and least
   bounded piece.
3. **Two changes at once make every failure ambiguous** — is the wire wrong
   because the SDK differs, or because the revision does? The only thing that
   could tell us is the existing protocol test suite, which is written against
   the layer being replaced.

Revisit if the SDK gains a `ToolExecution` type or a dynamic resource-listing
hook; either would make it clearly worth it. Also revisit if Overcast ever needs
a real MCP *client* — `runtime_mcp_call` hand-rolls one today.

## Groundwork already done

`internal/mcp` has been split into the protocol (`internal/mcp`) and the tools
(`internal/mcp/providers`). Providers depend on the protocol through five
symbols — `Tool`, `ToolResult`, `HandlerFunc`, `TextContent`,
`ProtocolVersion` — and the protocol depends on providers not at all. This
migration rewrites the protocol package and should leave the 14.3k lines of
providers essentially untouched.

## Phases

Each phase should land green on its own.

Phase 3 is split into 3a (build the listen stream) and 3b (move the observed
behaviour onto it). Phase 4 deletes the GET stream, once 3b has left nothing
observing it.

### 1 — Per-request context (largest, do first)

Replace the connection-scoped `negotiatedVersion`, `clientCapabilities` and
`logLevel` fields with a value built per request from `_meta` and threaded to
handlers. One validator for the protocol version from both header and `_meta`,
with the two distinct error codes. One `requireCapability` helper returning
`-32021`.

Cheaper than it looks: `clientCapabilities` and `negotiatedVersion` are written
today and **never read** — `handleInitialize` hardcodes `ProtocolVersion` into
its result. The negotiation is already a fiction.

The real dependency is that `ready` gates notification emission
(`registerProvider` suppresses `list_changed` before the handshake completes).
Removing the handshake removes the thing that decides when emission may start,
so this phase must choose a replacement policy.

**Settled in 3b: there is no replacement, and emission is unconditional.** A
stateless protocol has no readiness moment to gate on, and needs none — a client
hears about a change because it opened a `subscriptions/listen` stream and named
the type, which is a stronger statement of readiness than the handshake was. The
gate and `TestServer_RegisterProvider_DoesNotEmitListChangedBeforeLifecycleReady`
are gone; the three `TestNotifications_Registering*` tests, none of which
performs an `initialize`, are its successor.

### 2 — `server/discover`, `resultType`, cacheable results, headers

Mostly additive and independently testable. `resultType` and `ttlMs`/`cacheScope`
want one result envelope rather than per-call-site edits.

`x-mcp-header` is the exception: nothing in the codebase parses `inputSchema`
today (it is an opaque `json.RawMessage` end to end), and `handleToolsCall`
takes no `*http.Request`, so it cannot see headers at all. Threading one in
changes its signature and its call site, and raises a design question — what a
header means on the stdio transport, where there are none.

### 3 — `subscriptions/listen`, and retiring the GET stream

Build the listen stream first, move notification delivery onto it, then delete
`handleSSE`. Two things die quietly with it and must be handled deliberately:

- **`SSESource` loses its only caller.** `compat/mcp.go` passes the
  `Orchestrator` as an `SSESource`, and `handleSSE` is the only place
  `RegisterSSEClient` is invoked. Deleting the GET stream makes that bridge dead
  code **with no compile error**.
- **The SSE keep-alive moves.** It currently lives in `handleSSE`; its home
  becomes whatever writer serves both the per-request response stream and the
  listen stream. The spec calls keep-alive out for long-lived streams
  specifically, and with `ping` removed it is the only liveness mechanism left.

### 4 — Sessions, replay, and the handshake come out

Deletion, once nothing depends on them. Includes `MCPReplayLimit` and
`OVERCAST_MCP_REPLAY_LIMIT`, and `SetNotificationReplayLimit` (an API break for
`internal/router`). `Server.Ready()` goes too.

**Done.** Five things the deletion turned up that the proposal did not predict:

- **Three names were still describing the deleted world in code, not comments.**
  `supportedProtocolVersions` listed `2025-11-25` alongside the modern
  revision, so `server/discover` and every `-32022` were inviting clients into
  an era with no handshake, no session and no GET stream left to serve them.
  The workspace-info tool reported the same value as `mcp_version`. Both were
  fixed by collapsing `ModernProtocolVersion` and `ProtocolVersion` into the
  one constant there is now.
- **Log filtering had silently become a constant**, exactly as the phase-3b
  note below warned. Closed here rather than deferred to phase 5: the threshold
  lives on `requestStream`, an absent one means silence rather than a default
  (the revision forbids emitting to a request that named no level), and the
  check runs before the params are built so an emit with no audience allocates
  nothing.
- **The deletion left dead structure behind, not just dead prose.**
  `InitializeResult`, `DefaultReplayLimit`, the `Last-Event-ID` error values,
  `inFlightRequest.method` and `cancelInFlight`'s refusal to cancel an
  `initialize` all compiled fine and meant nothing. `streamFor` was the first of
  these and was caught the same way; a compile error was never going to find any
  of them.
- **Validation moved ahead of registration** in `handleRPCInternal`. A request
  the server will refuse should not first be registered as one it is serving,
  and doing it in that order also means the stream is fully configured before
  `registerInFlight` publishes it to the goroutine that may cancel the request.
- **Deleting the GET stream left the router's shutdown wiring untested**, and
  the one line it hangs on is the one that twice hung a CI package until the
  binary timed out. `TestRuntimeMCPRoutes_SSEStreamLetsGoOnPreShutdown` was the
  only thing checking that `internal/router` hands the server its shutdown
  channel, and `internal/mcp`'s own test wires one by hand so it cannot see the
  omission. Building the server became a function taking the channel — the shape
  `eventsHandler` and `domainsWatchHandler` already have, where leaving it out
  does not compile — and `Server.ShutdownSignal` is exported so the host can
  assert by identity that its channel arrived. Deliberately an identity check:
  opening a listen stream to watch it end would re-run the whole mechanism to
  observe one assignment, and would need the deadline that made the deleted
  test flake.

### 5 — MRTR

**Only worth doing when a tool actually needs elicitation, and none does.** As of
phase 4 no Overcast tool asks the user anything: every handler either answers
from repo or runtime state or fails. MRTR would be a whole feature — signed
`requestState`, replay protection, an `input_required` result shape — with no
caller, which is the same argument that kept `requireCapability` out of phase 1.
So this stays unbuilt until a tool needs it, and the plan is complete without it.

Two things to know when that day comes:

- **`tools/call` cannot express a non-complete result today.** It answers with a
  typed `ToolResult`, and `stampResult` only stamps map-shaped results, so there
  is nowhere for `resultType: "input_required"` to go. That is legal now — an
  absent `resultType` means `complete` — but it is the first thing MRTR has to
  change.
- **Handlers should return an intent** and let the layer above shape the
  `InputRequiredResult`, so `requestState` signing and replay protection live in
  one place; the spec requires servers to treat `requestState` as
  attacker-controlled and to protect its integrity. A `requireCapability` helper
  returning `-32021` belongs there too — `requestMeta.capabilities` is parsed and
  carried ready for it.

## The real cost is the tests

This dominates the estimate and is the thing most likely to be underestimated.

- **81** call sites of `requireLifecycleReady`, **107** of `operationHeaders()`,
  **25** constructing a GET SSE request.
- **~40 tests deleted** — sessions, replay, lifecycle rejection, GET-stream
  behaviour. Their subject genuinely disappears.
- **~24 tests rewritten.** These are the trap. Every `..._OnSSE` test asserts a
  real behaviour — that cancelling emits `notifications/cancelled`, that a
  runtime mutation emits `resources/list_changed`, that unsubscribe stops
  updates — but observes it *through* the GET stream. They read like GET-stream
  tests and are not. Deleting them silently drops coverage of `emitNotification`
  and all seven `emit*` helpers.

**A new observation harness has to exist before a single one can be ported.**
Plan this as "build a harness, then port 24 tests", not "delete 24 tests".

### What porting them found: there are two destinations, not one

The 33 tests that touch the GET stream do not split two ways (GET-stream
machinery vs. behaviour) but three, and the third is the one that costs work:

1. **12 test the GET stream, sessions or replay themselves.** Their subject
   genuinely disappears; they are deleted in phase 4.
2. **15 observe a *broadcast* notification** — `tools/list_changed`,
   `prompts/list_changed`, `resources/list_changed`, `resources/updated`. These
   port straight onto `subscriptions/listen`, which already carries all four.
   Done in phase 3b, in `internal/mcp/notification_delivery_test.go`.
3. **6 observe a *request-scoped* notification** — `notifications/progress`,
   `notifications/cancelled`, `notifications/message`. **These cannot go on a
   listen stream**, and not by omission: such notifications "flow only on the
   response stream of the request they relate to", which
   `subscriptionFilter.wants` enforces by kind. Their destination is a per-request
   response stream, and **that transport does not exist yet.** The POST path that
   answers `Accept: text/event-stream` today buffers the whole response through
   `captureResponseWriter` — which is not a `http.Flusher` — and writes it as a
   single `data:` event, so nothing can be interleaved ahead of the result.

So phase 3b is two pieces of work, and the second is a real transport rather
than a test change. Until it exists, deleting the GET stream drops `emitProgress`,
`emitCancelled` and `emitLogMessage` coverage entirely — which is the trap above,
one level down.

That transport now exists (`internal/mcp/request_stream.go`). Three notes for
phase 4:

- **The stream's headers are written late, on the first thing sent.** A stream
  commits to 200 the moment its headers go out, and `-32020`/`-32022` must stay
  HTTP errors — they are precisely the failures an intermediary is meant to see.
  Nothing is written when those are decided, so the response is still free to be
  a 400.
- **`emitProgress`, `emitCancelled` and `emitLogMessage` currently emit twice** —
  once to the request's stream, once to the legacy broadcast. The second call is
  scaffolding for the GET stream and should be deleted with it, which is what
  finally removes `emitNotification`. *Done in phase 4.*
- **Per-request log level is still unfinished.** `parseRequestMeta` reads
  `io.modelcontextprotocol/logLevel` into `requestMeta.logLevel` (phase 1) and
  nothing reads it; `shouldEmitLog` still consults the connection-scoped
  `s.logLevel` that `logging/setLevel` sets. Phase 4 removes that method, so it
  must move the threshold onto the request at the same time or log filtering
  silently becomes "always info". *Done in phase 4, and it settled a question
  this note did not ask: an absent level is not a default threshold but no
  threshold at all, because the revision says a server "MUST NOT emit
  notifications/message for requests that did not include this field".*

A related hazard: `ServeStdio` has no dispatcher of its own — it synthesises an
`http.Request` and pushes it through `RootHandler()`. Every removal in phases 1
and 4 changes the stdio path too, and its synchronous-dispatch carve-out for
`initialize` loses its reason to exist, which changes the loop's concurrency
model. *This landed as predicted: notifications still run inline because they
produce no response, and everything else is dispatched concurrently.* One
consequence it did not predict is that stdio tests must build their messages
through a helper that adds `_meta`, since a hand-marshalled request is now
refused — two stdio tests failed on exactly that, one of them reporting it four
steps downstream as "timed out waiting for handler to start".

## Docs to update

*Done.* `docs/plans/mcp.md` — its protocol, transport, logging and known-gaps
sections; the future improvement it listed for configurable notification replay
is deleted rather than rewritten, since this cancels it.
`internal/router/mcp_routes.go`'s header comment, which documented the GET
stream, the DELETE endpoint and the session strategy. `docs/dev/architecture.md`
needed nothing: its one MCP sentence names the endpoint and what it exposes, and
neither changed.

## Open questions the spec does not settle

1. Graceful subscription teardown: cancellation says the server **MUST** send
   `notifications/cancelled`; subscriptions says it **SHOULD** send an empty
   result. Whether both, and in what order, is unresolved.
2. Keep-alive cadence is entirely unspecified, and there is no keep-alive story
   for stdio at all.
3. A missing `MCP-Protocol-Version` header yields `-32020`; a missing
   `_meta` protocol version yields `-32602`. Which applies when both are absent
   is not stated. *Settled in code, in `requestMeta.validate`: the body wins, so
   both-absent is `-32602`. Protocol metadata lives in the body — "All protocol
   metadata travels in the message body … A binding MAY additionally mirror
   selected body fields into envelope metadata" — so a missing mirror is a lesser
   fault than a missing original, and stdio has no mirror at all.*
4. `inputResponses` / `requestState` placement is exemplified only for
   `tools/call`; `prompts/get` and `resources/read` are inferred.
5. No defined way for a client to say "the user declined" to an
   `inputRequests` other than never retrying.
