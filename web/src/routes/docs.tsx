import { createFileRoute, Link } from "@tanstack/react-router"
import { useQuery, queryOptions } from "@tanstack/react-query"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkGithubAlerts from "remark-github-alerts"
import remarkRemoveComments from "remark-remove-comments"
import { BookOpen, ExternalLink } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"
import { DOCS_INDEX } from "@/generated/docs-index"
import { cn } from "@/lib/utils"

interface DocsSearchParams {
  path?: string
}

export const Route = createFileRoute("/docs")({
  validateSearch: (search): DocsSearchParams => ({
    path: typeof search.path === "string" ? search.path : "README.md",
  }),
  component: DocsPage,
  head: () => ({ meta: [{ title: "Documentation — Overcast" }] }),
})

async function fetchDoc(path: string): Promise<string> {
  const res = await fetch(`/api/docs/page?path=${encodeURIComponent(path)}`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

function DocsPage() {
  const { path = "README.md" } = Route.useSearch()
  const currentDoc = DOCS_INDEX.find((doc) => doc.href === path) ?? DOCS_INDEX[0]
  const sections = Array.from(new Set(DOCS_INDEX.map((doc) => doc.section)))
  const { data, isLoading, isError } = useQuery(
    queryOptions({
      queryKey: ["docs-page", path],
      queryFn: () => fetchDoc(path),
      staleTime: Infinity,
      retry: false,
    }),
  )

  return (
    <main className="mx-auto grid w-full max-w-7xl grid-cols-1 gap-5 px-6 py-6 lg:grid-cols-[18rem_minmax(0,1fr)_14rem]">
      <aside className="hidden min-h-0 lg:block">
        <div className="sticky top-6 max-h-[calc(100vh-3rem)] overflow-y-auto rounded-xl border border-border bg-bg-elevated p-3">
          <div className="mb-3 flex items-center gap-2 px-2 text-sm font-medium text-fg">
            <BookOpen className="h-4 w-4 text-accent" />
            Docs
          </div>
          {sections.map((section) => (
            <div key={section} className="mb-4">
              <div className="mb-1 px-2 text-xs font-medium text-fg-subtle">{section}</div>
              <div className="space-y-0.5">
                {DOCS_INDEX.filter((doc) => doc.section === section).map((doc) => (
                  <Link
                    key={doc.href}
                    to="/docs"
                    search={{ path: doc.href }}
                    className={cn(
                      "block rounded-md px-2 py-1.5 text-sm transition-colors",
                      doc.href === path
                        ? "bg-accent-muted text-fg"
                        : "text-fg-muted hover:bg-bg-subtle hover:text-fg",
                    )}
                  >
                    {doc.title}
                  </Link>
                ))}
              </div>
            </div>
          ))}
        </div>
      </aside>

      <div className="min-w-0">
        <div className="mb-4 flex items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm text-fg-subtle">
              <BookOpen className="h-4 w-4" />
              {currentDoc.section}
            </div>
            <h1 className="mt-1 text-2xl font-semibold text-fg">{currentDoc.title}</h1>
            <p className="mt-1 text-sm text-fg-subtle">{currentDoc.description}</p>
          </div>
          <Button variant="outline" size="sm" asChild>
            <Link to="/">Back to dashboard</Link>
          </Button>
        </div>

        <article className="rounded-xl border border-border bg-bg-elevated p-5">
          {isLoading && (
            <div className="flex items-center justify-center py-20">
              <Spinner className="h-5 w-5" />
            </div>
          )}
          {isError && <p className="py-12 text-center text-sm text-fg-muted">Doc not found.</p>}
          {data && (
            <div className="prose prose-sm max-w-none">
              <ReactMarkdown
                remarkPlugins={[remarkGfm, remarkGithubAlerts, remarkRemoveComments]}
                components={{
                  h2: ({ node: _node, children, ...props }) => (
                    <h2 id={slug(String(children))} {...props}>
                      {children}
                    </h2>
                  ),
                  h3: ({ node: _node, children, ...props }) => (
                    <h3 id={slug(String(children))} {...props}>
                      {children}
                    </h3>
                  ),
                  a: ({ node: _node, href, children, ...props }) => {
                    const internal = href?.endsWith(".md") ? href : null
                    if (internal) {
                      return (
                        <Link
                          to="/docs"
                          search={{ path: resolveDocsHref(path, internal) }}
                          className="text-accent underline underline-offset-2"
                        >
                          {children}
                        </Link>
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
                        {children}
                        <ExternalLink className="inline h-3 w-3 opacity-60" />
                      </a>
                    )
                  },
                  table: ({ node: _node, children, ...props }) => (
                    <div className="overflow-x-auto">
                      <table className="w-full border-collapse text-xs" {...props}>
                        {children}
                      </table>
                    </div>
                  ),
                  th: ({ node: _node, children, ...props }) => (
                    <th
                      className="border border-border bg-bg-muted px-3 py-1.5 text-left font-semibold text-fg"
                      {...props}
                    >
                      {children}
                    </th>
                  ),
                  td: ({ node: _node, children, ...props }) => (
                    <td className="border border-border px-3 py-1.5 text-fg-muted" {...props}>
                      {children}
                    </td>
                  ),
                }}
              >
                {data}
              </ReactMarkdown>
            </div>
          )}
        </article>
      </div>

      <aside className="hidden xl:block">
        <div className="sticky top-6 rounded-xl border border-border bg-bg-elevated p-3">
          <div className="mb-2 text-xs font-medium text-fg-subtle">On this page</div>
          <div className="space-y-1">
            {currentDoc.headings
              .filter((heading) => heading.depth > 1 && heading.depth <= 3)
              .slice(0, 18)
              .map((heading) => (
                <a
                  key={`${heading.depth}:${heading.id}`}
                  href={`#${heading.id}`}
                  className={cn(
                    "block rounded px-2 py-1 text-xs text-fg-muted hover:bg-bg-subtle hover:text-fg",
                    heading.depth === 3 && "pl-4",
                  )}
                >
                  {heading.text}
                </a>
              ))}
          </div>
        </div>
      </aside>
    </main>
  )
}

function resolveDocsHref(currentPath: string, href: string): string {
  if (href.startsWith("./")) href = href.slice(2)
  if (href.startsWith("../")) {
    const currentDir = currentPath.split("/").slice(0, -1)
    for (const part of href.split("/")) {
      if (part === "..") currentDir.pop()
      else currentDir.push(part)
    }
    return currentDir.join("/")
  }
  if (href.includes("/")) return href
  const dir = currentPath.split("/").slice(0, -1).join("/")
  return dir ? `${dir}/${href}` : href
}

function slug(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
}
