# Overcast Web UI

The management console for [Overcast](../README.md) — a local AWS emulator. Provides a browser-based interface for inspecting and interacting with all emulated services.

## Tech stack

| Layer            | Library                                                |
| ---------------- | ------------------------------------------------------ |
| Framework        | React 19 + TypeScript                                  |
| Bundler          | Vite                                                   |
| Routing          | TanStack Router (file-based)                           |
| Data fetching    | TanStack Query v5                                      |
| Styles           | Tailwind CSS v4                                        |
| Components       | Radix UI primitives                                    |
| Dev `/api` proxy | Hono, mounted as a Vite plugin (`api/src`) — dev-only  |
| BFF              | Go (`internal/bff`, embedded in the `overcast` binary) |
| Flow diagrams    | React Flow / XYFlow                                    |
| Code editor      | Monaco Editor                                          |

## Getting started

A built `overcast` must be running before starting the dev server — not just the emulator
(`:4566`), but the web UI port (`:4567`) it starts alongside it, since the dev server's `/api/*`
proxy targets that port (see Architecture below).

```bash
# From the repo root — start the emulator + its BFF/UI port
make run

# In a separate terminal — start the web UI
cd web
pnpm install
pnpm run dev
```

The UI is served at `http://localhost:5173`. Its dev server proxies `/api/*` to the Go BFF at
`http://localhost:4567`, which in turn talks to the emulator at `http://localhost:4566`.

## Available scripts

| Command                 | Description                          |
| ----------------------- | ------------------------------------ |
| `pnpm run dev`          | Start Vite dev server with HMR       |
| `pnpm run build`        | Type-check and build for production  |
| `pnpm run lint`         | Run oxlint, then ESLint              |
| `pnpm run lint:oxlint`  | oxlint only (the primary linter)     |
| `pnpm run lint:eslint`  | ESLint only (the remainder)          |
| `pnpm run format`       | Format all files with Prettier       |
| `pnpm run format:check` | Check formatting without writing     |
| `pnpm run preview`      | Preview the production build locally |

## Linting

[oxlint](https://oxc.rs) is the primary linter. **[`.oxlintrc.json`](./.oxlintrc.json) owns
the rule set** — add new rules there, not to `eslint.config.js`. ESLint runs second and only
covers what oxlint has no equivalent for (chiefly `react-hooks/component-hook-factories`);
[`eslint.config.js`](./eslint.config.js) reads `.oxlintrc.json` and switches off every rule
oxlint owns, so the two cannot drift. See
[CONTRIBUTING.md](../CONTRIBUTING.md#linting--oxlint-first-eslint-for-the-remainder).

## Directory layout

```
src/
  routes/              # TanStack Router file-based pages (one per service)
    index.tsx          # Dashboard / overview
    map.tsx            # Topology map
    s3/                # S3 bucket browser
    sqs/               # SQS queue browser
    sns/               # SNS topic browser
    dynamodb/          # DynamoDB table browser
    lambda/            # Lambda function browser
    cloudwatch/        # CloudWatch Logs viewer
    ses.tsx            # SES email viewer
    pipes/             # EventBridge Pipes browser
    events.tsx         # Live event stream
    metrics.tsx        # Emulator metrics
    mail.tsx           # SMTP mock inbox
  components/
    ui/                # Shared, reusable UI components (Combobox, Table, etc.)
  features/            # Service-scoped data layer (api.ts queries, data.ts options)
  services/
    api.ts             # Typed API client — all fetch calls go through apiFetch()
  hooks/               # Shared React hooks
  lib/                 # Utilities (cn, etc.)
  styles/              # Global CSS / Tailwind tokens
api/
  src/
    app.ts             # Hono app: /api/* is one catch-all proxy to the Go BFF
    service-discovery.ts # Resolves the emulator endpoint and the Go BFF's own URL
    vite-plugin.ts     # Vite plugin that mounts the proxy in the dev server
```

## Architecture

The UI uses a **BFF (Backend-for-Frontend)** pattern, but there is only one BFF implementation:
`internal/bff/bff.go`, embedded in the `overcast` binary. The browser talks only to `/api/*`; in
production that's served directly by the binary, and in dev, `api/src/app.ts` forwards every
`/api/*` request byte-for-byte to the same binary's UI port (`:4567` by default) rather than
re-implementing each route in TypeScript. See `docs/plans/dev-bff-consolidation.md` for why (a
hand-mirrored second implementation used to live here, and silently drifted from the Go one —
`#1104`).

Data fetching follows the TanStack Query pattern:

- `web/src/services/api.ts` — typed API client with one function per operation
- `web/src/features/<service>/data.ts` — key factories and `queryOptions()` / `mutationOptions()` factories
- Route components consume these options via `useSuspenseQuery` / `useMutation`

## Contributing

See [AGENTS.md](./AGENTS.md) for conventions specific to this package (routing, data fetching, ARN inputs, Tailwind usage, TypeScript rules).

For project-wide conventions, see the [root AGENTS.md](../AGENTS.md).
