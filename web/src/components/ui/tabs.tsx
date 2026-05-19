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

export function TabList({ children, className }: TabListProps) {
  return (
    <div role="tablist" className={cn("flex gap-6 border-b border-border", className)}>
      {children}
    </div>
  )
}

// ─── Tab ──────────────────────────────────────────────────────────────────

const tabBase =
  "inline-flex items-center border-b-2 px-1 pb-2 text-sm font-medium transition-colors cursor-pointer"
const activeCls = "border-accent text-accent"
const inactiveCls = "border-transparent text-fg-muted hover:text-fg"

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
      className={cn(
        tabBase,
        isSelected ? activeCls : inactiveCls,
        isDisabled && "pointer-events-none opacity-50",
        className,
      )}
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
