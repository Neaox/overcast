import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

/**
 * The label/value grid every detail page opens with — the ARN, the created
 * date, the status, the engine version.
 *
 * The typography is the whole point of the component. A definition list is the
 * same thing a table is — a label naming a field, a value holding machine
 * output — so it takes the same two specs `components/ui/table.tsx` sets:
 *
 * - the **label** is the field-label spec (`@/lib/typography`), so a detail-page
 *   label and a column header read as the same kind of thing;
 * - the **value** is mono by default, because ARNs, ids, timestamps, sizes and
 *   statuses are all machine output. Prose — a description someone typed — is
 *   the exception and asks for `variant="prose"`.
 *
 * Both are baked in here rather than left to the caller, which is what keeps
 * every detail page in one voice.
 */

// ─── Grid ─────────────────────────────────────────────────────────────────
const definitionListVariants = cva("grid gap-x-8 gap-y-3", {
  variants: {
    columns: {
      /** Stacked — a sidebar or a narrow dialog. */
      1: "grid-cols-1",
      /** The dialog / half-width default. */
      2: "grid-cols-1 sm:grid-cols-2",
      /** The detail-page default: two columns tight, three once there is room. */
      3: "grid-cols-2 md:grid-cols-3",
      /** Short values only — ids and ARNs get too narrow to read at four up. */
      4: "grid-cols-2 md:grid-cols-4",
    },
  },
  defaultVariants: { columns: 3 },
})

/**
 * `"stacked"` puts the label above its value — the detail-page metadata grid.
 * `"inline"` sets them side by side against a fixed label column, for the
 * compact header blocks (a mail message's From/To/Date, an S3 object's
 * content type and size).
 */
type DefinitionLayout = "stacked" | "inline"

/**
 * The layout travels by context so a caller sets it once on the list rather
 * than repeating it on every pair.
 */
const DefinitionLayoutContext = React.createContext<DefinitionLayout>("stacked")

interface DefinitionListProps
  extends React.HTMLAttributes<HTMLDListElement>, VariantProps<typeof definitionListVariants> {
  layout?: DefinitionLayout
}

/** The `<dl>` grid. Use it directly when the list is not inside a card. */
function DefinitionList({
  className,
  columns,
  layout = "stacked",
  children,
  ...props
}: DefinitionListProps) {
  // An inline list reads as a block of rows, so it stays single-column unless
  // the caller asks otherwise.
  const cols = columns ?? (layout === "inline" ? 1 : 3)
  return (
    <dl
      className={cn(
        definitionListVariants({ columns: cols }),
        layout === "inline" && "gap-y-2",
        className,
      )}
      {...props}
    >
      <DefinitionLayoutContext.Provider value={layout}>{children}</DefinitionLayoutContext.Provider>
    </dl>
  )
}

// ─── Item ─────────────────────────────────────────────────────────────────
/**
 * The face and size of a value — the part a call site must not override, so it
 * is applied last. Colour is deliberately not here: see `definitionValueColor`.
 */
const definitionValueVariants = cva("wrap-anywhere", {
  variants: {
    variant: {
      /** Machine output — ids, ARNs, timestamps, sizes, statuses. The default. */
      mono: "font-mono text-xs",
      /** A sentence a human wrote — a description, a status reason. */
      prose: "text-[13px]",
    },
  },
  defaultVariants: { variant: "mono" },
})

/**
 * The default colour, applied *before* the caller's classes so a status value
 * can be tinted (`text-success`, `text-danger`) without losing its font.
 */
const definitionValueColor = {
  mono: "text-fg",
  prose: "text-fg-muted",
} as const

interface DefinitionProps extends VariantProps<typeof definitionValueVariants> {
  label: React.ReactNode
  /** `null`, `undefined` and `""` render as an em dash. */
  value: React.ReactNode
  /** Span the full row — for ARNs and anything else a grid cell cannot hold. */
  full?: boolean
  /** Overrides the enclosing list's layout for this one pair. */
  layout?: DefinitionLayout
  className?: string
  valueClassName?: string
}

/**
 * One label/value pair. The face and size are appended *after* `valueClassName`
 * so `tailwind-merge` keeps them in place — a caller can tint or space a value
 * but cannot quietly drop it out of mono, which is what holds the detail pages
 * in one voice.
 */
function Definition({
  label,
  value,
  variant,
  full,
  layout,
  className,
  valueClassName,
}: DefinitionProps) {
  const inherited = React.useContext(DefinitionLayoutContext)
  const inline = (layout ?? inherited) === "inline"
  const empty = value === null || value === undefined || value === ""
  return (
    <div
      className={cn(
        "flex min-w-0",
        inline ? "gap-3" : "flex-col gap-0.5",
        full && "col-span-full",
        className,
      )}
    >
      <dt
        className={cn(
          fieldLabel,
          "text-fg-subtle",
          // The fixed column is what lines the values up down the block.
          inline && "w-28 shrink-0 pt-0.5",
        )}
      >
        {label}
      </dt>
      <dd
        className={cn(
          definitionValueColor[variant ?? "mono"],
          valueClassName,
          definitionValueVariants({ variant }),
          // An em dash is an absence, not a value, so it recedes regardless.
          empty && "text-fg-subtle",
        )}
      >
        {empty ? "—" : value}
      </dd>
    </div>
  )
}

// ─── Card ─────────────────────────────────────────────────────────────────
// `title` names the card's heading here, not the DOM tooltip the `<dl>` would
// otherwise inherit from `HTMLAttributes`.
interface DefinitionCardProps extends Omit<DefinitionListProps, "title"> {
  /** Optional card heading. Omit it for the bare metadata card most pages use. */
  title?: React.ReactNode
  /** Controls rendered beside the title. Requires `title`. */
  actions?: React.ReactNode
  cardClassName?: string
  contentClassName?: string
}

/** A `Card` wrapping a `DefinitionList` — the metadata block a detail page opens with. */
function DefinitionCard({
  title,
  actions,
  cardClassName,
  contentClassName,
  children,
  ...props
}: DefinitionCardProps) {
  return (
    <Card className={cardClassName}>
      {title && (
        <CardHeader className="flex-row items-center justify-between gap-4">
          <CardTitle>{title}</CardTitle>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </CardHeader>
      )}
      <CardContent className={contentClassName}>
        <DefinitionList {...props}>{children}</DefinitionList>
      </CardContent>
    </Card>
  )
}

export { DefinitionCard, DefinitionList, Definition }
export type { DefinitionCardProps, DefinitionListProps, DefinitionProps }
