import { useEffect } from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useQuery, queryOptions } from "@tanstack/react-query"
import ReactMarkdown, { type Components } from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkGithubAlerts from "remark-github-alerts"
import remarkRemoveComments from "remark-remove-comments"
import { BookOpen, ExternalLink } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"
import { Skeleton } from "@/components/ui/skeleton"
import { CodeTabsGroup, CodeTabsPanel } from "@/features/docs/code-tabs"
import { MarkdownCodeBlock } from "@/features/docs/markdown-code"
import remarkCodeTabs from "@/lib/remark-code-tabs"
import remarkHeadingIds from "@/lib/remark-heading-ids"
import { slug } from "@/lib/slug"
import { cn } from "@/lib/utils"
import type { DocsNavEntry } from "@/types/common"

export { slug }

// The custom element names remark-code-tabs emits; Components is keyed by
// intrinsic tag names only, hence the cast.
const codeTabsComponents = {
  "code-tabs-group": CodeTabsGroup,
  "code-tabs-panel": CodeTabsPanel,
} as unknown as Components

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

/**
 * Drop the doc's leading H1 only when it repeats the page chrome's title
 * (the header already renders title + description from the docs index).
 * A doc whose top heading says something different keeps it.
 */
function stripLeadingH1(content: string, pageTitle: string): string {
  const m = content.match(/^\s*# (.+)\r?\n+/)
  if (!m) return content
  return m[1].trim().toLowerCase() === pageTitle.trim().toLowerCase()
    ? content.slice(m[0].length)
    : content
}

async function fetchDoc(path: string): Promise<string> {
  const res = await fetch(`/api/docs/page?path=${encodeURIComponent(path)}`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

/**
 * The docs sidebar and per-page table of contents, from the same binary this
 * page already fetches every doc body from.
 *
 * It used to be `import { DOCS_NAV } from "@/docs-nav.gen"` — a 7,000-line
 * generated module committed to the repository and bundled into the SPA. Every
 * docs pull request rewrote hundreds of lines of it, so concurrent docs
 * branches conflicted on a file nobody had written by hand. The server derives
 * it from the docs it already embeds (internal/docsindex), and the page that
 * cannot render without /api/docs/page loses nothing by also needing
 * /api/docs/nav.
 */
async function fetchNav(): Promise<DocsNavEntry[]> {
  const res = await fetch("/api/docs/nav")
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const body = (await res.json()) as { entries?: DocsNavEntry[] }
  return body.entries ?? []
}

const navQueryOptions = queryOptions({
  queryKey: ["docs-nav"],
  queryFn: fetchNav,
  staleTime: Infinity,
  retry: false,
})

function DocsPage() {
  const { path = "README.md" } = Route.useSearch()
  const { data: nav, isPending: navPending } = useQuery(navQueryOptions)
  const entries = nav ?? []
  // Before the nav arrives — and for a path the nav does not list — the page
  // still has to render its header. The path is the one thing it always knows,
  // so fall back to that rather than to a blank title that then shifts.
  const currentDoc = entries.find((doc) => doc.href === path) ?? placeholderDoc(path)
  const sections = Array.from(new Set(entries.map((doc) => doc.section)))
  const { data, isLoading, isError } = useQuery(
    queryOptions({
      queryKey: ["docs-page", path],
      queryFn: () => fetchDoc(path),
      staleTime: Infinity,
      retry: false,
    }),
  )

  // Deep links: heading ids exist (remarkHeadingIds assigns them), but on
  // client-side navigation the content arrives after the route, so the
  // browser's native anchor scroll never fires - do it once the doc renders.
  useEffect(() => {
    const anchor = window.location.hash.slice(1)
    if (!anchor || !data) return
    document.getElementById(anchor)?.scrollIntoView({ block: "start" })
  }, [data])

  return (
    /* A <div>, not a second <main>: the app shell already owns the page's one main
       landmark, and this route renders inside it. */
    <div className="mx-auto grid w-full max-w-7xl grid-cols-1 gap-5 px-6 py-6 lg:grid-cols-[18rem_minmax(0,1fr)_14rem]">
      <aside aria-label="Documentation" className="hidden min-h-0 lg:block">
        <div className="sticky top-6 max-h-[calc(100vh-3rem)] overflow-y-auto rounded-xl border border-border bg-bg-elevated p-3">
          <div className="mb-3 flex items-center gap-2 px-2 text-sm font-medium text-fg">
            <BookOpen className="h-4 w-4 text-accent" />
            Docs
          </div>
          {navPending && (
            <div className="space-y-2 px-2 py-1" aria-hidden>
              {SIDEBAR_SKELETON_WIDTHS.map((width, i) => (
                <Skeleton key={i} depth={i % 2 === 0 ? "1" : "2"} className={cn("h-4", width)} />
              ))}
            </div>
          )}
          {sections.map((section) => (
            <div key={section} className="mb-4">
              <div className="mb-1 px-2 text-xs font-medium text-fg-subtle">{section}</div>
              <div className="space-y-0.5">
                {entries
                  .filter((doc) => doc.section === section)
                  .map((doc) => (
                    <Link
                      key={doc.href}
                      to="/docs"
                      search={{ path: doc.href }}
                      className={cn(
                        "block rounded-md px-2 py-1.5 text-sm transition-colors",
                        doc.href === path
                          ? "bg-accent-muted text-fg"
                          : "text-fg-muted hover:bg-accent-muted hover:text-accent",
                      )}
                    >
                      {doc.title}
                    </Link>
                  ))}
              </div>
            </div>
          ))}
          <div className="mt-4 border-t border-border px-2 pt-3 text-xs text-fg-subtle">
            These docs cover <span className="text-fg-muted">using</span> Overcast. Developing
            Overcast itself? Contributor docs live in{" "}
            <a
              href="https://github.com/overcast-sh/overcast/tree/main/docs/dev"
              target="_blank"
              rel="noreferrer"
              className="text-accent underline underline-offset-2"
            >
              docs/dev
            </a>{" "}
            in the repository.
          </div>
        </div>
      </aside>

      <div className="min-w-0">
        <div className="mb-4 flex items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-2 text-sm text-fg-subtle">
              <BookOpen className="h-4 w-4" />
              {currentDoc.section}
            </div>
            <h1 className="mt-1 font-mono text-2xl font-semibold text-fg">{currentDoc.title}</h1>
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
                // Order matters twice over: remarkHeadingIds must run before
                // remarkCodeTabs, which lifts the ids off the headings it
                // folds into tabs, and remarkCodeTabs before
                // remarkRemoveComments, which strips the sentinel comments it
                // keys on.
                remarkPlugins={[
                  remarkGfm,
                  remarkGithubAlerts,
                  remarkHeadingIds,
                  remarkCodeTabs,
                  remarkRemoveComments,
                ]}
                components={{
                  ...codeTabsComponents,
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
                    <div tabIndex={0} className="overflow-x-auto">
                      <table className="w-full border-collapse text-xs" {...props}>
                        {children}
                      </table>
                    </div>
                  ),
                  th: ({ node: _node, children, ...props }) => (
                    <th
                      className="border border-border bg-bg-muted px-3 py-1.5 text-left font-mono font-semibold text-fg"
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
                  pre: MarkdownCodeBlock,
                }}
              >
                {stripLeadingH1(data, currentDoc.title)}
              </ReactMarkdown>
            </div>
          )}
        </article>
      </div>

      <aside aria-label="On this page" className="hidden xl:block">
        <div className="sticky top-6 rounded-xl border border-border bg-bg-elevated p-3">
          <div className="mb-2 font-mono text-xs font-medium text-fg-subtle">On this page</div>
          <div className="space-y-1">
            {currentDoc.headings
              .filter((heading) => heading.depth > 1 && heading.depth <= 3)
              .slice(0, 18)
              .map((heading) => (
                <a
                  key={`${heading.depth}:${heading.id}`}
                  href={`#${heading.id}`}
                  className={cn(
                    "block rounded px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-accent-muted hover:text-accent",
                    heading.depth === 3 && "pl-4",
                  )}
                >
                  {heading.text}
                </a>
              ))}
          </div>
        </div>
      </aside>
    </div>
  )
}

// Placeholder bar widths for the sidebar while the navigation is in flight —
// enough of them to hold the column's height, so the page does not jump when
// the real list arrives.
const SIDEBAR_SKELETON_WIDTHS = [
  "w-24",
  "w-32",
  "w-28",
  "w-36",
  "w-20",
  "w-32",
  "w-24",
  "w-28",
  "w-36",
  "w-24",
] as const

/** The header's stand-in while the nav is in flight, or if it never arrives. */
function placeholderDoc(path: string): DocsNavEntry {
  return {
    path: `docs/${path}`,
    href: path,
    title: path,
    description: "",
    section: "Documentation",
    tags: [],
    headings: [],
  }
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
