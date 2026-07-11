import { useLocation, useNavigate } from "@tanstack/react-router"
import { useQuery, queryOptions } from "@tanstack/react-query"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkGithubAlerts from "remark-github-alerts"
import remarkRemoveComments from "remark-remove-comments"
import { BookOpen, ExternalLink } from "lucide-react"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

// ─── Service name → UI route map ─────────────────────────────────────────────
// Matches the docs/services/{name}.md filenames to their app routes.
const SERVICE_ROUTES: Record<string, string> = {
  s3: "/s3",
  sqs: "/sqs",
  dynamodb: "/dynamodb",
  sns: "/sns",
  ses: "/ses",
  secretsmanager: "/secretsmanager",
  lambda: "/lambda",
  kinesis: "/kinesis",
  pipes: "/pipes",
  iam: "/iam",
  cloudformation: "/cloudformation",
  ec2: "/ec2",
  ecs: "/ecs",
  cognito: "/cognito",
  appsync: "/appsync",
  apigateway: "/apigateway",
  cloudfront: "/cloudfront",
  rds: "/rds",
  stepfunctions: "/stepfunctions",
  waf: "/waf",
  shield: "/shield",
  kms: "/kms",
  ssm: "/ssm",
  sts: "/sts",
  cloudwatch: "/cloudwatch",
  "cloudwatch-logs": "/cloudwatch",
  appregistry: "/applications",
}

/**
 * Convert a *.md href (e.g. "lambda.md" or "../services/lambda.md") to the
 * corresponding in-app route with #docs hash.  Returns null if not a service doc link.
 */
function serviceDocHref(href: string): string | null {
  const stem = href.replace(/^.*[\\/]/, "").replace(/\.md$/i, "")
  const route = SERVICE_ROUTES[stem]
  return route ? `${route}#docs` : null
}

/** Strip a trailing .md from a string (used for link labels). */
function stripMd(s: string): string {
  return typeof s === "string" ? s.replace(/\.md$/i, "") : s
}

// ─── API helper ─────────────────────────────────────────────────────────────

async function fetchServiceDocs(service: string): Promise<string> {
  const res = await fetch(`/api/docs/${encodeURIComponent(service)}`)
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
  const {
    data: markdown,
    isLoading,
    isError,
  } = useQuery(
    queryOptions({
      queryKey: ["service-docs", service],
      queryFn: () => fetchServiceDocs(service),
      enabled: open,
      staleTime: Infinity, // docs don't change during a session
      retry: false,
    }),
  )

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <BookOpen className="h-4 w-4 text-fg-muted" />
            {label} — Service Docs
          </DialogTitle>
        </DialogHeader>
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
            <div className="prose prose-sm prose-invert max-w-none px-1 pb-4">
              <ReactMarkdown
                remarkPlugins={[remarkGfm, remarkGithubAlerts, remarkRemoveComments]}
                urlTransform={(url) => serviceDocHref(url) ?? url}
                components={{
                  // Open links in a new tab; inter-doc *.md links open in the same app
                  a: ({ node: _n, children, href, ...props }) => {
                    const internal = href ? serviceDocHref(href) : null
                    const resolvedHref = internal ?? href
                    // Strip .md from string children used as label
                    const label = typeof children === "string" ? stripMd(children) : children
                    if (internal) {
                      return (
                        <a
                          href={resolvedHref}
                          className="text-accent underline underline-offset-2"
                          {...props}
                        >
                          {label}
                        </a>
                      )
                    }
                    return (
                      <a
                        href={resolvedHref}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex items-center gap-0.5 text-accent underline underline-offset-2"
                        {...props}
                      >
                        {label}
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
                      className="border border-border bg-bg-muted px-3 py-1.5 text-left font-semibold text-fg"
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
                  code: ({ node: _n, children, className, ...props }) => {
                    // Fenced code block
                    const isBlock = className?.startsWith("language-")
                    if (isBlock) {
                      return (
                        <pre className="overflow-auto rounded-md border border-border bg-bg-muted p-3 font-mono text-xs text-fg">
                          <code {...props}>{children}</code>
                        </pre>
                      )
                    }
                    // Inline code: detect `something.md` and render as an inter-doc link
                    const text = typeof children === "string" ? children : ""
                    const internal = text.match(/^[\w-]+\.md$/i) ? serviceDocHref(text) : null
                    if (internal) {
                      return (
                        <a
                          href={internal}
                          className="rounded bg-bg-muted px-1 py-0.5 font-mono text-xs text-accent underline underline-offset-2"
                        >
                          {stripMd(text)}
                        </a>
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
                          className="mb-1.5 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide"
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
                        note: { border: "border-blue-400/60", titleColor: "oklch(0.67 0.19 240)" },
                        tip: { border: "border-green-400/60", titleColor: "oklch(0.67 0.16 145)" },
                        important: { border: "border-purple-400/60", titleColor: "oklch(0.67 0.19 290)" },
                        warning: { border: "border-amber-400/60", titleColor: "oklch(0.78 0.16 75)" },
                        caution: { border: "border-red-400/60", titleColor: "oklch(0.67 0.19 25)" },
                      }
                      const s = styles[type ?? ""] ?? { border: "border-accent/40", titleColor: "" }
                      return (
                        <div
                          className={cn("mb-3 border-l-[3px] py-1 pl-3 pr-1", s.border)}
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
