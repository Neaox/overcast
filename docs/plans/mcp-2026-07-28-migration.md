# Migrating MCP to revision 2026-07-28

Status: proposal. Nothing here is implemented. The package split it depends on
is separate work; see "Groundwork already done" below.

Overcast's MCP servers speak revision `2025-11-25`
(`internal/mcp/server.go`, `ProtocolVersion`). The current published revision is
`2026-07-28`. This document says what that move costs, what it removes, what it
adds, and in what order it is worth doing.

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
  handshake: `2025-11-25` and earlier. This is what Overcast speaks.
- **Modern** — versions that carry version, identity and capabilities as
  per-request metadata: `2026-07-28` and later.
- **Dual-era** — an implementation that serves both.

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

### 5 — MRTR

Only worth doing when a tool actually needs elicitation. Handlers should return
an intent and let the layer above shape the `InputRequiredResult`, so
`requestState` signing and replay protection live in one place — the spec
requires servers to treat `requestState` as attacker-controlled and to protect
its integrity.

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

A related hazard: `ServeStdio` has no dispatcher of its own — it synthesises an
`http.Request` and pushes it through `RootHandler()`. Every removal in phases 1
and 4 changes the stdio path too, and its synchronous-dispatch carve-out for
`initialize` loses its reason to exist, which changes the loop's concurrency
model.

## Docs to update

`docs/plans/mcp.md` (states "Both servers target MCP `2025-11-25`"; its
transport, capability and "known gaps" sections all go stale — it lists
configurable notification replay as a future improvement, which this cancels),
`internal/router/mcp_routes.go`'s header comment (documents the GET stream, the
DELETE endpoint and the session strategy), and `docs/dev/architecture.md`.

## Open questions the spec does not settle

1. Graceful subscription teardown: cancellation says the server **MUST** send
   `notifications/cancelled`; subscriptions says it **SHOULD** send an empty
   result. Whether both, and in what order, is unresolved.
2. Keep-alive cadence is entirely unspecified, and there is no keep-alive story
   for stdio at all.
3. A missing `MCP-Protocol-Version` header yields `-32020`; a missing
   `_meta` protocol version yields `-32602`. Which applies when both are absent
   is not stated.
4. `inputResponses` / `requestState` placement is exemplified only for
   `tools/call`; `prompts/get` and `resources/read` are inferred.
5. No defined way for a client to say "the user declined" to an
   `inputRequests` other than never retrying.
