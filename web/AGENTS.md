# web/AGENTS.md

> Web UI conventions for AI agents and contributors.
> For backend conventions, build commands, and project-wide rules, see the [root AGENTS.md](../AGENTS.md).
> For test conventions, see [tests/AGENTS.md](../tests/AGENTS.md).

See [README.md](./README.md) for the tech stack, getting started guide, and directory layout.

---

## Routing conventions

- Routes live in `web/src/routes/` using TanStack Router's file-based convention.
- **Never edit `web/src/routeTree.gen.ts`** — it is auto-generated when the dev server runs (`npm run dev`). If the dev server is already running, changing a route file updates the tree automatically.
- Route params: use `$paramName` in filenames (e.g. `$name.tsx`).
- Access params via `Route.useParams()`, search params via `Route.useSearch()` / `Route.useNavigate()`.

### TanStack Router link typing — never use `as any`

TanStack Router's `<Link>` component is fully type-safe. **Never silence or work around it with `as any` or `// eslint-disable`.**

- Import `FileRoutesByTo` or `FileRouteTypes` from `@/routeTree.gen` to type route strings:
  ```ts
  import type { FileRoutesByTo } from "@/routeTree.gen"
  // Use keyof FileRoutesByTo for a field that holds a route path
  ```
- When mapping over data objects where one field is a route, **type that field** — don't cast at the call site:
  ```ts
  interface MyEntry {
    route: keyof FileRoutesByTo  // ✅ typed once here
  }
  // then at the call site:
  <Link to={entry.route}>…</Link>  // ✅ fully type-safe, no cast needed
  ```
- For dynamic routes with params, use `to` + `params` — never construct path strings:
  ```ts
  <Link to="/$service" params={{ service: entry.id }}>…</Link>  // ✅
  <Link to={"/" + entry.id as any}>…</Link>                     // ❌ never
  ```
- Static route segments always take priority over dynamic ones in TanStack Router's file-based routing. `/s3` will always resolve before `/$service` — dynamic catch-all routes are safe to add.
- After adding or removing route files, regenerate the route tree if the dev server is not running:
  ```ts
  // in web/:
  node --input-type=module <<'EOF'
  import { Generator, getConfig } from '@tanstack/router-generator'
  const cfg = await getConfig({ routesDirectory: 'src/routes', generatedRouteTree: 'src/routeTree.gen.ts', autoCodeSplitting: true }, '.')
  await new Generator({ config: cfg, root: process.cwd() }).run()
  EOF
  ```

---

## Data fetching conventions

All server state goes through **TanStack Query**. Follow the pattern already established in `web/src/features/<service>/data.ts`:

```typescript
// Key factory — first in the file
export const myKeys = {
  all: ["my-service"] as const,
  list: () => [...myKeys.all, "list"] as const,
  detail: (id: string) => [...myKeys.all, "detail", id] as const,
}

// Query options factory
export function myListQueryOptions() {
  return queryOptions({
    queryKey: myKeys.list(),
    queryFn: () => api.myService.list(),
  })
}

// Mutation options factory
export function createMyThingMutationOptions() {
  return mutationOptions({
    mutationKey: [...myKeys.all, "create"] as const,
    mutationFn: (params: CreateParams) => api.myService.create(params),
  })
}
```

- Query invalidation after mutations: use `qc.invalidateQueries({ queryKey: myKeys.xxx() })`.
- All API calls go through `web/src/services/api.ts` — never call `fetch` directly from components.

---

## API client conventions (`web/src/services/api/<service>.ts`)

- **Always use AWS SDK v3 clients for standard AWS service endpoints.** Import the relevant commands from `@aws-sdk/client-<service>` and obtain a client from `awsClients.<service>()` (defined in `web/src/services/aws-clients.ts`). Never hand-roll `fetch` calls or use `emulatorFetch` for operations that the SDK covers.
- `emulatorFetch` and direct `fetch` are only acceptable for emulator-specific custom endpoints (`/_overcast/*`, `/_rds/*`, etc.) that have no SDK equivalent.
- When adding a new service client file, add both browser-side factory methods to `aws-clients.ts` and the corresponding imports to `web/api/src/client/aws.ts` for BFF use.

---

## BFF conventions (`web/api/src/routes/`)

Each route file proxies requests to the emulator using `resolveEndpoint()`:

```typescript
const endpoint = resolveEndpoint(c)
const res = await fetch(`${endpoint.baseUrl}/2015-03-31/...`, {
  method: c.req.method,
  headers: { ... },
  body: ...,
})
return c.newResponse(res.body, res.status, Object.fromEntries(res.headers))
```

- One BFF route file per AWS service.
- Pass query strings through to the emulator where the AWS SDK expects them.
- The BFF is not a transformation layer — it forwards requests and returns raw responses.

---

## ARN inputs — always use `ResourceArnCombobox`

Whenever a form field expects an AWS resource ARN, use `ResourceArnCombobox` instead of a plain text input. It provides a searchable dropdown populated from live emulator data, with free-text fallback for ARNs not yet in the dropdown.

```typescript
import { ResourceArnCombobox } from "@/components/ui/resource-arn-combobox"

<ResourceArnCombobox
  resourceType="sqs"       // "sqs" | "lambda" | "dynamodb-stream" | "esm-source"
  value={arn}
  onChange={setArn}
/>
```

| `resourceType`      | Shows                                                                 |
| ------------------- | --------------------------------------------------------------------- |
| `"sqs"`             | All SQS queues                                                        |
| `"lambda"`          | Lambda functions (placeholder only — no list yet)                     |
| `"dynamodb-stream"` | DynamoDB tables with streams enabled; value = stream ARN              |
| `"esm-source"`      | SQS queues + DynamoDB streams combined (for Lambda ESM trigger forms) |

Adding a new resource type: add a hook, a `RESOURCE_CONFIG` entry, and update `useResourceItems` and the `ArnResourceType` union — all in `web/src/components/ui/resource-arn-combobox.tsx`.

---

## Styling — Tailwind CSS v4

Follow the canonical class rules and v4 syntax in the [root AGENTS.md](../AGENTS.md) (section "Frontend — Tailwind CSS v4").

Custom design tokens (colours, spacing) are defined in `web/src/styles/`. Use `text-fg`, `text-fg-muted`, `bg-bg`, `bg-bg-elevated`, `border-border`, `text-accent` etc. — never hardcode hex values.

---

## Component conventions

- Shared components live in `web/src/components/ui/` — one file per component.
- Service-specific components live in `web/src/features/<service>/components/`.
- Large route files (`web/src/routes/<service>/$name.tsx`) may contain sub-components defined in the same file if they are tightly coupled to that route. Extract to `features/<service>/components/` when they exceed ~200 lines or are needed elsewhere.
- Prefer `cn()` from `@/lib/utils` for conditional class merging.

## React effects

Effects carry a high bar. Before adding `useEffect`, read and apply
[You Might Not Need an Effect](https://react.dev/learn/you-might-not-need-an-effect).

- Do not use effects to derive state from props, query data, or other state. Compute derived values during render instead.
- Do not use effects to synchronize one piece of React state from another. Prefer a single source of truth, render-time derivation, or resetting state at the event boundary that caused the change.
- It is valid to update state during render when guarded by an `if` condition that cannot recurse indefinitely. This is often better than an effect when adjusting state after props/data change.
- Use effects for synchronization with external systems: DOM APIs, browser storage, subscriptions, timers, network connections, imperative third-party widgets, and cleanup of those systems.
- Avoid synchronous `setState` inside effects. If an effect immediately sets state based only on render data, it probably belongs in render logic or an event handler.
- Refs are not render data. Do not read or write `ref.current` during render to drive UI. Use state for values that affect rendered output.
- Prefer event handlers for user-triggered work such as pausing, copying, scrolling, or form submission. Effects should not be a substitute for handling the event where it happens.

---

## TypeScript

- `strict: true` is enforced — no `any` unless unavoidable, document why.
- Before committing or pushing web changes, run `npm run lint` and `npx tsc --noEmit` from `web/`.
- Type exports for API responses live in `web/src/services/api.ts` as `export interface` / `export type`.

---

## Testing

### Stack

| Tool | Role |
| ---- | ---- |
| Vitest | Test runner (jsdom environment, globals enabled) |
| @testing-library/react | Component rendering and querying |
| @testing-library/user-event | Realistic user interaction simulation |
| MSW (msw/node) | Network-level request interception for BFF `/api/*` calls |
| TanStack Router (memory history) | Real router context for components that use router hooks |

### Test file locations

- Component tests: `web/src/features/<service>/components/<name>.test.tsx`
- Route tests (metadata / redirects): `web/src/routes/<service>/routes.test.tsx`
- Shared test helpers: `web/src/test/`

### Always import `render` from `@/test/render`

Never import `render` from `@testing-library/react` directly. The wrapper in `web/src/test/render.tsx` provides a fresh `QueryClient` and a pre-wired `userEvent` instance:

```ts
// ✅
import { render, renderWithData, renderWithRouter, screen } from "@/test/render"

// ❌ — missing QueryClientProvider; components with useQuery will throw
import { render, screen } from "@testing-library/react"
```

### Never mock TanStack Query

Do not mock `@tanstack/react-query` or individual hooks like `useQuery`. Use one of the two alternatives:

**Option A — Cache seeding** (`renderWithData`): seeds specific query keys synchronously before first render. Preferred for components that display data without side-effects.

```ts
import { renderWithData } from "@/test/render"
import { myListQueryOptions } from "@/features/my-service/data"

it("renders the list", () => {
  renderWithData(
    <MyList />,
    [[myListQueryOptions().queryKey, [{ id: "1", name: "Widget" }]]],
  )
  expect(screen.getByText("Widget")).toBeInTheDocument()
})
```

**Option B — MSW override**: controls the BFF response at the network boundary. Preferred for loading states, error states, and mutation flows.

```ts
import { server } from "@/test/server"
import { http, HttpResponse } from "msw"

describe("error state", () => {
  beforeEach(() => {
    server.use(http.get("/api/s3/buckets", () => new HttpResponse(null, { status: 500 })))
  })

  it("shows an error banner", async () => {
    render(<BucketList />)
    expect(await screen.findByRole("alert")).toBeInTheDocument()
  })
})
```

### Never mock TanStack Router

Do not `vi.mock("@tanstack/react-router")` to stub `useNavigate`, `useParams`, or `useSearch`. Instead use `renderWithRouter` — it mounts the component inside a real router backed by in-memory history:

```ts
import { renderWithRouter } from "@/test/render"

// Component reads a path param via useParams
it("shows the repository name", () => {
  renderWithRouter(RepositoryDetail, {
    path: "/ecr/$name",
    initialEntry: "/ecr/api",
  })
  expect(screen.getByRole("heading", { name: "api" })).toBeInTheDocument()
})

// Assert navigation side-effects
it("navigates back after delete", async () => {
  const { user, router } = renderWithRouter(DeleteButton, {
    path: "/ecr/$name",
    initialEntry: "/ecr/api",
  })
  await user.click(screen.getByRole("button", { name: "Delete" }))
  expect(router.state.location.pathname).toBe("/ecr")
})
```

`renderWithRouter` mounts the component as the sole route at `path`. Supply `initialEntry` whenever the path contains dynamic segments.

### MSW handler scope

- Default handlers live in `web/src/test/handlers.ts`. They return empty-state responses and are active for every test.
- `server.use(...)` inside a `describe` or `it` overrides for that scope only. `afterEach(() => server.resetHandlers())` in `setup.ts` clears overrides automatically.
- `onUnhandledRequest: "error"` is set globally. If a component calls a BFF path with no matching handler, the test **fails**. Add a handler to `handlers.ts` when you add a new `/api/*` route.

### User events

Use `@testing-library/user-event` via the `user` value returned by `render` / `renderWithData` / `renderWithRouter`. Avoid `fireEvent` unless `userEvent` does not support the event.

```ts
it("submits the form", async () => {
  const { user } = render(<CreateQueueForm />)
  await user.type(screen.getByRole("textbox", { name: "Queue name" }), "my-queue")
  await user.click(screen.getByRole("button", { name: "Create" }))
  expect(await screen.findByText("my-queue")).toBeInTheDocument()
})
```

### Query selectors — prefer semantic roles

```ts
// ✅ Preferred
screen.getByRole("button", { name: "Delete" })
screen.getByRole("heading", { name: "ECR Repositories" })

// ✅ Acceptable
screen.getByText("backend/api")
screen.getByLabelText("Queue name")

// ❌ Avoid unless no semantic alternative exists
screen.getByTestId("delete-btn")
```

### DRY patterns

- Hoist shared setup (`beforeEach`, test data, MSW overrides) to the nearest enclosing `describe` block.
- Use `it.each` for repeated assertions differing only by input:

  ```ts
  it.each([
    ["MUTABLE", "Mutable"],
    ["IMMUTABLE", "Immutable"],
  ])("renders %s tag mutability", (mutability, label) => {
    renderWithData(<RepositoryDetail name="api" />, [
      [detailQueryOptions("api").queryKey, { ...baseRepo, imageTagMutability: mutability }],
    ])
    expect(screen.getByText(label)).toBeInTheDocument()
  })
  ```

- Shared fixtures belong at the top of the test file or in a `__fixtures__` file co-located with the test.

### Route metadata tests

Route `head` / `beforeLoad` callbacks do not need a render — test them directly:

```ts
it("sets the page title", async () => {
  const head = await MyRoute.options.head?.({} as never)
  expect(head?.meta?.[0]?.title).toBe("My Page — Overcast")
})
```

### `getBy` vs `queryBy` vs `findBy` — pick the right variant

The wrong query variant is the single biggest cause of false positives and confusing errors.

| Variant | Returns | Throws if absent | Use when |
| ------- | ------- | ---------------- | -------- |
| `getBy*` | element | ✅ immediately | Element **must** be in the DOM right now |
| `queryBy*` | element \| null | ❌ | Asserting an element is **absent** |
| `findBy*` | Promise\<element\> | ✅ after timeout | Element appears **after** an async operation |

```ts
// ✅ Asserting presence after data loads
expect(await screen.findByRole("row", { name: "my-queue" })).toBeInTheDocument()

// ✅ Asserting absence — must use queryBy, not getBy
expect(screen.queryByText("Loading…")).not.toBeInTheDocument()

// ❌ getBy + not.toBeInTheDocument() throws before the assertion runs
expect(screen.getByText("Loading…")).not.toBeInTheDocument()
```

Prefer `findBy*` over `waitFor(() => expect(screen.getBy*(...)))` for single-element waits — it reads more clearly and retries automatically. Use `waitFor` only when waiting for a non-DOM condition (e.g. a mock was called, `router.state` changed).

### Always `await` async interactions

Every `user.*` call and every `findBy*` query returns a Promise. A missing `await` produces a test that passes for the wrong reason — the assertion runs before the interaction completes.

```ts
// ✅
await user.click(screen.getByRole("button", { name: "Delete" }))
expect(await screen.findByText("Deleted")).toBeInTheDocument()

// ❌ — assertion runs before click resolves; may pass incorrectly
user.click(screen.getByRole("button", { name: "Delete" }))
expect(screen.getByText("Deleted")).toBeInTheDocument()
```

Vitest's `--reporter=verbose` will warn about unhandled promises, but the safest habit is: if you call `user.*`, always `await` it.

### Scoped queries with `within()`

When the DOM contains multiple similar elements (table rows, list cards, dialogs), scope queries with `within()` to avoid ambiguous matches:

```ts
import { within } from "@/test/render"

const row = screen.getByRole("row", { name: /my-queue/ })
expect(within(row).getByText("ACTIVE")).toBeInTheDocument()
expect(within(row).getByRole("button", { name: "Delete" })).toBeEnabled()
```

### One concern per test

Each `it` block should test one logical scenario. Multiple unrelated assertions in a single test make failures hard to diagnose — when the test turns red you can't tell which assertion failed without reading the output carefully.

```ts
// ✅ — two separate, clearly named tests
it("shows the queue name", () => { … })
it("shows the queue URL", () => { … })

// ❌ — failure message is "expected 'sqs://…' to be in the document"; which part broke?
it("renders queue details", () => {
  expect(screen.getByText("my-queue")).toBeInTheDocument()
  expect(screen.getByText("sqs://localhost/000000000000/my-queue")).toBeInTheDocument()
  expect(screen.getByText("Standard")).toBeInTheDocument()
})
```

Exception: tightly coupled assertions about the same rendered element are fine in one test (e.g. checking both `aria-label` and `href` on the same link).

### Name tests as behaviour descriptions

Write `it("…")` strings as complete sentences describing what the component does under a given condition, not what code path is exercised:

```ts
// ✅
it("shows an error banner when the BFF returns 500")
it("disables the submit button while the mutation is in flight")
it("navigates to the detail page after successful creation")

// ❌
it("error handling")
it("submit button disabled")
it("test navigation")
```

Prefer the `describe("ComponentName > scenario")` → `it("does X")` structure so the full sentence reads naturally in the test output.

### What agents must NOT do (testing-specific)

- Never `vi.mock("@tanstack/react-query", ...)` — use cache seeding or MSW.
- Never `vi.mock("@tanstack/react-router", ...)` — use `renderWithRouter`.
- Never use snapshots — they become stale noise.
- Never import `render` from `@testing-library/react` directly.
- Never leave an unhandled MSW request in tests — add the route to `handlers.ts`.
- Never use `getBy*` to assert absence — use `queryBy*` + `.not.toBeInTheDocument()`.
- Never omit `await` before `user.*` calls or `findBy*` queries.
- Never put multiple unrelated assertions in a single `it` block — split them.
- Never name a test `"test 1"`, `"works"`, or `"renders"` — describe the behaviour.

---

## What agents must NOT do (web-specific)

- Never edit `web/src/routeTree.gen.ts` directly.
- Never call `fetch` directly from components — go through `web/src/services/api.ts`.
- Never use a plain `<input>` or `<Input>` for an AWS ARN field — use `<ResourceArnCombobox>`.
- Never add dependencies to `web/package.json` without justification.
- Never commit or push code that fails `cd web && npm run lint` or `cd web && npx tsc --noEmit`.
