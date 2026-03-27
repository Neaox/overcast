/**
 * Combobox — generic searchable dropdown primitive.
 *
 * Uses a plain positioned <div> for the dropdown — no Radix Popover — to avoid
 * the timing conflict where Radix treats the triggering pointerdown as "outside"
 * the popover (since it happened before the popover mounted) and immediately
 * fires onOpenChange(false), causing a flash-and-disappear.
 *
 * Focus is managed at the container level: the dropdown only closes when focus
 * moves outside the wrapper div entirely, not when moving between input and list.
 */
import { useEffect, useRef, useState } from "react"
import { ChevronsUpDown } from "lucide-react"
import { cn } from "@/lib/utils"

// ─── Types ────────────────────────────────────────────────────────────────────

export interface ComboboxRenderContext {
  selected: boolean
  active: boolean
}

export interface ComboboxProps<T> {
  value: string
  onChange: (value: string) => void
  items: T[]
  filterFn: (item: T, query: string) => boolean
  getItemValue: (item: T) => string
  renderItem: (item: T, ctx: ComboboxRenderContext) => React.ReactNode
  renderCustomFooter?: (query: string, select: (v: string) => void) => React.ReactNode
  allowCustom?: boolean
  id?: string
  placeholder?: string
  className?: string
  inputClassName?: string
  popoverWidth?: string
}

// ─── Hook ─────────────────────────────────────────────────────────────────────

function useCombobox<T>(
  value: string,
  onChange: (v: string) => void,
  items: T[],
  filterFn: (item: T, query: string) => boolean,
  getItemValue: (item: T) => string,
  allowCustom: boolean,
) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [activeIdx, setActiveIdx] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLUListElement>(null)

  const filtered = query ? items.filter((item) => filterFn(item, query)) : items

  useEffect(() => setActiveIdx(0), [query])

  useEffect(() => {
    const el = listRef.current?.children[activeIdx] as HTMLElement | undefined
    el?.scrollIntoView({ block: "nearest" })
  }, [activeIdx])

  function handleOpen() {
    setQuery("")
    setOpen(true)
  }

  function select(v: string) {
    onChange(v)
    setOpen(false)
    setQuery("")
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!open) {
      if (e.key === "Enter" || e.key === "ArrowDown") {
        e.preventDefault()
        handleOpen()
      }
      return
    }
    if (e.key === "ArrowDown") {
      e.preventDefault()
      setActiveIdx((i) => Math.min(i + 1, filtered.length - 1))
    } else if (e.key === "ArrowUp") {
      e.preventDefault()
      setActiveIdx((i) => Math.max(i - 1, 0))
    } else if (e.key === "Enter") {
      e.preventDefault()
      if (filtered[activeIdx]) {
        select(getItemValue(filtered[activeIdx]))
      } else if (allowCustom && query) {
        select(query)
      }
    } else if (e.key === "Escape") {
      setOpen(false)
      setQuery("")
    }
  }

  // Close only when focus leaves the entire container (input + dropdown together).
  function handleContainerBlur(e: React.FocusEvent) {
    if (e.currentTarget.contains(e.relatedTarget as Node | null)) return
    setOpen(false)
    setQuery("")
  }

  return {
    open,
    query,
    setQuery,
    filtered,
    activeIdx,
    inputRef,
    listRef,
    handleOpen,
    select,
    handleKeyDown,
    handleContainerBlur,
  }
}

// ─── Shared dropdown list ─────────────────────────────────────────────────────

function DropdownList<T>({
  filtered,
  activeIdx,
  value,
  listRef,
  getItemValue,
  renderItem,
  renderCustomFooter,
  query,
  select,
  items,
  popoverWidth,
}: {
  filtered: T[]
  activeIdx: number
  value: string
  listRef: React.RefObject<HTMLUListElement | null>
  getItemValue: (item: T) => string
  renderItem: (item: T, ctx: ComboboxRenderContext) => React.ReactNode
  renderCustomFooter?: (query: string, select: (v: string) => void) => React.ReactNode
  query: string
  select: (v: string) => void
  items: T[]
  popoverWidth: string
}) {
  const showCustomFooter =
    !!renderCustomFooter && !!query && !items.find((item) => getItemValue(item) === query)

  return (
    <div
      className={cn(
        "absolute top-full left-0 z-50 mt-1 rounded-md border border-border bg-bg-elevated shadow-lg",
        popoverWidth,
      )}
    >
      {filtered.length === 0 && !showCustomFooter ? (
        <p className="px-3 py-6 text-center text-sm text-fg-muted">No results for "{query}"</p>
      ) : (
        <ul ref={listRef} role="listbox" className="max-h-64 overflow-y-auto py-1">
          {filtered.map((item, i) => (
            <li
              key={getItemValue(item)}
              role="option"
              aria-selected={getItemValue(item) === value}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => select(getItemValue(item))}
              className={cn(
                "cursor-pointer px-3 py-1.5 text-sm",
                i === activeIdx ? "bg-accent text-white" : "hover:bg-bg-muted",
              )}
            >
              {renderItem(item, { selected: getItemValue(item) === value, active: i === activeIdx })}
            </li>
          ))}
        </ul>
      )}
      {showCustomFooter && renderCustomFooter!(query, select)}
    </div>
  )
}

// ─── Full-size variant ────────────────────────────────────────────────────────

export function Combobox<T>({
  value,
  onChange,
  items,
  filterFn,
  getItemValue,
  renderItem,
  renderCustomFooter,
  allowCustom = false,
  id,
  placeholder,
  className,
  inputClassName,
  popoverWidth = "w-full",
}: ComboboxProps<T>) {
  const {
    open,
    query,
    setQuery,
    filtered,
    activeIdx,
    inputRef,
    listRef,
    handleOpen,
    select,
    handleKeyDown,
    handleContainerBlur,
  } = useCombobox(value, onChange, items, filterFn, getItemValue, allowCustom)

  return (
    <div className={cn("relative", className)} onBlur={handleContainerBlur}>
      <input
        ref={inputRef}
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-autocomplete="list"
        value={open ? query : value}
        placeholder={open ? value : (placeholder ?? "")}
        spellCheck={false}
        onFocus={handleOpen}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={handleKeyDown}
        className={cn(
          "flex h-8 w-full rounded-md border border-border bg-bg py-1 pr-8 pl-3",
          "text-sm text-fg placeholder:text-fg-subtle",
          "focus:ring-1 focus:ring-accent focus:outline-none",
          inputClassName,
        )}
      />
      <ChevronsUpDown className="pointer-events-none absolute top-1/2 right-2 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
      {open && (
        <DropdownList
          filtered={filtered}
          activeIdx={activeIdx}
          value={value}
          listRef={listRef}
          getItemValue={getItemValue}
          renderItem={renderItem}
          renderCustomFooter={renderCustomFooter}
          query={query}
          select={select}
          items={items}
          popoverWidth={popoverWidth}
        />
      )}
    </div>
  )
}

// ─── Compact pill-shaped variant (e.g. header toolbar) ───────────────────────

export function ComboboxCompact<T>({
  value,
  onChange,
  items,
  filterFn,
  getItemValue,
  renderItem,
  renderCustomFooter,
  allowCustom = false,
  inputClassName,
  popoverWidth = "w-64",
}: Omit<ComboboxProps<T>, "id" | "placeholder" | "className">) {
  const {
    open,
    query,
    setQuery,
    filtered,
    activeIdx,
    inputRef,
    listRef,
    handleOpen,
    select,
    handleKeyDown,
    handleContainerBlur,
  } = useCombobox(value, onChange, items, filterFn, getItemValue, allowCustom)

  return (
    <div className="relative" onBlur={handleContainerBlur}>
      <input
        ref={inputRef}
        role="combobox"
        aria-expanded={open}
        aria-autocomplete="list"
        value={open ? query : value}
        placeholder={open ? value : ""}
        spellCheck={false}
        onFocus={handleOpen}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={handleKeyDown}
        className={cn(
          "w-36 rounded-full border border-border bg-bg-muted py-0.5 pr-5 pl-2",
          "cursor-pointer font-mono text-xs text-fg-muted",
          "focus:text-fg focus:ring-1 focus:ring-accent focus:outline-none",
          inputClassName,
        )}
      />
      <ChevronsUpDown className="pointer-events-none absolute top-1/2 right-1.5 h-3 w-3 -translate-y-1/2 text-fg-subtle" />
      {open && (
        <DropdownList
          filtered={filtered}
          activeIdx={activeIdx}
          value={value}
          listRef={listRef}
          getItemValue={getItemValue}
          renderItem={renderItem}
          renderCustomFooter={renderCustomFooter}
          query={query}
          select={select}
          items={items}
          popoverWidth={popoverWidth}
        />
      )}
    </div>
  )
}
