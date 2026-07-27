import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

/**
 * Static loading skeletons — design turn 5b ("loading patterns").
 *
 * Three rules from the design are binding and deliberately not configurable:
 *
 * 1. **Skeletons are static.** *"Skeletons are static. Nothing shimmers."* No
 *    pulse, no shimmer, no sweep, no transition.
 * 2. **Depth is opacity, never a second colour.** Every bar is filled with
 *    `--border` and stepped down the ladder `1.0 / 0.7 / 0.6 / 0.55` as it
 *    recedes left-to-right and top-to-bottom.
 * 3. **The blinking cursor is the only motion.** `.oc-cursor-blink` (defined in
 *    `styles/global.css`) is a 1.1s hard square wave that goes *solid* — never
 *    hidden — under `prefers-reduced-motion: reduce`.
 *
 * Placeholder counts follow the design's `hint-placeholder-count` convention:
 * 5 table rows, 3 dashboard cards, both overridable.
 */

// ─── Bar ──────────────────────────────────────────────────────────────────
const skeletonVariants = cva("block rounded-[3px] bg-border", {
  variants: {
    /** Opacity ladder from 5b. `0` is the nearest plane, `3` the furthest. */
    depth: {
      "0": "opacity-100",
      "1": "opacity-70",
      "2": "opacity-60",
      "3": "opacity-[0.55]",
    },
  },
  defaultVariants: { depth: "0" },
})

interface SkeletonProps
  extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof skeletonVariants> {}

/** A single flat placeholder bar. Geometry comes from `className`. */
function Skeleton({ depth, className, ...props }: SkeletonProps) {
  return <span aria-hidden className={cn(skeletonVariants({ depth }), className)} {...props} />
}

// ─── Blinking cursor ──────────────────────────────────────────────────────
const cursorVariants = cva("oc-cursor-blink inline-block shrink-0 bg-accent-glow", {
  variants: {
    /** The three cursor geometries drawn in turn 5, scaled to their type. */
    size: {
      /** 6 × 12 — beside 11px mono copy (skeleton footer, idle event tail). */
      text: "h-[12px] w-[6px]",
      /** 6 × 13 — inside a 32px busy control. */
      control: "h-[13px] w-[6px]",
      /** 7 × 20 — beside a 24px metric value. */
      display: "h-[20px] w-[7px]",
    },
  },
  defaultVariants: { size: "text" },
})

interface BlinkingCursorProps
  extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof cursorVariants> {}

/**
 * Terminal-style caret. Blinks on a 1.1s square wave and renders solid under
 * `prefers-reduced-motion: reduce` — it is a layout element and part of the
 * sentence it sits in, so hiding it would reflow the line and lose meaning.
 *
 * On an accent fill pass `className="bg-fg-on-accent"` to invert it.
 */
function BlinkingCursor({ size, className, ...props }: BlinkingCursorProps) {
  return <span aria-hidden className={cn(cursorVariants({ size }), className)} {...props} />
}

// ─── Table skeleton ───────────────────────────────────────────────────────
/**
 * Column-1 bar widths, verbatim from 5b. Deliberately irregular and
 * deliberately fixed — randomising reshuffles on every render, which is the
 * flicker skeletons exist to prevent. Cycled when `rows` exceeds the array.
 */
const ROW_BAR_WIDTHS = ["w-[72%]", "w-[58%]", "w-[81%]", "w-[44%]", "w-[66%]"] as const

interface SkeletonRowsProps {
  /** Placeholder row count (`hint-placeholder-count`). */
  rows?: number
  /** Lowercase plural resource noun — renders as `loading log groups`. */
  noun?: string
  className?: string
}

/**
 * Table body placeholder: `rows` skeleton rows over a centred `loading <noun>`
 * footer. Row padding is **10px**, not the 12px the artboard draws on its own
 * skeleton rows — every real data row in the design is 10px, and matching them
 * is what stops the 2px-per-row jump on load.
 */
function SkeletonRows({ rows = 5, noun = "data", className }: SkeletonRowsProps) {
  return (
    <div role="status" aria-busy className={cn("flex flex-col", className)}>
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          data-slot="skeleton-row"
          className="grid grid-cols-[1fr_110px_90px] items-center gap-3 border-b border-border px-4 py-[10px]"
        >
          <Skeleton className={cn("h-[9px]", ROW_BAR_WIDTHS[i % ROW_BAR_WIDTHS.length])} />
          <Skeleton depth="1" className="h-[9px] w-full" />
          <Skeleton depth="3" className="h-[9px] w-[46px]" />
        </div>
      ))}
      <p className="flex items-center justify-center gap-2 p-[10px] font-mono text-[11px] text-fg-subtle">
        <span>{`loading ${noun}`}</span>
        <BlinkingCursor />
      </p>
    </div>
  )
}

// ─── Card skeleton ────────────────────────────────────────────────────────
/** Title-bar widths per card, verbatim from 5b. Cycled when `cards` exceeds it. */
const CARD_TITLE_WIDTHS = ["w-[62%]", "w-[48%]", "w-[70%]"] as const

interface SkeletonCardsProps {
  /** Placeholder card count (`hint-placeholder-count`). */
  cards?: number
  /** Lowercase plural resource noun, announced to assistive tech. */
  noun?: string
  className?: string
}

/** Dashboard grid placeholder: 3-up cards, each an icon tile over two text bars. */
function SkeletonCards({ cards = 3, noun = "data", className }: SkeletonCardsProps) {
  return (
    <div role="status" aria-busy className={cn("grid grid-cols-3 gap-2", className)}>
      <span className="sr-only">{`loading ${noun}`}</span>
      {Array.from({ length: cards }, (_, i) => (
        <div
          key={i}
          data-slot="skeleton-card"
          className="flex flex-col gap-3 rounded-card border border-border bg-bg-elevated p-3"
        >
          <div className="flex h-[30px] items-center justify-between">
            <Skeleton className="h-[30px] w-[30px] rounded-control" />
            <Skeleton depth="2" className="h-[8px] w-[34px]" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Skeleton className={cn("h-[10px]", CARD_TITLE_WIDTHS[i % CARD_TITLE_WIDTHS.length])} />
            <Skeleton depth="3" className="h-[8px] w-[56%]" />
          </div>
        </div>
      ))}
    </div>
  )
}

export { Skeleton, BlinkingCursor, SkeletonRows, SkeletonCards }
export type { SkeletonProps, BlinkingCursorProps, SkeletonRowsProps, SkeletonCardsProps }
