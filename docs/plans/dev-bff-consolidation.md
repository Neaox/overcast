# Dev BFF consolidation — kill the dual-BFF drift class

> **Status:** planned, still not started as of 2026-08-21 (releases are well past the alpha.24
> gate this was parked behind). The Hono mirrors remain — and have since grown a `settings.ts`
> route, more evidence of the drift class — the hand-mirrored types are still in
> `web/src/types/common.ts`, and there is no `cmd/tsgen` / `api.gen.ts`. Owner: TBD.
> **Scope:** the vite dev server's Hono BFF (`web/api/src`) vs the production Go BFF (`internal/bff`), plus the hand-mirrored TypeScript API types. Shipped artifacts are unaffected — this is dev tooling and build discipline.

## 1. Why this plan exists

The web UI talks to `/api/*`. In production those routes are served by the Go BFF embedded in the binary. In development they are served by a **separate, hand-written Hono implementation** inside the vite plugin (`web/api/src/routes/*` — debug, docs, ecs, events, health, lambda, lambda-instances, mail, metrics, rds, s3, settings, sqs, topology, registered in `app.ts`). Two implementations of one contract, kept aligned by memory.

That failure mode stopped being theoretical on 2026-07-25, twice in one day:

- **PR #289** — the Metrics & Health feature added `/api/debug/metrics` to the Go BFF only; in dev the page permanently rendered its "debug disabled" state. Found by live UI testing, not by any check.
- **PR #290** — the Go BFF stripped doc frontmatter and excluded `docs/dev/`; the Hono BFF served raw bytes, so dev rendered YAML frontmatter as page content and would happily serve contributor-only docs. Found by a human reading a docs page.

Both fixes were "mirror the change into the second implementation" — i.e., more of the disease as the cure. The same session also hand-mirrored a batch of Go response structs into `web/src/types/common.ts` (`DebugMetrics`, `Advisory`, `DataDirProbeResult`, …), creating the same class of drift one layer down: a Go field rename will not fail `tsc`, it will silently ship a UI reading `undefined`.

**Principle (borrowed from the wire-protocol plan's own logic):** where the same behavior exists twice across a language boundary, discipline does not keep them aligned — either one side becomes authoritative and the other becomes a pass-through/generated artifact, or a test pins the pair. "Update both" has a 100% observed failure rate.

## 2. Items

### B1 — Collapse the Hono BFF into a thin proxy

**Change.** Replace the per-route mirrors in `web/api/src/routes/` with one catch-all: `/api/* → <emulator>:4567/api/*` against the Go BFF, forwarding method, body, query, and the service-discovery headers (`x-overcast-endpoint`, `x-overcast-region` — resolve the target emulator exactly as `service-discovery.ts` does today). Delete the mirrored route files.

**Pre-work: route audit.** Catalogue every Hono route against `internal/bff` and classify: *mirror* (proxy replaces it — expected: all of debug/docs/health/metrics/topology/service routes) vs *genuinely dev-only* (no Go counterpart — expected: none; if any exist, they stay and are documented as dev-only in the plugin). The audit table goes in the PR description.

**Streaming caveat (design constraint, not optional):** the Events page consumes a live stream (SSE or WebSocket — confirm which during the audit; the event-history work may also have touched this). The proxy must pass streaming responses through unbuffered — Hono/undici can do this, but it must be verified with the Events page live-tailing through the proxy before the mirrors are deleted. Same check for any long-poll/chunked endpoint (Lambda `invoke-with-progress`).

**Dev-workflow consequence:** dev requires a running emulator built from current Go code. This is a de facto requirement already (the mirrors call through to the emulator for all real data); the change makes it honest. `docs/dev/development-setup.md` gains one paragraph: build/run the emulator (docker-go one-liner), then `npm run dev`.

**Accept when:** every web UI page works in dev through the proxy (manual sweep: dashboard, map, events incl. live tail + history, metrics & health incl. debug-gated panels, debug/raw-state, inbox, docs incl. frontmatter-stripped rendering and `docs/dev` exclusion); the mirrored route files are deleted; a `git grep` for the old route registrations is empty; #289/#290's bug classes are structurally impossible (asserted by the route audit, not by tests that would themselves be mirrors).

### B2 — Generate TypeScript API types from the Go structs

**Change.** A small `cmd/tsgen` (house codegen pattern — `capgen` is the template) that renders the BFF-consumed response structs (`state.DebugMetrics`, `router.Advisory`, the `/_health` payload, `DataDirProbe`, flush records, and whatever the audit in B1 surfaces as wire-visible) into a checked-in `web/src/types/api.gen.ts`: `json` tags → field names, `omitempty` → optional, doc comments carried over. Hand-written mirrors in `web/src/types/common.ts` are deleted in favor of re-exports.

**CI:** a regen-and-diff step exactly like `capgen --check` — drift between Go structs and committed TS types fails the build with a "run tsgen" message.

**Non-goals:** no runtime schema validation, no OpenAPI, no client generation — types only, for exactly the structs the UI consumes.

**Accept when:** `common.ts` contains no hand-mirrored server types; a deliberate Go field rename makes CI fail at the tsgen-diff step (verified once, then reverted, as the failing-first evidence).

### B3 — Pin the remaining cross-language constants

Small, but it closes the loop:

- **Docs heading slugs:** `internal/router/advisories.go` ships a fragment (`#data-dir-placement-…`) computed by `web/src/routes/docs.tsx`'s `slug()`. **Still open, and now half-covered:** `scripts/docs-index.go --check` (run by `make docs-check`) validates every in-page anchor a *published doc* writes against the real heading ids — that gate caught four GitHub-shaped anchors and one anchor to a since-renamed heading. It cannot see the advisory constant, which is a Go string rather than a Markdown link, so the cross-language pairing itself is still pinned by nothing. Add a web test asserting `slug("Data dir placement — avoid host bind mounts on Docker Desktop")` equals the exact Go constant (import the string via a tiny fixture or duplicate-with-test — the test IS the pairing). Any future advisory fragment gets added to the same table test.
- **Path strings** in the web client (`API_BASE + "/debug/metrics"` etc.): deliberately NOT generated or pinned. Post-B1 they fail loudly in dev (real 404 from the real BFF on first render), which is the cheap, honest guard.

## 3. What deliberately stays out

- **A shared route-manifest contract test** (both BFFs tested against a route list): only the fallback if B1 hits a blocker. It keeps two implementations alive, covers existence but not behavior (the #290 frontmatter bug was behavior), and is maintenance forever.
- **Moving the Go BFF into the vite process** (wasm/sidecar exotica): the emulator is the dev dependency already; running it is the simple truth.
- **Generating the Hono proxy itself:** it's ~40 lines with no per-route knowledge; nothing to generate.

## 4. Phasing

| Phase | Contents | Effort | Gate |
|---|---|---|---|
| 1 | B1 route audit + proxy + streaming verification + mirror deletion + dev-setup docs | M | manual page sweep green through proxy; audit table in PR |
| 2 | B2 tsgen + CI diff gate + mirror-type deletion | S–M | deliberate-drift CI failure demonstrated |
| 3 | B3 slug pinning test | S | table test green; referenced from CONTRIBUTING's DRY notes |

Phases land independently; B2/B3 don't depend on B1 (types drift regardless of who serves the routes).

## 5. Risks

- **Proxy breaks a streaming/long-poll endpoint subtly** — mitigated by making the Events live-tail and Lambda progress endpoints explicit acceptance items before mirror deletion.
- **Dev-loop friction** ("now I must rebuild Go to see BFF changes") — real but honest; the alternative was seeing a *lie* in dev. Document the one-liner rebuild. If it grates, a `make dev` target that rebuilds + restarts the emulator and starts vite is a follow-up nicety, not a blocker.
- **tsgen over-reach** — keep it to the structs the UI actually consumes; resist generating the world (the wire-protocol plan's types-on-demand lesson applies verbatim).
