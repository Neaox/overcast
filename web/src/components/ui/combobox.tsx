/**
 * Combobox — generic searchable dropdown primitive.
 *
 * Uses Radix Popover for the dropdown so it integrates properly with Radix
 * Dialog's layering and click-outside detection. When a combobox lives inside
 * a Dialog, Radix knows the popover is "part of" the dialog and won't dismiss
 * the dialog when the user clicks on a dropdown item.
 *
 * Focus is managed at the container level: the dropdown only closes when focus
 * moves outside the wrapper div entirely, not when moving between input and list.
 */
import React, { useEffect, useId, useRef, useState } from "react"
import * as PopoverPrimitive from "@radix-ui/react-popover"
import { ChevronDown, ChevronsUpDown, Loader2, type LucideIcon } from "lucide-react"
import { cn } from "@/lib/utils"

// ─── Types ────────────────────────────────────────────────────────────────────

export interface ComboboxRenderContext {
  selected: boolean
  active: boolean
  disabled: boolean
  disabledReason?: string
}

interface ComboboxBaseProps<T> {
  items: T[]
  filterFn: (item: T, query: string) => boolean
  getItemValue: (item: T) => string
  renderItem: (item: T, ctx: ComboboxRenderContext) => React.ReactNode
  renderCustomFooter?: (query: string, select: (v: string) => void) => React.ReactNode
  /** Render a group separator above an item. Receives the current and previous filtered item. */
  renderSeparator?: (item: T, prev: T | null) => React.ReactNode
  isItemDisabled?: (item: T) => string | undefined
  allowCustom?: boolean
  /** When true, re-opening the dropdown seeds the query from the current value
   *  instead of clearing it, so the user can edit after selecting a suggestion. */
  allowFreeText?: boolean
  /** Shown when the filtered list is empty. Defaults to 'No results for "<query>"'. */
  emptyMessage?: string
  /** When true, renders a disabled input with a spinner instead of the full combobox. */
  isLoading?: boolean
  id?: string
  /**
   * What the field is for. A combobox is an <input> with no visible caption in most of
   * the console, so without this it is announced as an unnamed edit field — and a
   * placeholder is not a name: it is gone the moment anything is typed. Pass
   * `aria-labelledby` instead when a visible label already names the field.
   */
  "aria-label"?: string
  "aria-labelledby"?: string
  placeholder?: string
  className?: string
  inputClassName?: string
  popoverWidth?: string
}

export interface ComboboxPropsSingle<T> extends ComboboxBaseProps<T> {
  multiple?: false
  value: string
  onChange: (value: string) => void
}

export interface ComboboxPropsMultiple<T> extends ComboboxBaseProps<T> {
  multiple: true
  value: string[]
  onChange: (values: string[]) => void
}

export type ComboboxProps<T> = ComboboxPropsSingle<T> | ComboboxPropsMultiple<T>

// ─── Hook ─────────────────────────────────────────────────────────────────────

function useCombobox<T>(
  _value: string,
  onChange: (v: string) => void,
  items: T[],
  filterFn: (item: T, query: string) => boolean,
  getItemValue: (item: T) => string,
  allowCustom: boolean,
  allowFreeText: boolean,
) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [activeIdx, setActiveIdx] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLUListElement>(null)

  const filtered = query ? items.filter((item) => filterFn(item, query)) : items

  useEffect(() => {
    const el = listRef.current?.children[activeIdx] as HTMLElement | undefined
    el?.scrollIntoView({ block: "nearest" })
  }, [activeIdx])

  function updateQuery(q: string) {
    setQuery(q)
    setActiveIdx(0)
  }

  function handleOpen() {
    setQuery(allowFreeText ? _value : "")
    setActiveIdx(0)
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
    if (e.currentTarget.contains(e.relatedTarget)) return
    if (allowFreeText && query && query !== _value) onChange(query)
    setOpen(false)
    setQuery("")
  }

  return {
    open,
    query,
    setQuery: updateQuery,
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

function useMultiCombobox<T>(
  values: string[],
  onChange: (values: string[]) => void,
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

  useEffect(() => {
    const el = listRef.current?.children[activeIdx] as HTMLElement | undefined
    el?.scrollIntoView({ block: "nearest" })
  }, [activeIdx])

  function updateQuery(q: string) {
    setQuery(q)
    setActiveIdx(0)
  }

  function toggle(v: string) {
    if (values.includes(v)) {
      onChange(values.filter((x) => x !== v))
    } else {
      onChange([...values, v])
    }
    setQuery("")
    // keep dropdown open after selection
    setOpen(true)
  }

  function remove(v: string) {
    onChange(values.filter((x) => x !== v))
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!open) {
      if (e.key === "Enter" || e.key === "ArrowDown") {
        e.preventDefault()
        setQuery("")
        setOpen(true)
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
        toggle(getItemValue(filtered[activeIdx]))
      } else if (allowCustom && query) {
        if (!values.includes(query)) onChange([...values, query])
        setQuery("")
        inputRef.current?.focus()
      }
    } else if (e.key === "Backspace" && !query && values.length > 0) {
      onChange(values.slice(0, -1))
    } else if (e.key === "Escape") {
      setOpen(false)
      setQuery("")
    }
  }

  function handleContainerBlur(e: React.FocusEvent) {
    if (e.currentTarget.contains(e.relatedTarget)) return
    setOpen(false)
    setQuery("")
  }

  return {
    open,
    setOpen,
    query,
    setQuery: updateQuery,
    filtered,
    activeIdx,
    inputRef,
    listRef,
    toggle,
    remove,
    handleKeyDown,
    handleContainerBlur,
  }
}

// ─── Shared dropdown list ─────────────────────────────────────────────────────

function DropdownList<T>({
  listId,
  optionId,
  filtered,
  activeIdx,
  isSelected,
  listRef,
  getItemValue,
  renderItem,
  renderCustomFooter,
  renderSeparator,
  isItemDisabled,
  emptyMessage,
  query,
  select,
  items,
  popoverWidth,
  allowFreeText,
}: {
  listId: string
  optionId: (index: number) => string
  filtered: T[]
  activeIdx: number
  isSelected: (item: T) => boolean
  listRef: React.RefObject<HTMLUListElement | null>
  getItemValue: (item: T) => string
  renderItem: (item: T, ctx: ComboboxRenderContext) => React.ReactNode
  renderCustomFooter?: (query: string, select: (v: string) => void) => React.ReactNode
  renderSeparator?: (item: T, prev: T | null) => React.ReactNode
  isItemDisabled?: (item: T) => string | undefined
  emptyMessage?: string
  query: string
  select: (v: string) => void
  items: T[]
  popoverWidth: string
  allowFreeText: boolean
}) {
  const showCustomFooter =
    !!renderCustomFooter && !!query && !items.find((item) => getItemValue(item) === query)

  return (
    <PopoverPrimitive.Content
      side="bottom"
      align="start"
      sideOffset={4}
      onOpenAutoFocus={(e) => e.preventDefault()}
      onCloseAutoFocus={(e) => e.preventDefault()}
      onWheel={(e) => e.stopPropagation()}
      onTouchMove={(e) => e.stopPropagation()}
      className={cn(
        "rounded-md border border-border bg-bg-elevated shadow-lg",
        popoverWidth === "w-full" ? "w-(--radix-popover-trigger-width)" : popoverWidth,
      )}
      style={{ zIndex: 9999 }}
    >
      {filtered.length === 0 && !showCustomFooter ? (
        allowFreeText ? null : (
          <p className="px-3 py-6 text-center text-sm text-fg-muted">
            {emptyMessage ?? `No results for "${query}"`}
          </p>
        )
      ) : (
        <ul id={listId} ref={listRef} role="listbox" className="max-h-64 overflow-y-auto py-1">
          {filtered.map((item, i) => {
            const disabledReason = isItemDisabled?.(item)
            const isDisabled = !!disabledReason
            const separator = renderSeparator?.(item, filtered[i - 1] ?? null)
            return (
              <React.Fragment key={getItemValue(item)}>
                {separator}
                <li
                  id={optionId(i)}
                  role="option"
                  aria-selected={isSelected(item)}
                  aria-disabled={isDisabled}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => !isDisabled && select(getItemValue(item))}
                  className={cn(
                    "px-3 py-1.5 text-sm",
                    isDisabled
                      ? "cursor-not-allowed opacity-40"
                      : i === activeIdx
                        ? // Ink, not white: the dark theme's accent is a pale blue.
                          "cursor-pointer bg-accent text-fg-on-accent *:text-fg-on-accent **:text-fg-on-accent!"
                        : "cursor-pointer hover:bg-accent-muted",
                  )}
                >
                  {renderItem(item, {
                    selected: isSelected(item),
                    active: i === activeIdx && !isDisabled,
                    disabled: isDisabled,
                    disabledReason,
                  })}
                </li>
              </React.Fragment>
            )
          })}
        </ul>
      )}
      {showCustomFooter && renderCustomFooter(query, select)}
    </PopoverPrimitive.Content>
  )
}

// ─── Full-size variant (single-select) ───────────────────────────────────────

function SingleComboboxImpl<T>({
  value,
  onChange,
  items,
  filterFn,
  getItemValue,
  renderItem,
  renderCustomFooter,
  renderSeparator,
  isItemDisabled,
  allowCustom = false,
  allowFreeText = false,
  emptyMessage,
  isLoading = false,
  id,
  placeholder,
  className,
  inputClassName,
  popoverWidth = "w-full",
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
}: ComboboxPropsSingle<T>) {
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
  } = useCombobox(value, onChange, items, filterFn, getItemValue, allowCustom, allowFreeText)

  const listId = `${useId()}-listbox`
  const optionId = (index: number) => `${listId}-option-${index}`
  const containerRef = useRef<HTMLDivElement>(null)

  if (isLoading) {
    return (
      <div className={cn("relative", className)}>
        <input
          id={id}
          disabled
          placeholder="Loading…"
          className={cn(
            "flex h-8 w-full rounded-md border border-border bg-bg py-1 pr-8 pl-3",
            "cursor-not-allowed text-sm text-fg opacity-60 placeholder:text-fg-subtle",
            inputClassName,
          )}
        />
        <Loader2 className="pointer-events-none absolute top-1/2 right-2 h-3.5 w-3.5 -translate-y-1/2 animate-spin text-fg-subtle" />
      </div>
    )
  }

  return (
    <PopoverPrimitive.Root open={open} modal={false}>
      <PopoverPrimitive.Anchor asChild>
        <div ref={containerRef} className={cn("relative", className)} onBlur={handleContainerBlur}>
          <input
            ref={inputRef}
            id={id}
            role="combobox"
            aria-expanded={open}
            aria-autocomplete="list"
            aria-label={ariaLabel}
            aria-labelledby={ariaLabelledBy}
            aria-controls={open ? listId : undefined}
            aria-activedescendant={open && activeIdx >= 0 ? optionId(activeIdx) : undefined}
            value={open ? query : value}
            placeholder={open ? value : (placeholder ?? "")}
            spellCheck={false}
            onFocus={handleOpen}
            onClick={handleOpen}
            onChange={(e) => {
              setQuery(e.target.value)
              if (allowFreeText) onChange(e.target.value)
            }}
            onKeyDown={handleKeyDown}
            className={cn(
              "flex h-8 w-full rounded-md border border-border bg-bg py-1 pr-8 pl-3",
              "text-sm text-fg placeholder:text-fg-subtle",
              "hover:border-accent",
              "focus-visible:border-accent focus-visible:ring-1 focus-visible:ring-accent focus-visible:outline-none focus-visible:ring-inset",
              inputClassName,
            )}
          />
          <ChevronsUpDown className="pointer-events-none absolute top-1/2 right-2 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
        </div>
      </PopoverPrimitive.Anchor>
      {open && (
        <PopoverPrimitive.Portal>
          <DropdownList
            listId={listId}
            optionId={optionId}
            filtered={filtered}
            activeIdx={activeIdx}
            isSelected={(item) => getItemValue(item) === value}
            listRef={listRef}
            getItemValue={getItemValue}
            renderItem={renderItem}
            renderCustomFooter={renderCustomFooter}
            renderSeparator={renderSeparator}
            isItemDisabled={isItemDisabled}
            emptyMessage={emptyMessage}
            query={query}
            select={select}
            items={items}
            popoverWidth={popoverWidth}
            allowFreeText={allowFreeText}
          />
        </PopoverPrimitive.Portal>
      )}
    </PopoverPrimitive.Root>
  )
}

// ─── Full-size variant (multi-select / tag input) ─────────────────────────────

function MultiComboboxImpl<T>({
  value: values,
  onChange,
  items,
  filterFn,
  getItemValue,
  renderItem,
  renderCustomFooter,
  isItemDisabled,
  allowCustom = false,
  emptyMessage,
  isLoading = false,
  placeholder,
  className,
  popoverWidth = "w-full",
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
}: ComboboxPropsMultiple<T>) {
  const {
    open,
    setOpen,
    query,
    setQuery,
    filtered,
    activeIdx,
    inputRef,
    listRef,
    toggle,
    remove,
    handleKeyDown,
    handleContainerBlur,
  } = useMultiCombobox(values, onChange, items, filterFn, getItemValue, allowCustom)

  const listId = `${useId()}-listbox`
  const optionId = (index: number) => `${listId}-option-${index}`

  const containerRef = useRef<HTMLDivElement>(null)

  if (isLoading) {
    return (
      <div
        className={cn(
          "relative flex min-h-8 w-full items-center rounded-md border border-border bg-bg px-3 py-1 opacity-60",
          className,
        )}
      >
        <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin text-fg-subtle" />
        <span className="text-sm text-fg-subtle">Loading…</span>
      </div>
    )
  }

  return (
    <PopoverPrimitive.Root open={open} modal={false}>
      <PopoverPrimitive.Anchor asChild>
        <div ref={containerRef} className={cn("relative", className)} onBlur={handleContainerBlur}>
          <div
            className="flex min-h-8 w-full cursor-text flex-wrap gap-1 rounded-md border border-border bg-bg px-2 py-1 transition-colors focus-within:border-accent focus-within:ring-1 focus-within:ring-accent focus-within:ring-inset hover:border-accent"
            onClick={() => inputRef.current?.focus()}
          >
            {values.map((v) => (
              <span
                key={v}
                className="flex items-center gap-1 rounded bg-accent-muted px-2 py-0.5 text-xs font-medium text-fg"
              >
                {v}
                <button
                  type="button"
                  aria-label={`Remove ${v}`}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => remove(v)}
                  className="ml-0.5 text-fg-muted hover:text-fg"
                >
                  ×
                </button>
              </span>
            ))}
            <input
              ref={inputRef}
              role="combobox"
              aria-expanded={open}
              aria-autocomplete="list"
              aria-label={ariaLabel}
              aria-labelledby={ariaLabelledBy}
              aria-controls={open ? listId : undefined}
              aria-activedescendant={open && activeIdx >= 0 ? optionId(activeIdx) : undefined}
              value={query}
              placeholder={values.length === 0 ? (placeholder ?? "") : ""}
              spellCheck={false}
              onFocus={() => {
                setQuery("")
                setOpen(true)
              }}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={handleKeyDown}
              className="min-w-20 flex-1 bg-transparent py-0.5 text-sm text-fg outline-none placeholder:text-fg-subtle"
            />
          </div>
        </div>
      </PopoverPrimitive.Anchor>
      {open && (
        <PopoverPrimitive.Portal>
          <DropdownList
            listId={listId}
            optionId={optionId}
            filtered={filtered}
            activeIdx={activeIdx}
            isSelected={(item) => values.includes(getItemValue(item))}
            listRef={listRef}
            getItemValue={getItemValue}
            renderItem={renderItem}
            renderCustomFooter={renderCustomFooter}
            isItemDisabled={isItemDisabled}
            emptyMessage={emptyMessage}
            query={query}
            select={toggle}
            items={items}
            popoverWidth={popoverWidth}
            allowFreeText={false}
          />
        </PopoverPrimitive.Portal>
      )}
    </PopoverPrimitive.Root>
  )
}

// ─── Public Combobox — dispatches to single or multi impl ─────────────────────

export function Combobox<T>(props: ComboboxProps<T>) {
  if (props.multiple) {
    return <MultiComboboxImpl<T> {...props} />
  }
  return <SingleComboboxImpl<T> {...props} />
}

// ─── Compact split-control variant (e.g. header toolbar) ─────────────────────

/**
 * The topbar's control shape: a 32px bordered rounded-rect split into a
 * value cell (optional leading icon + mono text) and a 26px chevron cell.
 * Hover and focus move the *border* to accent — the fill never changes.
 *
 * It is still a combobox: the value cell is a real, typeable input driving the
 * same filter/keyboard machinery as the full-size variant. Everything outside
 * the input — the icon, the padding, the chevron cell — swallows its own
 * mousedown so the click lands as "focus the input and open", never as a blur.
 */
export function ComboboxCompact<T>({
  value,
  onChange,
  items,
  filterFn,
  getItemValue,
  renderItem,
  renderCustomFooter,
  allowCustom = false,
  allowFreeText = false,
  leadingIcon: LeadingIcon,
  inputClassName,
  popoverWidth = "w-64",
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
}: Omit<ComboboxPropsSingle<T>, "id" | "placeholder" | "className"> & {
  /** Rendered muted at the head of the value cell. */
  leadingIcon?: LucideIcon
}) {
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
  } = useCombobox(value, onChange, items, filterFn, getItemValue, allowCustom, allowFreeText)

  const listId = `${useId()}-listbox`
  const optionId = (index: number) => `${listId}-option-${index}`
  const containerRef = useRef<HTMLDivElement>(null)

  return (
    <PopoverPrimitive.Root open={open} modal={false}>
      <PopoverPrimitive.Anchor asChild>
        <div
          ref={containerRef}
          onBlur={handleContainerBlur}
          onMouseDown={(e) => {
            if (e.target === inputRef.current) return
            e.preventDefault()
            inputRef.current?.focus()
            if (!open) handleOpen()
          }}
          className={cn(
            "group flex h-8 w-44 cursor-pointer items-center rounded-control border border-border bg-bg",
            "transition-colors hover:border-accent",
            "focus-within:border-accent focus-within:ring-1 focus-within:ring-accent focus-within:ring-inset",
          )}
        >
          {LeadingIcon && (
            <LeadingIcon aria-hidden className="ml-2 h-3.5 w-3.5 shrink-0 text-fg-subtle" />
          )}
          <input
            ref={inputRef}
            role="combobox"
            aria-expanded={open}
            aria-autocomplete="list"
            aria-label={ariaLabel}
            aria-labelledby={ariaLabelledBy}
            aria-controls={open ? listId : undefined}
            aria-activedescendant={open && activeIdx >= 0 ? optionId(activeIdx) : undefined}
            value={open ? query : value}
            placeholder={open ? value : ""}
            spellCheck={false}
            onFocus={handleOpen}
            onClick={handleOpen}
            onChange={(e) => {
              setQuery(e.target.value)
              if (allowFreeText) onChange(e.target.value)
            }}
            onKeyDown={handleKeyDown}
            className={cn(
              "min-w-0 flex-1 cursor-pointer bg-transparent px-2 font-mono text-xs",
              "text-fg-muted transition-colors outline-none placeholder:text-fg-subtle",
              "group-focus-within:text-fg group-hover:text-fg",
              inputClassName,
            )}
          />
          <span
            aria-hidden
            className="flex h-full w-[26px] shrink-0 items-center justify-center border-l border-border text-fg-subtle"
          >
            <ChevronDown className="h-3 w-3" />
          </span>
        </div>
      </PopoverPrimitive.Anchor>
      {open && (
        <PopoverPrimitive.Portal>
          <DropdownList
            listId={listId}
            optionId={optionId}
            filtered={filtered}
            activeIdx={activeIdx}
            isSelected={(item) => getItemValue(item) === value}
            listRef={listRef}
            getItemValue={getItemValue}
            renderItem={renderItem}
            renderCustomFooter={renderCustomFooter}
            query={query}
            select={select}
            items={items}
            popoverWidth={popoverWidth}
            allowFreeText={allowFreeText}
          />
        </PopoverPrimitive.Portal>
      )}
    </PopoverPrimitive.Root>
  )
}
