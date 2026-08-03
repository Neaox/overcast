/**
 * Router-level error fallback — `defaultErrorComponent` in main.tsx.
 *
 * Until this existed the app relied on TanStack Router's built-in fallback,
 * which prints the message and nothing else: no way to retry without a reload,
 * no stack, and — for by far the most common failure here, the emulator not
 * answering — no hint that the endpoint is the thing to look at.
 *
 * The shell (sidebar, header) lives outside the route Outlet, so for a route
 * error this replaces the content area only and navigation stays available.
 * It is also rendered bare when the root route itself fails, which is why it
 * depends on nothing beyond the router and the endpoint store.
 */
import { useRouter, useRouterState, Link, type ErrorComponentProps } from "@tanstack/react-router"
import { AlertTriangle, Home, PlugZap, RefreshCw, RotateCcw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { endpointStore } from "@/services/endpoint-store"
import { cn } from "@/lib/utils"

type ErrorKind = "connection" | "chunk" | "unknown"

/**
 * Which of three stories to tell. The distinction earns its place because each
 * kind has a different fix: start the emulator, reload the page, or report it.
 */
function classify(error: Error): ErrorKind {
  const text = `${error.name}: ${error.message}`
  if (
    /failed to fetch dynamically imported module|error loading dynamically imported module|importing a module script failed/i.test(
      text,
    )
  ) {
    return "chunk"
  }
  if (/failed to fetch|network\s?error|load failed|econnrefused|fetch failed/i.test(text)) {
    return "connection"
  }
  return "unknown"
}

const COPY: Record<ErrorKind, { title: string; hint: string }> = {
  connection: {
    title: "Can't reach the emulator",
    hint: "Nothing answered at the configured endpoint. Check that Overcast is running, then try again.",
  },
  chunk: {
    title: "This page didn't finish loading",
    hint: "A part of the app failed to download — usually a stale tab after Overcast was updated. Reloading fetches the current build.",
  },
  unknown: {
    title: "Something went wrong",
    hint: "This page hit an error it could not recover from. Retrying re-runs the page's data; the details below are worth attaching to a bug report.",
  },
}

const ICON: Record<ErrorKind, typeof AlertTriangle> = {
  connection: PlugZap,
  chunk: RefreshCw,
  unknown: AlertTriangle,
}

/** Everything a bug report wants, in one paste. */
function report(error: Error, kind: ErrorKind, pathname: string): string {
  const endpoint = endpointStore.get()
  return [
    `Overcast web UI error (${kind})`,
    `route:     ${pathname}`,
    `endpoint:  ${endpoint.baseUrl}`,
    `region:    ${endpoint.region}`,
    `when:      ${new Date().toISOString()}`,
    `agent:     ${typeof navigator === "undefined" ? "unknown" : navigator.userAgent}`,
    "",
    `${error.name}: ${error.message}`,
    error.stack ?? "(no stack)",
  ].join("\n")
}

export function RouteError({ error, reset }: ErrorComponentProps) {
  const router = useRouter()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const err = error instanceof Error ? error : new Error(String(error))
  const kind = classify(err)
  const { title, hint } = COPY[kind]
  const Icon = ICON[kind]
  const endpoint = endpointStore.get()

  // Reset clears the error boundary; invalidate re-runs the route's loader and
  // queries, so "Try again" retries the thing that failed rather than just
  // re-rendering the failure.
  const retry = () => {
    reset()
    void router.invalidate()
  }

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-10 sm:py-16">
      <div className="flex flex-col items-center text-center">
        <div
          className={cn(
            "mb-6 flex h-16 w-16 items-center justify-center rounded-2xl border",
            kind === "connection"
              ? "border-warning/40 bg-warning/10 text-warning"
              : "border-danger/40 bg-danger/10 text-danger",
          )}
        >
          <Icon className="h-8 w-8" aria-hidden="true" />
        </div>

        <h1 className="font-mono text-xl font-semibold text-fg sm:text-2xl">{title}</h1>
        <p className="mt-2 max-w-prose text-sm text-fg-muted">{hint}</p>

        {kind === "connection" && (
          <p className="mt-3 text-sm text-fg-muted">
            Endpoint:{" "}
            <code className="rounded bg-bg-elevated px-1.5 py-0.5 font-mono text-xs text-fg">
              {endpoint.baseUrl}
            </code>
          </p>
        )}
      </div>

      {/* The message itself, verbatim — the one thing every reader wants. */}
      <div className="mt-6 overflow-hidden rounded-card border border-border bg-bg-elevated">
        <div className="flex items-start justify-between gap-2 border-b border-border-muted px-3 py-2">
          <span className="font-mono text-[10px] tracking-[0.14em] text-fg-subtle uppercase">
            {err.name || "Error"}
          </span>
          <CopyButton
            value={report(err, kind, pathname)}
            noun="error details"
            tone="inline"
            className="mt-0.5"
          />
        </div>
        <pre className="max-h-40 overflow-auto px-3 py-2 font-mono text-xs wrap-break-word whitespace-pre-wrap text-fg">
          {err.message || "No message"}
        </pre>
      </div>

      {/* Actions stack on a narrow viewport and sit in a row from `sm` up. */}
      <div className="mt-4 flex flex-col gap-2 sm:flex-row sm:justify-center">
        <Button onClick={retry} className="w-full sm:w-auto">
          <RotateCcw className="h-4 w-4" aria-hidden="true" />
          Try again
        </Button>
        <Button
          variant="secondary"
          onClick={() => window.location.reload()}
          className="w-full sm:w-auto"
        >
          <RefreshCw className="h-4 w-4" aria-hidden="true" />
          Reload page
        </Button>
        <Button variant="ghost" asChild className="w-full sm:w-auto">
          <Link to="/">
            <Home className="h-4 w-4" aria-hidden="true" />
            Dashboard
          </Link>
        </Button>
      </div>

      {err.stack && (
        <details className="mt-6 rounded-card border border-border bg-bg-elevated">
          <summary className="cursor-pointer px-3 py-2 font-mono text-xs text-fg-muted select-none hover:text-fg">
            Stack trace
          </summary>
          <pre className="max-h-64 overflow-auto border-t border-border-muted px-3 py-2 font-mono text-[11px] leading-relaxed text-fg-subtle">
            {err.stack}
          </pre>
        </details>
      )}

      {/* Which page this was — the shell's breadcrumb is gone if the root
          route is what failed, and a bug report is worth more with it. */}
      <p className="mt-4 text-center font-mono text-[11px] text-fg-subtle">{pathname}</p>
    </div>
  )
}
