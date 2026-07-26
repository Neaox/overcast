import { Fragment } from "react"
import { Link, useRouterState } from "@tanstack/react-router"
import { cn } from "@/lib/utils"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useConnectionStatus } from "@/hooks/use-connection-status"
import { ALL_SERVICES, findServiceKeyForPathname } from "@/lib/nav-services"

interface Ancestor {
  to: string
  label: string
}

/**
 * Ancestors of the current route, excluding the current segment — the page's
 * own heading already names it.
 */
function ancestorsOf(pathname: string): Ancestor[] {
  const segments = pathname.split("/").filter(Boolean)
  return segments.slice(0, -1).map((segment, index) => {
    const to = "/" + segments.slice(0, index + 1).join("/")
    const service = ALL_SERVICES.find((s) => s.key === findServiceKeyForPathname(to))
    return { to, label: service?.to === to ? service.label : decodeURIComponent(segment) }
  })
}

export function HeaderBreadcrumbs() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const endpoint = useEndpoint()
  const { isOnline } = useConnectionStatus()

  if (pathname === "/") return null

  return (
    <nav
      aria-label="Breadcrumb"
      className="hidden min-w-0 items-center gap-2 font-mono text-xs sm:flex"
    >
      <span
        className={cn(
          "h-[7px] w-[7px] shrink-0 rounded-full",
          isOnline === true ? "bg-success" : "bg-fg-subtle",
        )}
        title={
          isOnline === true ? "Connected" : isOnline === false ? "Disconnected" : "Connecting…"
        }
      />
      <span className="shrink-0 text-fg-subtle">{endpoint.label ?? endpoint.baseUrl}</span>
      {ancestorsOf(pathname).map((ancestor) => (
        <Fragment key={ancestor.to}>
          <span className="shrink-0 text-border">/</span>
          <Link
            to={ancestor.to}
            className="min-w-0 truncate text-fg-muted transition-colors hover:text-fg"
          >
            {ancestor.label}
          </Link>
        </Fragment>
      ))}
    </nav>
  )
}
