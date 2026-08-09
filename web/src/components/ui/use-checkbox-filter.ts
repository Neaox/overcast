import { useCallback, useMemo } from "react"
import type { CheckboxFilterItem } from "./checkbox-filter-dropdown"

/**
 * Props a `<CheckboxFilterDropdown>` needs — the whole set, so a caller can
 * spread the hook's return value straight into the component.
 */
export interface CheckboxFilterProps {
  items: CheckboxFilterItem[]
  model: "hide" | "show"
  selected: Set<string>
  onToggle: (id: string) => void
  onShowAll: () => void
  onHideAll: () => void
  triggerLabel: string
}

export interface UseCheckboxFilterOptions {
  items: CheckboxFilterItem[]
  /** See `CheckboxFilterDropdown`: "hide" is a deny-list, "show" an include-list. */
  model: "hide" | "show"
  /** Current selection. Hide-model: the hidden ids. Show-model: the included ids. */
  value: readonly string[]
  /** Receives the next selection, always sorted (see `toggleFilterValue`). */
  onChange: (next: string[]) => void
  /** Plural noun for the trigger label, e.g. "services", "statuses", "methods". */
  noun: string
}

/**
 * Add or remove `id`, returning a **sorted** array.
 *
 * Sorting is not cosmetic: these arrays end up in a TanStack Query key and in
 * the URL, and both must be order-independent so that ticking A then B is the
 * same cache entry (and the same link) as ticking B then A.
 */
export function toggleFilterValue(value: readonly string[], id: string): string[] {
  return value.includes(id)
    ? value.filter((v) => v !== id)
    : [...value, id].sort()
}

/**
 * The trigger label conventions shared by every filter dropdown on a page.
 *
 * Both models read the same way to a user — the label describes what the table
 * is showing, not which checkboxes are ticked — so an untouched hide-model
 * filter and an empty show-model filter both say "all <noun>".
 */
export function checkboxFilterLabel(
  model: "hide" | "show",
  itemCount: number,
  selectedCount: number,
  noun: string,
): string {
  if (model === "hide") {
    if (selectedCount === 0) return `all ${noun}`
    const visible = Math.max(0, itemCount - selectedCount)
    if (itemCount > 0 && visible === 0) return `no ${noun}`
    return `${visible} selected`
  }
  // Show-model: nothing ticked means nothing is filtered out, not "no rows".
  if (selectedCount === 0) return `all ${noun}`
  return `${selectedCount} selected`
}

/**
 * Turns a selection array plus a setter into everything a
 * `<CheckboxFilterDropdown>` needs.
 *
 * The traces page renders three of these dropdowns (services, statuses,
 * methods) over two different models; without this hook each one would carry
 * its own copy of the toggle / select-all / clear-all / label block.
 *
 * The selection is kept as a sorted array rather than a `Set` because it is
 * owned by URL search params — a `Set` is not serialisable and its iteration
 * order would leak into the query key.
 */
export function useCheckboxFilter({
  items,
  model,
  value,
  onChange,
  noun,
}: UseCheckboxFilterOptions): CheckboxFilterProps {
  const selected = useMemo(() => new Set(value), [value])

  const onToggle = useCallback(
    (id: string) => onChange(toggleFilterValue(value, id)),
    [onChange, value],
  )

  const allIds = useCallback(() => items.map((i) => i.id).sort(), [items])

  // "Show all" makes every item visible; "hide all" the reverse. Which of the
  // two clears the selection depends on the model.
  const onShowAll = useCallback(
    () => onChange(model === "hide" ? [] : allIds()),
    [model, onChange, allIds],
  )
  const onHideAll = useCallback(
    () => onChange(model === "hide" ? allIds() : []),
    [model, onChange, allIds],
  )

  // A hide-model selection can name something the page has not seen yet — a
  // service from a shared URL whose traces have not loaded — and that hides
  // nothing, so it must not be counted against the visible items. A show-model
  // selection is a filter in its own right (the URL may carry an exact status
  // code that is not one of the offered classes), so every entry counts.
  const selectedCount = model === "hide"
    ? items.reduce((n, item) => (selected.has(item.id) ? n + 1 : n), 0)
    : value.length

  const triggerLabel = checkboxFilterLabel(model, items.length, selectedCount, noun)

  return { items, model, selected, onToggle, onShowAll, onHideAll, triggerLabel }
}
