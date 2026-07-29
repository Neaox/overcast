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
/**
 * The list is a **container query context**, so its shape follows the width of
 * the card it is in rather than the width of the browser. The same list is a
 * vertical label/value run inside Lambda's `max-w-2xl` configuration card and a
 * three-up grid on a full-width detail page, with no call site deciding.
 *
 * `@3xl` (48rem) is where a fixed label column stops paying for itself and the
 * values have room to sit under their labels instead of beside them.
 */
const definitionListVariants = cva("grid gap-x-8 gap-y-3", {
  variants: {
    columns: {
      /** Follows the container. The default — prefer it over pinning a count. */
      auto: "grid-cols-1 @3xl:grid-cols-2 @5xl:grid-cols-3",
      /** Stacked — a sidebar or a narrow dialog. */
      1: "grid-cols-1",
      /** The dialog / half-width shape. */
      2: "grid-cols-1 @2xl:grid-cols-2",
      3: "grid-cols-2 @3xl:grid-cols-3",
      /** Short values only — ids and ARNs get too narrow to read at four up. */
      4: "grid-cols-2 @3xl:grid-cols-4",
    },
  },
  defaultVariants: { columns: "auto" },
})

/**
 * `"auto"` — the default — is inline while the card is narrow and stacked once
 * it is wide, at the same `@3xl` breakpoint the column count uses.
 *
 * The two fixed values are for lists whose width does not tell the whole story:
 * `"inline"` for a block that should stay a label/value run however much room
 * it is given (a mail message's From/To/Date), `"stacked"` for one that should
 * never take a fixed label column.
 */
type DefinitionLayout = "auto" | "stacked" | "inline"

/**
 * The layout travels by context so a caller sets it once on the list rather
 * than repeating it on every pair.
 */
const DefinitionLayoutContext = React.createContext<DefinitionLayout>("auto")

interface DefinitionListProps
  extends React.HTMLAttributes<HTMLDListElement>, VariantProps<typeof definitionListVariants> {
  layout?: DefinitionLayout
}

/** The `<dl>` grid. Use it directly when the list is not inside a card. */
function DefinitionList({
  className,
  columns,
  layout = "auto",
  children,
  ...props
}: DefinitionListProps) {
  // A list that is always a label/value run stays single-column: a second
  // column of fixed labels reads as a table without being one.
  const cols = columns ?? (layout === "inline" ? 1 : "auto")
  return (
    // The container is a wrapper, not the `<dl>` itself: `@container` makes an
    // element a query context for its *descendants*, so a `<dl>` carrying both
    // `@container` and `@3xl:grid-cols-2` would never match its own width — the
    // variant would resolve against whatever container sits further up instead.
    <div className="@container">
      <dl
        className={cn(
          definitionListVariants({ columns: cols }),
          layout === "inline" && "gap-y-2",
          className,
        )}
        {...props}
      >
        <DefinitionLayoutContext.Provider value={layout}>
          {children}
        </DefinitionLayoutContext.Provider>
      </dl>
    </div>
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

/**
 * Inline and stacked differ only in flex direction and whether the label takes
 * a fixed column, so `auto` is expressible in CSS — the pair reflows with the
 * card at `@3xl` without React re-rendering or measuring anything.
 */
const definitionItemVariants = cva("flex min-w-0", {
  variants: {
    layout: {
      auto: "gap-3 @3xl:flex-col @3xl:gap-0.5",
      inline: "gap-3",
      stacked: "flex-col gap-0.5",
    },
  },
  defaultVariants: { layout: "auto" },
})

/** The fixed label column exists only while the pair is inline. */
const definitionLabelVariants = cva("text-fg-subtle", {
  variants: {
    layout: {
      auto: "w-28 shrink-0 pt-0.5 @3xl:w-auto @3xl:pt-0",
      inline: "w-28 shrink-0 pt-0.5",
      stacked: "",
    },
  },
  defaultVariants: { layout: "auto" },
})

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
  const resolved = layout ?? inherited
  const empty = value === null || value === undefined || value === ""
  return (
    <div
      className={cn(
        definitionItemVariants({ layout: resolved }),
        full && "col-span-full",
        className,
      )}
    >
      <dt className={cn(fieldLabel, definitionLabelVariants({ layout: resolved }))}>{label}</dt>
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
