import { createContext, useContext, type ReactNode } from "react"
import { cn } from "@/lib/utils"

// ─── Context ──────────────────────────────────────────────────────────────

interface TabsContext {
  selectedKey: string
  onSelectionChange: (key: string) => void
}

const TabsCtx = createContext<TabsContext | null>(null)

function useTabsContext() {
  const ctx = useContext(TabsCtx)
  if (!ctx) throw new Error("Tab components must be used within <Tabs>")
  return ctx
}

// ─── Tabs (root) ──────────────────────────────────────────────────────────

interface TabsProps {
  selectedKey: string
  onSelectionChange: (key: string) => void
  children: ReactNode
  className?: string
}

export function Tabs({ selectedKey, onSelectionChange, children, className }: TabsProps) {
  return (
    <TabsCtx.Provider value={{ selectedKey, onSelectionChange }}>
      <div className={className}>{children}</div>
    </TabsCtx.Provider>
  )
}

// ─── TabList ──────────────────────────────────────────────────────────────

interface TabListProps {
  children: ReactNode
  className?: string
}

/**
 * Only the selected tab is in the tab order (see `Tab`), so the arrow keys are
 * the only way to reach the others — without them the roving `tabIndex` would
 * make every unselected tab unreachable by keyboard.
 */
function moveFocus(list: HTMLElement, from: EventTarget | null, delta: number | "first" | "last") {
  const tabs = [...list.querySelectorAll<HTMLButtonElement>('[role="tab"]:not(:disabled)')]
  if (tabs.length === 0) return
  const current = tabs.indexOf(from as HTMLButtonElement)
  const next =
    delta === "first"
      ? 0
      : delta === "last"
        ? tabs.length - 1
        : (current + delta + tabs.length) % tabs.length
  tabs[next].focus()
  tabs[next].click()
}

const ARROW_KEYS = new Map<string, number | "first" | "last">([
  ["ArrowRight", 1],
  ["ArrowLeft", -1],
  ["Home", "first"],
  ["End", "last"],
])

export function TabList({ children, className }: TabListProps) {
  return (
    <div
      role="tablist"
      className={cn("flex gap-6 border-b border-border", className)}
      onKeyDown={(event) => {
        const delta = ARROW_KEYS.get(event.key)
        if (delta === undefined) return
        event.preventDefault()
        moveFocus(event.currentTarget, event.target, delta)
      }}
    >
      {children}
    </div>
  )
}

// ─── Tab ──────────────────────────────────────────────────────────────────

const tabBase = cn(
  "inline-flex items-center border-b-2 px-1 pb-2 font-mono text-sm font-medium transition-colors",
  "cursor-pointer disabled:cursor-not-allowed disabled:opacity-50",
)
const activeCls = "border-accent text-accent"
const inactiveCls = "border-transparent text-fg-muted not-disabled:hover:text-fg"

interface TabProps {
  id: string
  children: ReactNode
  isDisabled?: boolean
  className?: string
}

export function Tab({ id, children, isDisabled, className }: TabProps) {
  const { selectedKey, onSelectionChange } = useTabsContext()
  const isSelected = selectedKey === id
  return (
    <button
      role="tab"
      aria-selected={isSelected}
      aria-disabled={isDisabled || undefined}
      tabIndex={isSelected ? 0 : -1}
      disabled={isDisabled}
      className={cn(tabBase, isSelected ? activeCls : inactiveCls, className)}
      onClick={() => onSelectionChange(id)}
    >
      {children}
    </button>
  )
}

// ─── TabPanel ─────────────────────────────────────────────────────────────

interface TabPanelProps {
  id: string
  children: ReactNode
  className?: string
}

export function TabPanel({ id, children, className }: TabPanelProps) {
  const { selectedKey } = useTabsContext()
  if (selectedKey !== id) return null
  return (
    <div role="tabpanel" className={className}>
      {children}
    </div>
  )
}
