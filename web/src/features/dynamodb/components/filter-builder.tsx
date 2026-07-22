import { useState } from "react"
import { Plus, Trash2, Play } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { createId } from "@/lib/id"

// ─── Types ─────────────────────────────────────────────────────────────────

export type Comparator = "=" | "!=" | "<" | "<=" | ">" | ">=" | "begins_with" | "contains"

export interface FilterCondition {
  id: string
  attribute: string
  comparator: Comparator
  value: string
  /** S, N, or BOOL — determines how the value is serialised for DynamoDB */
  valueType: "S" | "N" | "BOOL"
}

const COMPARATORS: { value: Comparator; label: string }[] = [
  { value: "=", label: "=" },
  { value: "!=", label: "≠" },
  { value: "<", label: "<" },
  { value: "<=", label: "≤" },
  { value: ">", label: ">" },
  { value: ">=", label: "≥" },
  { value: "begins_with", label: "begins with" },
  { value: "contains", label: "contains" },
]

// ─── Component ─────────────────────────────────────────────────────────────

interface Props {
  /** Known attribute names seen in the current item set (for autocomplete) */
  knownAttributes: string[]
  /** Called when the user presses Run to apply the filters */
  onApply: (filters: FilterCondition[]) => void
  /** Called when the user clears all filters */
  onClear: () => void
  /** Whether a query/scan is in progress */
  loading?: boolean
  /** Label for the run button */
  runLabel?: string
}

function emptyCondition(): FilterCondition {
  return { id: createId(), attribute: "", comparator: "=", value: "", valueType: "S" }
}

export function FilterBuilder({
  knownAttributes,
  onApply,
  onClear,
  loading,
  runLabel = "Run",
}: Props) {
  const [conditions, setConditions] = useState<FilterCondition[]>([emptyCondition()])

  function updateCondition(id: string, patch: Partial<FilterCondition>) {
    setConditions((prev) => prev.map((c) => (c.id === id ? { ...c, ...patch } : c)))
  }

  function removeCondition(id: string) {
    setConditions((prev) => {
      const next = prev.filter((c) => c.id !== id)
      return next.length === 0 ? [emptyCondition()] : next
    })
  }

  function addCondition() {
    setConditions((prev) => [...prev, emptyCondition()])
  }

  function handleClear() {
    setConditions([emptyCondition()])
    onClear()
  }

  function handleApply() {
    const valid = conditions.filter((c) => c.attribute.trim() && c.value.trim())
    onApply(valid)
  }

  const hasValidCondition = conditions.some((c) => c.attribute.trim() && c.value.trim())

  return (
    <div className="flex flex-col gap-2">
      {conditions.map((cond, i) => (
        <div key={cond.id} className="flex items-center gap-2">
          {i > 0 && <span className="w-10 text-center text-xs font-medium text-fg-muted">AND</span>}
          {i === 0 && conditions.length > 1 && <span className="w-10" />}

          {/* Attribute name — input with datalist for suggestions */}
          <div className="relative">
            <Input
              value={cond.attribute}
              onChange={(e) => updateCondition(cond.id, { attribute: e.target.value })}
              placeholder="Attribute"
              className="w-40"
              list={`attrs-${cond.id}`}
            />
            <datalist id={`attrs-${cond.id}`}>
              {knownAttributes.map((a) => (
                <option key={a} value={a} />
              ))}
            </datalist>
          </div>

          {/* Comparator */}
          <select
            className="rounded border border-border bg-bg px-2 py-1.5 text-sm text-fg"
            value={cond.comparator}
            onChange={(e) => updateCondition(cond.id, { comparator: e.target.value as Comparator })}
          >
            {COMPARATORS.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>

          {/* Value */}
          <Input
            value={cond.value}
            onChange={(e) => updateCondition(cond.id, { value: e.target.value })}
            placeholder="Value"
            className="w-40"
            onKeyDown={(e) => e.key === "Enter" && handleApply()}
          />

          {/* Value type */}
          <select
            className="rounded border border-border bg-bg px-2 py-1.5 text-xs text-fg-muted"
            value={cond.valueType}
            onChange={(e) =>
              updateCondition(cond.id, { valueType: e.target.value as "S" | "N" | "BOOL" })
            }
          >
            <option value="S">String</option>
            <option value="N">Number</option>
            <option value="BOOL">Boolean</option>
          </select>

          {/* Remove */}
          <Button
            size="sm"
            variant="ghost"
            className="h-7 w-7 shrink-0 p-0 text-fg-muted hover:text-danger"
            onClick={() => removeCondition(cond.id)}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}

      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={addCondition}>
          <Plus className="mr-1 h-3.5 w-3.5" />
          Add filter
        </Button>
        <div className="ml-auto flex gap-2">
          {hasValidCondition && (
            <Button size="sm" variant="ghost" onClick={handleClear}>
              Clear
            </Button>
          )}
          <Button size="sm" onClick={handleApply} disabled={loading}>
            <Play className="mr-1 h-3.5 w-3.5" />
            {runLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}

// ─── Evaluation helper (client-side in-memory filtering for Scan) ──────

/** Evaluates a set of filter conditions against a DynamoDB item (AND logic). */
// eslint-disable-next-line react-refresh/only-export-components
export function matchesFilters(
  item: Record<string, Record<string, unknown>>,
  filters: FilterCondition[],
): boolean {
  if (filters.length === 0) return true
  return filters.every((f) => matchesSingle(item, f))
}

function matchesSingle(
  item: Record<string, Record<string, unknown> | undefined>,
  filter: FilterCondition,
): boolean {
  const attr = item[filter.attribute]
  if (!attr) return false

  // Extract the raw value string from the DynamoDB attribute.
  const raw = extractScalar(attr)
  if (raw === undefined) return false

  const target = filter.value

  switch (filter.comparator) {
    case "=":
      return raw === target
    case "!=":
      return raw !== target
    case "<":
      return compare(raw, target, filter.valueType) < 0
    case "<=":
      return compare(raw, target, filter.valueType) <= 0
    case ">":
      return compare(raw, target, filter.valueType) > 0
    case ">=":
      return compare(raw, target, filter.valueType) >= 0
    case "begins_with":
      return raw.startsWith(target)
    case "contains":
      return raw.includes(target)
    default:
      return false
  }
}

function extractScalar(attr: Record<string, unknown>): string | undefined {
  if ("S" in attr && typeof attr.S === "string") return attr.S
  if ("N" in attr && typeof attr.N === "string") return attr.N
  if ("BOOL" in attr) return String(attr.BOOL)
  if ("NULL" in attr) return "null"
  return undefined
}

function compare(a: string, b: string, type: "S" | "N" | "BOOL"): number {
  if (type === "N") {
    const na = parseFloat(a)
    const nb = parseFloat(b)
    if (!isNaN(na) && !isNaN(nb)) return na - nb
  }
  return a.localeCompare(b)
}
