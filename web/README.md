# Overcast Web UI

The management console for [Overcast](../README.md) — a local AWS emulator. Provides a browser-based interface for inspecting and interacting with all emulated services.

## Tech stack

| Layer         | Library                      |
| ------------- | ---------------------------- |
| Framework     | React 19 + TypeScript        |
| Bundler       | Vite                         |
| Routing       | TanStack Router (file-based) |
| Data fetching | TanStack Query v5            |
| Styles        | Tailwind CSS v4              |
| Components    | Radix UI primitives          |
| BFF server    | Hono (Node.js)               |
| Flow diagrams | React Flow / XYFlow          |
| Code editor   | Monaco Editor                |

## Getting started

The emulator must be running on port 4566 before starting the dev server.

```bash
# From the repo root — start the emulator
make run

# In a separate terminal — start the web UI
cd web
npm install
npm run dev
```

The UI is served at `http://localhost:5173`. The BFF proxy runs on port 5174 and forwards requests to the emulator at `http://localhost:4566`.

## Available scripts

| Command                | Description                          |
| ---------------------- | ------------------------------------ |
| `npm run dev`          | Start Vite dev server with HMR       |
| `npm run build`        | Type-check and build for production  |
| `npm run lint`         | Run ESLint                           |
| `npm run format`       | Format all files with Prettier       |
| `npm run format:check` | Check formatting without writing     |
| `npm run preview`      | Preview the production build locally |

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
    routes/            # Hono BFF proxy routes — one file per service
    app.ts             # Hono app wiring
    vite-plugin.ts     # Vite plugin that starts the BFF alongside the dev server
```

## Architecture

The UI uses a **BFF (Backend-for-Frontend)** pattern. The browser talks only to the Hono server in `api/`; that server proxies to the emulator at `:4566`. This keeps CORS and credential concerns out of the browser entirely.

Data fetching follows the TanStack Query pattern:

- `web/src/services/api.ts` — typed API client with one function per operation
- `web/src/features/<service>/data.ts` — key factories and `queryOptions()` / `mutationOptions()` factories
- Route components consume these options via `useSuspenseQuery` / `useMutation`

## Contributing

See [AGENTS.md](./AGENTS.md) for conventions specific to this package (routing, data fetching, ARN inputs, Tailwind usage, TypeScript rules).

For project-wide conventions, see the [root AGENTS.md](../AGENTS.md).
