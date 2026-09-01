import { useState } from "react"
import { useLocation, useNavigate } from "@tanstack/react-router"
import { useQuery, queryOptions } from "@tanstack/react-query"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkGithubAlerts from "remark-github-alerts"
import remarkRemoveComments from "remark-remove-comments"
import { BookOpen, ChevronLeft, ExternalLink } from "lucide-react"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"
import { MarkdownCodeBlock } from "@/features/docs/markdown-code"
import { classifyDocLink, docTitle, landingPath } from "@/features/docs/service-doc-links"
import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

/** Strip a trailing .md from a string (used for link labels). */
function stripMd(s: string): string {
  return typeof s === "string" ? s.replace(/\.md$/i, "") : s
}

// ─── API helper ─────────────────────────────────────────────────────────────

// One endpoint for every page the modal shows. /api/docs/page serves any
// published doc by path, which is what a sub-page needs; the older
// /api/docs/{service} route stays for external callers.
async function fetchDoc(path: string): Promise<string> {
  const res = await fetch(`/api/docs/page?path=${encodeURIComponent(path)}`)
  if (res.status === 404) throw new Error("not-found")
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

// ─── Modal ───────────────────────────────────────────────────────────────────

interface ServiceDocsModalProps {
  /** Matches the filename stem in docs/services/{service}.md */
  service: string
  /** Human-readable label, e.g. "S3", "SQS" */
  label: string
  open: boolean
  onClose: () => void
}

export function ServiceDocsModal({ service, label, open, onClose }: ServiceDocsModalProps) {
  // Which page the modal is showing. It starts on the service's landing page
  // and follows links down into docs/services/<key>/*.md in place: the
  // per-operation table is a sub-page now, and bouncing the reader out to a
  // browser tab for it would be a worse answer than the single scrolling page
  // the split replaced.
  const home = landingPath(service)
  const [path, setPath] = useState(home)

  // Reopening the modal, or opening it on a different service, starts at that
  // service's landing page rather than wherever it was last left. Adjusted
  // during render rather than in an effect: there is no external system to
  // synchronise with, and an effect would render the stale page first.
  const session = `${home}:${open}`
  const [lastSession, setLastSession] = useState(session)
  if (session !== lastSession) {
    setLastSession(session)
    setPath(home)
  }

  const {
    data: markdown,
    isLoading,
    isError,
  } = useQuery(
    queryOptions({
      queryKey: ["service-docs", path],
      queryFn: () => fetchDoc(path),
      enabled: open,
      staleTime: Infinity, // docs don't change during a session
      retry: false,
    }),
  )

  const onSubPage = path !== home

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <BookOpen className="h-4 w-4 text-fg-muted" />
            {onSubPage ? docTitle(path, label) : `${label} — Service Docs`}
          </DialogTitle>
        </DialogHeader>
        {onSubPage && (
          <button
            type="button"
            onClick={() => setPath(home)}
            className="-mb-1 flex items-center gap-0.5 self-start text-xs text-fg-muted hover:text-fg"
          >
            <ChevronLeft className="h-3 w-3" />
            {label}
          </button>
        )}
        <div className="mt-2 max-h-[70vh] min-h-0 overflow-y-auto">
          {isLoading && (
            <div className="flex items-center justify-center py-16">
              <Spinner className="h-5 w-5" />
            </div>
          )}
          {isError && (
            <p className="py-8 text-center text-sm text-fg-muted">
              No documentation available for this service.
            </p>
          )}
          {markdown && (
            <div className="prose prose-sm max-w-none px-1 pb-4 prose-invert">
              <ReactMarkdown
                remarkPlugins={[remarkGfm, remarkGithubAlerts, remarkRemoveComments]}
                urlTransform={(url) => url}
                components={{
                  // A link into another service's page hands off to that
                  // service's console route; a link to a sub-page (or to a
                  // service with no console route) loads here; everything else
                  // opens in a new tab.
                  a: ({ node: _n, children, href, ...props }) => {
                    const target = classifyDocLink(path, href ?? "")
                    // Strip .md from string children used as label
                    const text = typeof children === "string" ? stripMd(children) : children
                    if (target.kind === "route") {
                      return (
                        <a
                          href={target.href}
                          className="text-accent underline underline-offset-2"
                          {...props}
                        >
                          {text}
                        </a>
                      )
                    }
                    if (target.kind === "doc") {
                      return (
                        <button
                          type="button"
                          onClick={() => setPath(target.path)}
                          className="text-accent underline underline-offset-2"
                        >
                          {text}
                        </button>
                      )
                    }
                    return (
                      <a
                        href={href}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-0.5 text-accent underline underline-offset-2"
                        {...props}
                      >
                        {text}
                        <ExternalLink className="inline h-3 w-3 opacity-60" />
                      </a>
                    )
                  },
                  // Style tables
                  table: ({ node: _n, children, ...props }) => (
                    <div className="overflow-x-auto">
                      <table
                        className="w-full border-collapse text-xs [&_td:nth-child(2)]:whitespace-nowrap [&_th:nth-child(2)]:whitespace-nowrap"
                        {...props}
                      >
                        {children}
                      </table>
                    </div>
                  ),
                  th: ({ node: _n, children, ...props }) => (
                    <th
                      className="border border-border bg-bg-muted px-3 py-1.5 text-left font-mono font-semibold text-fg"
                      {...props}
                    >
                      {children}
                    </th>
                  ),
                  td: ({ node: _n, children, ...props }) => (
                    <td className="border border-border px-3 py-1.5 text-fg-muted" {...props}>
                      {children}
                    </td>
                  ),
                  // Fenced blocks render (highlighted) through the shared
                  // pre component; code here only ever shows inline.
                  pre: MarkdownCodeBlock,
                  code: ({ node: _n, children, ...props }) => {
                    // Inline code: `something.md` or `service/page.md` reads as
                    // an inter-doc reference, so render it as one.
                    const text = typeof children === "string" ? children : ""
                    const target = /^[\w-]+(\/[\w-]+)?\.md$/i.test(text)
                      ? classifyDocLink(path, text)
                      : { kind: "external" as const }
                    if (target.kind === "route") {
                      return (
                        <a
                          href={target.href}
                          className="rounded bg-bg-muted px-1 py-0.5 font-mono text-xs text-accent underline underline-offset-2"
                        >
                          {stripMd(text)}
                        </a>
                      )
                    }
                    if (target.kind === "doc") {
                      return (
                        <button
                          type="button"
                          onClick={() => setPath(target.path)}
                          className="rounded bg-bg-muted px-1 py-0.5 font-mono text-xs text-accent underline underline-offset-2"
                        >
                          {stripMd(text)}
                        </button>
                      )
                    }
                    return (
                      <code
                        className="rounded bg-bg-muted px-1 py-0.5 font-mono text-xs text-fg"
                        {...props}
                      >
                        {children}
                      </code>
                    )
                  },
                  h1: ({ node: _n, children, ...props }) => (
                    <h1 className="mt-6 mb-3 text-lg font-bold text-fg first:mt-0" {...props}>
                      {children}
                    </h1>
                  ),
                  h2: ({ node: _n, children, ...props }) => (
                    <h2 className="mt-5 mb-2 text-base font-semibold text-fg" {...props}>
                      {children}
                    </h2>
                  ),
                  h3: ({ node: _n, children, ...props }) => (
                    <h3 className="mt-4 mb-1.5 text-sm font-semibold text-fg" {...props}>
                      {children}
                    </h3>
                  ),
                  p: ({ node: _n, className, children, ...props }) => {
                    if (className === "markdown-alert-title") {
                      return (
                        <p
                          className={cn(sectionLabel, "mb-1.5 flex items-center gap-1.5")}
                          style={{ color: "var(--alert-title-color)" }}
                          {...props}
                        >
                          {children}
                        </p>
                      )
                    }
                    return (
                      <p className="mb-3 text-sm leading-relaxed text-fg-muted" {...props}>
                        {children}
                      </p>
                    )
                  },
                  ul: ({ node: _n, children, ...props }) => (
                    <ul className="mb-3 list-disc pl-5 text-sm text-fg-muted" {...props}>
                      {children}
                    </ul>
                  ),
                  ol: ({ node: _n, children, ...props }) => (
                    <ol className="mb-3 list-decimal pl-5 text-sm text-fg-muted" {...props}>
                      {children}
                    </ol>
                  ),
                  li: ({ node: _n, children, ...props }) => (
                    <li className="mb-1 leading-relaxed" {...props}>
                      {children}
                    </li>
                  ),
                  div: ({ node: _n, className, children, ...props }) => {
                    if (className?.includes("markdown-alert")) {
                      const type = className.match(/markdown-alert-(\w+)/)?.[1]
                      const styles: Record<string, { border: string; titleColor: string }> = {
                        note: { border: "border-accent/60", titleColor: "var(--accent)" },
                        tip: { border: "border-success/60", titleColor: "var(--success)" },
                        important: {
                          border: "border-cat-8/60",
                          titleColor: "var(--cat-8)",
                        },
                        warning: {
                          border: "border-warning/60",
                          titleColor: "var(--warning)",
                        },
                        caution: { border: "border-danger/60", titleColor: "var(--danger)" },
                      }
                      const s = styles[type ?? ""] ?? { border: "border-accent/40", titleColor: "" }
                      return (
                        <div
                          className={cn("mb-3 border-l-[3px] py-1 pr-1 pl-3", s.border)}
                          style={{ "--alert-title-color": s.titleColor } as React.CSSProperties}
                          {...props}
                        >
                          {children}
                        </div>
                      )
                    }
                    return (
                      <div className={className} {...props}>
                        {children}
                      </div>
                    )
                  },
                  blockquote: ({ node: _n, children, ...props }) => (
                    <blockquote
                      className="mb-3 border-l-2 border-accent/40 pl-3 text-sm text-fg-muted italic"
                      {...props}
                    >
                      {children}
                    </blockquote>
                  ),
                  hr: ({ node: _n, ...props }) => <hr className="my-4 border-border" {...props} />,
                }}
              >
                {markdown}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// ─── Button ──────────────────────────────────────────────────────────────────

interface ServiceDocsButtonProps {
  service: string
  label: string
  open: boolean
  onOpen: () => void
  onClose: () => void
}

export function ServiceDocsButton({
  service,
  label,
  open,
  onOpen,
  onClose,
}: ServiceDocsButtonProps) {
  return (
    <>
      <Button variant="ghost" size="sm" onClick={onOpen} title={`View ${label} docs`}>
        <BookOpen className="h-3.5 w-3.5" />
        Docs
      </Button>
      <ServiceDocsModal service={service} label={label} open={open} onClose={onClose} />
    </>
  )
}

// ─── Hash-controlled hook ────────────────────────────────────────────────────
// Components can call useDocsFromHash() to auto-open the docs modal when the
// URL contains #docs.  The hook returns [open, openFn, closeFn].

// eslint-disable-next-line react-refresh/only-export-components
export function useDocsFromHash(): [boolean, () => void, () => void] {
  const navigate = useNavigate()
  const location = useLocation()
  // TanStack Router strips the leading '#' from location.hash
  const open = location.hash === "docs"

  function openDocs() {
    void navigate({ hash: "docs" })
  }

  function closeDocs() {
    void navigate({ hash: "" })
  }

  return [open, openDocs, closeDocs]
}
