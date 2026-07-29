import { useEffect, useRef } from "react"
import { useRouterState } from "@tanstack/react-router"
import { findServiceForPathname } from "@/lib/service-registry"

/**
 * Tab favicon — the icon of the service you are looking at, so a row of a
 * dozen Overcast tabs is readable at a glance (the AWS console does the same).
 *
 * The glyph is not a second, hand-maintained icon table: the registry's own
 * icon component is rendered offscreen and serialised, so the tab always shows
 * exactly what the sidebar shows. That is also what makes it icon-set
 * agnostic — swap the registry to coloured icons and the favicons follow,
 * because their own fills win over the `currentColor` ink set here.
 *
 * Non-service routes (dashboard, map, docs) restore the Overcast mark.
 */

/**
 * Ink is the Overcast mark's own stroke, per scheme — see public/favicon.svg.
 * It travels inside the SVG because a favicon is rendered as an image, with no
 * access to the page's styles; the class keeps the rule from reaching anything
 * else while the same element is also mounted (hidden) in the document.
 */
const INK_CLASS = "overcast-favicon-ink"
const INK_CSS =
  `.${INK_CLASS}{color:#31536F}` +
  `@media (prefers-color-scheme:dark){.${INK_CLASS}{color:#89BCE2}}`

const FALLBACK_HREF = "/favicon.svg"

/**
 * The href index.html shipped with, captured before anything overwrites it so
 * the fallback survives a base path and cannot drift from the static asset.
 */
let defaultHref: string | undefined

function faviconLink(): HTMLLinkElement {
  const existing = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
  defaultHref ??= existing?.getAttribute("href") ?? FALLBACK_HREF
  if (existing) return existing

  const link = document.createElement("link")
  link.rel = "icon"
  document.head.append(link)
  return link
}

function paint(glyph: Element | null | undefined) {
  const link = faviconLink()
  const href = glyph
    ? `data:image/svg+xml,${encodeURIComponent(new XMLSerializer().serializeToString(glyph))}`
    : (defaultHref ?? FALLBACK_HREF)
  if (link.getAttribute("href") !== href) link.setAttribute("href", href)
}

export function ServiceFavicon() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const service = findServiceForPathname(pathname)
  const host = useRef<HTMLSpanElement>(null)

  useEffect(() => {
    paint(host.current?.firstElementChild)
  }, [service])

  if (!service) return null

  // The wrapper carries no xmlns of its own: the serialiser emits the
  // namespace, and a second declaration would make the data URI invalid XML.
  // It exists to host the scheme-aware ink the nested glyph inherits.
  const Icon = service.icon
  return (
    <span ref={host} hidden>
      <svg viewBox="0 0 24 24" width="24" height="24" className={INK_CLASS}>
        <style>{INK_CSS}</style>
        <Icon />
      </svg>
    </span>
  )
}
