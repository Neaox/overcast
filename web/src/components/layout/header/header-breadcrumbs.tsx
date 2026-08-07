import { Fragment } from "react"
import { Link, useRouterState } from "@tanstack/react-router"
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
  const crumbs = ancestorsOf(pathname)
  if (crumbs.length === 0) return null
  return (
    <nav aria-label="Breadcrumb" className="hidden min-w-0 items-center gap-2 font-mono text-xs sm:flex">
      <span className="shrink-0 text-border">/</span>
      {crumbs.map((ancestor) => (
        <Fragment key={ancestor.to}>
          <Link to={ancestor.to} className="min-w-0 truncate text-fg-muted transition-colors hover:text-fg">
            {ancestor.label}
          </Link>
          <span className="shrink-0 text-border">/</span>
        </Fragment>
      ))}
    </nav>
  )
}
