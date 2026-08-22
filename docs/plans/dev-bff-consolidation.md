# Dev BFF consolidation — kill the dual-BFF drift class

> **Status:** B1 and B3 shipped 2026-08-22 (#1104). The Hono route mirrors in `web/api/src/routes/`
> are gone — `web/api/src/app.ts` is now a single `/api/*` proxy to the Go BFF (see §2, B1) — and
> the docs-slug pairing (B3) is pinned by a test. **B2 (tsgen) is deferred**, filed as a follow-up
> on #1104: the hand-mirrored types in `web/src/types/common.ts` are unchanged by this pass (B1
> doesn't touch wire shapes, so there's no new drift risk from landing B1 without B2) but still
> aren't generated. Owner: TBD.
> **Scope:** the vite dev server's Hono BFF (`web/api/src`) vs the production Go BFF (`internal/bff`), plus the hand-mirrored TypeScript API types. Shipped artifacts are unaffected — this is dev tooling and build discipline, except where the B1 route audit found a Go-side gap (the Lambda layer metadata endpoint was missing from `internal/bff/bff.go` entirely, a real 404 in every build — fixed alongside the proxy since the proxy would otherwise have turned a dev-only regression into one, and the fix is a two-line addition to the audit's target file anyway).

## 1. Why this plan exists

The web UI talks to `/api/*`. In production those routes are served by the Go BFF embedded in the binary. In development they are served by a **separate, hand-written Hono implementation** inside the vite plugin (`web/api/src/routes/*` — debug, docs, ecs, events, health, lambda, lambda-instances, mail, metrics, rds, s3, settings, sqs, topology, registered in `app.ts`). Two implementations of one contract, kept aligned by memory.

That failure mode stopped being theoretical on 2026-07-25, twice in one day:

- **PR #289** — the Metrics & Health feature added `/api/debug/metrics` to the Go BFF only; in dev the page permanently rendered its "debug disabled" state. Found by live UI testing, not by any check.
- **PR #290** — the Go BFF stripped doc frontmatter and excluded `docs/dev/`; the Hono BFF served raw bytes, so dev rendered YAML frontmatter as page content and would happily serve contributor-only docs. Found by a human reading a docs page.

Both fixes were "mirror the change into the second implementation" — i.e., more of the disease as the cure. The same session also hand-mirrored a batch of Go response structs into `web/src/types/common.ts` (`DebugMetrics`, `Advisory`, `DataDirProbeResult`, …), creating the same class of drift one layer down: a Go field rename will not fail `tsc`, it will silently ship a UI reading `undefined`.

**Principle (borrowed from the wire-protocol plan's own logic):** where the same behavior exists twice across a language boundary, discipline does not keep them aligned — either one side becomes authoritative and the other becomes a pass-through/generated artifact, or a test pins the pair. "Update both" has a 100% observed failure rate.

## 2. Items

### B1 — Collapse the Hono BFF into a thin proxy — shipped 2026-08-22 (#1104)

**Change.** Replaced the per-route mirrors in `web/api/src/routes/` with one catch-all in `web/api/src/app.ts`: `/api/*` forwards method, path, query string, headers (including `x-overcast-endpoint`/`x-overcast-region`, however the browser delivered them) and body verbatim to the Go BFF's own UI port (`web/api/src/service-discovery.ts`'s `GO_BFF_ENDPOINT`, default `http://localhost:4567`, overridable via `GO_BFF_ENDPOINT` or `OVERCAST_UI_PORT`). All fourteen route files under `web/api/src/routes/` and `web/api/src/client/aws.ts` are deleted.

**Route audit (as landed).** Every existing Hono route had a Go BFF counterpart except one: `GET /api/lambda/layers/{layerName}/versions/{version}/metadata` (Lambda layer detail page) was never registered in `internal/bff/bff.go` at all — the dev mirror called the emulator directly, so the feature worked under `pnpm dev` and 404'd in every built binary. Fixed by adding the route + a thin proxy handler to `bff.go` (mirroring `handleLambdaSourceGet`'s shape) rather than keeping it dev-only, since it isn't dev-only — it's a real console feature the Go BFF was simply missing. No other dev-only routes were found; the "genuinely dev-only, keep in the plugin" case in the original plan didn't materialize.
Two more drift instances the audit surfaced, both fixed for free by deleting the mirror rather than porting it: the Go BFF's richer debug-trace endpoints (`/api/debug/trace*`) had no Hono mirror at all (dev-only 404s, now fixed), and the Hono lambda routes never forwarded `x-overcast-region` upstream the way `bff.go`'s handlers do (a silent region-scoping bug in dev only, also now fixed).

**Streaming.** Verified live end-to-end, not just unit-tested: built `overcast`, ran it with `OVERCAST_PORT=14566 OVERCAST_UI_PORT=14567`, ran `pnpm dev` pointed at it, and confirmed `/api/events` streams SSE chunks through the proxy continuously (`curl -N`) and that `/api/cloudformation/stacks/<nonexistent>/diagnostics` — the motivating Go-only route — correctly 404s through the proxy instead of never having existed. `web/api/src/app.test.ts` covers the same shapes (streaming pass-through, header stripping, method/body/query forwarding, the `OvercastUnavailable` 502) against a mocked Go BFF for CI, since a real one isn't available there.

**Dev-workflow consequence:** `web/README.md` and `web/AGENTS.md` now say dev requires a running `overcast serve` (both its API port and its UI port, started together by default) — this was already true for AWS calls; the change makes `/api/*` honest about needing the UI port too.

**Accept when:** the mirrored route files are deleted (done); `git grep` for the old route registrations is empty (verified); #289/#290's bug classes are structurally impossible (there is exactly one implementation now); every route audited above works through the proxy against a live `overcast serve` (verified manually, see above) and against a mocked one (verified in CI, `app.test.ts`).

### B2 — Generate TypeScript API types from the Go structs — deferred, follow-up filed on #1104

**Not done in this pass.** B1 doesn't touch any wire shape — the Go BFF was already the source of truth for every response the console renders, mirror or proxy — so landing B1 without B2 introduces no new drift risk; `web/src/types/common.ts` is exactly as hand-mirrored after this PR as before it. Building `cmd/tsgen` (parsing Go struct tags/doc comments, rendering `.ts`, a `--check` CI gate, retiring `common.ts` in favor of the generated file) is its own multi-file effort with its own review surface, and doing it hastily inside the PR that also deleted fourteen files risked half-finishing both. Follow-up filed on #1104 rather than silently dropped.

**Change (unchanged from the original plan).** A small `cmd/tsgen` (house codegen pattern — `capgen` is the template) that renders the BFF-consumed response structs (`state.DebugMetrics`, `router.Advisory`, the `/_health` payload, `DataDirProbe`, flush records) into a checked-in `web/src/types/api.gen.ts`: `json` tags → field names, `omitempty` → optional, doc comments carried over. Hand-written mirrors in `web/src/types/common.ts` are deleted in favor of re-exports.

**CI:** a regen-and-diff step exactly like `capgen --check` — drift between Go structs and committed TS types fails the build with a "run tsgen" message.

**Non-goals:** no runtime schema validation, no OpenAPI, no client generation — types only, for exactly the structs the UI consumes.

**Accept when:** `common.ts` contains no hand-mirrored server types; a deliberate Go field rename makes CI fail at the tsgen-diff step (verified once, then reverted, as the failing-first evidence).

### B3 — Pin the remaining cross-language constants — shipped 2026-08-22 (#1104)

- **Docs heading slugs:** `internal/router/advisories.go` ships a fragment (`#data-dir-placement-…`) computed by `web/src/routes/docs.tsx`'s `slug()` (now exported for this). `web/src/routes/docs.slug.test.ts` asserts `slug()` on the two literal Go headings (`docs/performance.md`'s "Data dir placement — …" and `docs/storage.md`'s "Builds without SQLite") equals the exact fragments `dataDirDocsPath`/`noSQLiteDocsPath` compute in Go. Any future advisory fragment gets added to the same test.
- **Path strings** in the web client (`API_BASE + "/debug/metrics"` etc.): deliberately NOT generated or pinned. Post-B1 they fail loudly in dev (real 404 from the real BFF on first render), which is the cheap, honest guard.

## 3. What deliberately stays out

- **A shared route-manifest contract test** (both BFFs tested against a route list): only the fallback if B1 hits a blocker. It keeps two implementations alive, covers existence but not behavior (the #290 frontmatter bug was behavior), and is maintenance forever.
- **Moving the Go BFF into the vite process** (wasm/sidecar exotica): the emulator is the dev dependency already; running it is the simple truth.
- **Generating the Hono proxy itself:** it's ~40 lines with no per-route knowledge; nothing to generate.

## 4. Phasing

| Phase | Contents | Effort | Gate | Status |
|---|---|---|---|---|
| 1 | B1 route audit + proxy + streaming verification + mirror deletion + dev-setup docs | M | manual page sweep green through proxy; audit table in PR | done (#1104) |
| 2 | B2 tsgen + CI diff gate + mirror-type deletion | S–M | deliberate-drift CI failure demonstrated | deferred — follow-up on #1104 |
| 3 | B3 slug pinning test | S | table test green; referenced from CONTRIBUTING's DRY notes | done (#1104) |

Phases land independently; B2/B3 don't depend on B1 (types drift regardless of who serves the routes).

## 5. Risks

- **Proxy breaks a streaming/long-poll endpoint subtly** — mitigated by making the Events live-tail and Lambda progress endpoints explicit acceptance items before mirror deletion.
- **Dev-loop friction** ("now I must rebuild Go to see BFF changes") — real but honest; the alternative was seeing a *lie* in dev. Document the one-liner rebuild. If it grates, a `make dev` target that rebuilds + restarts the emulator and starts vite is a follow-up nicety, not a blocker.
- **tsgen over-reach** — keep it to the structs the UI actually consumes; resist generating the world (the wire-protocol plan's types-on-demand lesson applies verbatim).
