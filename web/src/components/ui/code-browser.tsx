/**
 * CodeBrowser — a VS Code-style multi-file editor with explorer tree and tabs.
 *
 * Generic: no service coupling. Provide files, a loader, and an onChange handler.
 *
 * Usage:
 *   <CodeBrowser
 *     files={[{ name: "index.js", size: 245 }, { name: "lib/utils.js", size: 180 }]}
 *     initialFile="index.js"
 *     initialValue="console.log('hi')"
 *     language="javascript"
 *     loadFile={async (path) => ({ content: "...", language: "javascript" })}
 *     onChange={(path, value) => { ... }}
 *     height="60vh"
 *   />
 */
import { useState, useCallback, useMemo, useRef, useEffect } from "react"
import Editor, { type OnMount } from "@monaco-editor/react"
import type * as Monaco from "monaco-editor"
import { ChevronRight, ChevronDown, FileCode, FolderOpen, Folder, X } from "lucide-react"
import { cn } from "@/lib/utils"

// ─── Public types ──────────────────────────────────────────────────────────

export interface BrowserFile {
  /** Path inside the archive, e.g. "src/index.js" */
  name: string
  /** Uncompressed size in bytes */
  size: number
}

export interface LoadedFile {
  content: string
  language: string
}

export interface CodeBrowserProps {
  /** Flat list of every file in the project. */
  files: BrowserFile[]
  /** Which file to show initially. */
  initialFile?: string
  /** Initial source content for the initial file (avoids an extra load). */
  initialValue?: string
  /** Language hint for the initial file. */
  language?: string
  /** Async loader: given a file path, return its content + language. */
  loadFile?: (path: string) => Promise<LoadedFile>
  /** Called whenever the user edits. */
  onChange?: (path: string, value: string) => void
  /** Called when the active (visible) file changes. */
  onActiveFileChange?: (path: string) => void
  /** Editor height. Default "60vh". */
  height?: string
  /** Read-only mode. */
  readOnly?: boolean
  /** Extra CSS class on the root container. */
  className?: string
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function basename(path: string): string {
  const i = path.lastIndexOf("/")
  return i < 0 ? path : path.slice(i + 1)
}

function fileIconColor(name: string): string {
  if (/\.[jt]sx?$/.test(name)) return "text-yellow-500"
  if (/\.py$/.test(name)) return "text-blue-400"
  if (/\.json$/.test(name)) return "text-yellow-600"
  if (/\.ya?ml$/.test(name)) return "text-rose-400"
  if (/\.md$/.test(name)) return "text-sky-400"
  if (/\.css$/.test(name)) return "text-purple-400"
  if (/\.html$/.test(name)) return "text-orange-400"
  if (/\.java$/.test(name)) return "text-red-500"
  if (/\.cs$/.test(name)) return "text-green-500"
  return "text-fg-muted"
}

function languageForPath(path: string): string {
  if (/\.tsx?$/.test(path)) return "typescript"
  if (/\.[mc]?jsx?$/.test(path)) return "javascript"
  if (/\.py$/.test(path)) return "python"
  if (/\.java$/.test(path)) return "java"
  if (/\.cs$/.test(path)) return "csharp"
  if (/\.json$/.test(path)) return "json"
  if (/\.ya?ml$/.test(path)) return "yaml"
  if (/\.md$/.test(path)) return "markdown"
  if (/\.html?$/.test(path)) return "html"
  if (/\.css$/.test(path)) return "css"
  if (/\.sh$|\.bash$/.test(path)) return "shell"
  if (/\.xml$/.test(path)) return "xml"
  return "plaintext"
}

// ─── Tree data structure ───────────────────────────────────────────────────

interface TreeNode {
  name: string // segment name ("src", "index.js")
  path: string // full path ("src/index.js")
  isDir: boolean
  children: TreeNode[]
  size: number
}

function buildTree(files: BrowserFile[]): TreeNode[] {
  const root: TreeNode = { name: "", path: "", isDir: true, children: [], size: 0 }

  for (const f of files) {
    const parts = f.name.split("/")
    let node = root
    for (let i = 0; i < parts.length; i++) {
      const segment = parts[i]
      const isLast = i === parts.length - 1
      const childPath = parts.slice(0, i + 1).join("/")
      let child = node.children.find((c) => c.name === segment)
      if (!child) {
        child = {
          name: segment,
          path: childPath,
          isDir: !isLast,
          children: [],
          size: isLast ? f.size : 0,
        }
        node.children.push(child)
      }
      node = child
    }
  }

  // Sort: directories first, then alphabetical
  function sortTree(nodes: TreeNode[]) {
    nodes.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
    for (const n of nodes) {
      if (n.isDir) sortTree(n.children)
    }
  }
  sortTree(root.children)

  // Collapse single-child directories: "src" -> "lib" -> "file.js" becomes "src/lib" -> "file.js"
  function collapse(nodes: TreeNode[]): TreeNode[] {
    return nodes.map((n) => {
      if (n.isDir) {
        n.children = collapse(n.children)
        if (n.children.length === 1 && n.children[0].isDir) {
          const merged = n.children[0]
          return {
            ...merged,
            name: n.name + "/" + merged.name,
            children: merged.children,
          }
        }
      }
      return n
    })
  }

  return collapse(root.children)
}

// ─── Component ─────────────────────────────────────────────────────────────

export function CodeBrowser({
  files,
  initialFile,
  initialValue,
  language,
  loadFile,
  onChange,
  onActiveFileChange,
  height = "60vh",
  readOnly = false,
  className = "",
}: CodeBrowserProps) {
  // ── File cache: path → { content, language } ──────────────────────────
  const [fileCache, setFileCache] = useState<Record<string, LoadedFile | undefined>>(() => {
    if (initialFile && initialValue != null) {
      return {
        [initialFile]: {
          content: initialValue,
          language: language ?? languageForPath(initialFile),
        },
      }
    }
    return {}
  })
  const [loadingFile, setLoadingFile] = useState<string | null>(null)

  // ── Open tabs & active tab ────────────────────────────────────────────
  const [openTabs, setOpenTabs] = useState<string[]>(() =>
    initialFile ? [initialFile] : files.length > 0 ? [files[0].name] : [],
  )
  const [activeFile, setActiveFile] = useState<string>(
    initialFile ?? (files.length > 0 ? files[0].name : ""),
  )

  // ── Explorer collapsed dirs ───────────────────────────────────────────
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  // ── Monaco ref ────────────────────────────────────────────────────────
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null)
  const monacoRef = useRef<typeof Monaco | null>(null)

  // ── Detect dark mode ──────────────────────────────────────────────────
  const isDark =
    typeof document !== "undefined" &&
    (document.documentElement.getAttribute("data-theme") === "dark" ||
      (document.documentElement.getAttribute("data-theme") == null &&
        window.matchMedia("(prefers-color-scheme: dark)").matches))

  // ── Tree ──────────────────────────────────────────────────────────────
  const tree = useMemo(() => buildTree(files), [files])
  const hasExplorer = files.length > 1
  const openFile = useCallback(
    async (path: string) => {
      // Add to tabs if not already open
      setOpenTabs((tabs) => (tabs.includes(path) ? tabs : [...tabs, path]))
      setActiveFile(path)
      onActiveFileChange?.(path)

      // Load content if not cached
      if (!fileCache[path] && loadFile) {
        setLoadingFile(path)
        try {
          const loaded = await loadFile(path)
          setFileCache((prev) => ({ ...prev, [path]: loaded }))
        } catch {
          // show fallback
          setFileCache((prev) => ({
            ...prev,
            [path]: { content: `// Failed to load ${path}`, language: languageForPath(path) },
          }))
        } finally {
          setLoadingFile(null)
        }
      }
    },
    [fileCache, loadFile, onActiveFileChange],
  )

  // ── Close a tab ───────────────────────────────────────────────────────
  const closeTab = useCallback(
    (path: string, e?: React.MouseEvent) => {
      e?.stopPropagation()
      setOpenTabs((tabs) => {
        const next = tabs.filter((t) => t !== path)
        if (next.length === 0 && files.length > 0) {
          // Always keep at least one tab open
          const fallback = initialFile ?? files[0].name
          setActiveFile(fallback)
          onActiveFileChange?.(fallback)
          return [fallback]
        }
        if (activeFile === path) {
          const idx = tabs.indexOf(path)
          const newActive = next[Math.min(idx, next.length - 1)]
          setActiveFile(newActive)
          onActiveFileChange?.(newActive)
        }
        return next
      })
    },
    [activeFile, files, initialFile, onActiveFileChange],
  )

  // ── Monaco mount ──────────────────────────────────────────────────────
  const handleMount: OnMount = useCallback((editor, monaco) => {
    editorRef.current = editor
    monacoRef.current = monaco
  }, [])

  // ── Current file data ─────────────────────────────────────────────────
  const currentData = fileCache[activeFile]
  const currentLanguage = currentData?.language ?? language ?? languageForPath(activeFile)
  const currentValue = currentData?.content ?? initialValue ?? ""
  const isLoading = loadingFile === activeFile

  // ── Breadcrumb segments ───────────────────────────────────────────────
  const breadcrumb = activeFile.split("/")

  // ── Toggle dir ────────────────────────────────────────────────────────
  const toggleDir = useCallback((path: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])

  // ── Sync initial file into cache ───────────────────────────────────────
  useEffect(() => {
    if (initialFile && initialValue != null && !fileCache[initialFile]) {
      setFileCache((prev) => ({
        ...prev,
        [initialFile]: {
          content: initialValue,
          language: language ?? languageForPath(initialFile),
        },
      }))
    }
  }, [initialFile, initialValue, language, fileCache])

  // ─── Render tree node ─────────────────────────────────────────────────
  function renderNode(node: TreeNode, depth: number = 0) {
    const indent = depth * 12

    if (node.isDir) {
      const isOpen = !collapsed.has(node.path)
      return (
        <div key={node.path}>
          <button
            onClick={() => toggleDir(node.path)}
            className="flex w-full items-center gap-1 py-0.5 text-left text-xs transition-colors hover:bg-white/5"
            style={{ paddingLeft: indent + 4 }}
          >
            {isOpen ? (
              <ChevronDown className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
            )}
            {isOpen ? (
              <FolderOpen className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
            ) : (
              <Folder className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
            )}
            <span className="truncate text-fg-muted">{node.name}</span>
          </button>
          {isOpen && node.children.map((c) => renderNode(c, depth + 1))}
        </div>
      )
    }

    const isActive = node.path === activeFile
    return (
      <button
        key={node.path}
        onClick={() => openFile(node.path)}
        className={cn(
          "flex w-full items-center gap-1.5 py-0.5 text-left text-xs transition-colors",
          isActive ? "bg-accent/15 text-fg" : "text-fg-muted hover:bg-white/5 hover:text-fg",
        )}
        style={{ paddingLeft: indent + 22 }}
        title={node.path}
      >
        <FileCode className={cn("h-3.5 w-3.5 shrink-0", fileIconColor(node.name))} />
        <span className="truncate">{node.name}</span>
      </button>
    )
  }

  return (
    <div
      className={cn("flex overflow-hidden rounded-md border border-border", className)}
      style={{ height }}
    >
      {/* ── Explorer sidebar (multi-file only) ──────────────────────── */}
      {hasExplorer && (
        <div
          className="flex shrink-0 flex-col overflow-hidden border-r border-white/10"
          style={{
            width: 220,
            backgroundColor: isDark ? "#181818" : "var(--color-bg-subtle, #f8f8f8)",
          }}
        >
          {/* Sidebar header */}
          <div
            className="flex items-center px-3 py-1.5 text-[10px] font-semibold tracking-widest uppercase"
            style={{ color: isDark ? "#888" : "var(--color-fg-muted)" }}
          >
            Explorer
          </div>
          {/* File tree */}
          <div className="flex-1 overflow-x-hidden overflow-y-auto py-0.5">
            {tree.map((n) => renderNode(n, 0))}
          </div>
        </div>
      )}

      {/* ── Editor area ─────────────────────────────────────────────── */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Tab bar */}
        <div
          className="flex overflow-x-auto border-b border-white/10"
          style={{
            backgroundColor: isDark ? "#1e1e1e" : "var(--color-bg-elevated, #fff)",
          }}
        >
          {openTabs.map((tab) => {
            const isActive = tab === activeFile
            return (
              <button
                key={tab}
                onClick={() => openFile(tab)}
                className={cn(
                  "group relative flex shrink-0 items-center gap-1.5 border-r border-white/5 px-3 py-1.5 text-xs transition-colors",
                  isActive ? "text-fg" : "text-fg-muted hover:text-fg",
                )}
                style={{
                  backgroundColor: isActive
                    ? isDark
                      ? "#1e1e1e"
                      : "#fff"
                    : isDark
                      ? "#2d2d2d"
                      : "#f0f0f0",
                }}
              >
                {/* Active tab top highlight */}
                {isActive && <span className="absolute inset-x-0 top-0 h-0.5 bg-accent" />}
                <FileCode className={cn("h-3.5 w-3.5 shrink-0", fileIconColor(tab))} />
                <span className="max-w-32 truncate">{basename(tab)}</span>
                <span
                  onClick={(e) => closeTab(tab, e)}
                  className={cn(
                    "ml-1 flex h-4 w-4 items-center justify-center rounded-sm transition-colors hover:bg-white/10",
                    !isActive && "opacity-0 group-hover:opacity-100",
                  )}
                  role="button"
                  tabIndex={-1}
                  aria-label={`Close ${basename(tab)}`}
                >
                  <X className="h-3 w-3" />
                </span>
              </button>
            )
          })}
        </div>

        {/* Breadcrumb bar */}
        <div
          className="flex items-center gap-1 px-3 py-1 text-xs"
          style={{
            backgroundColor: isDark ? "#1e1e1e" : "#fff",
            color: isDark ? "#888" : "var(--color-fg-muted)",
          }}
        >
          {breadcrumb.map((seg, i) => (
            <span key={i} className="flex items-center gap-1">
              {i > 0 && <ChevronRight className="h-3 w-3" />}
              <span className={cn(i === breadcrumb.length - 1 && "text-fg")}>{seg}</span>
            </span>
          ))}
        </div>

        {/* Editor */}
        <div className="min-h-0 flex-1">
          {isLoading ? (
            <div
              className="flex h-full items-center justify-center"
              style={{ backgroundColor: isDark ? "#1e1e1e" : "#fff" }}
            >
              <div className="h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
            </div>
          ) : (
            <Editor
              height="100%"
              path={activeFile}
              defaultLanguage={currentLanguage}
              defaultValue={currentValue}
              theme={isDark ? "vs-dark" : "light"}
              onChange={(val) => onChange?.(activeFile, val ?? "")}
              onMount={handleMount}
              saveViewState
              options={{
                fontSize: 13,
                readOnly,
                minimap: { enabled: false },
                scrollBeyondLastLine: false,
                wordWrap: "on",
                lineNumbers: "on",
                renderLineHighlight: "line",
                padding: { top: 8, bottom: 8 },
                automaticLayout: true,
                cursorBlinking: "smooth",
                smoothScrolling: true,
                renderWhitespace: "selection",
              }}
            />
          )}
        </div>
      </div>
    </div>
  )
}
