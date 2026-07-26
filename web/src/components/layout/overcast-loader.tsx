import { OvercastMark } from "@/components/brand/overcast-mark"
import { cn } from "@/lib/utils"

/**
 * The blinking cursor is the only sanctioned motion in the system, so the
 * loader is the brand mark with its prompt cursor — the `_` after the `>` —
 * blinking, rather than a rotating ring.
 *
 * The cursor is the mark's trailing `<line>`; selecting it structurally keeps
 * the geometry in `overcast-mark.tsx` as the single source of truth instead of
 * forking the brand paths into a second file. That also means the shared
 * `.oc-cursor-blink` class cannot be applied directly — the animated element
 * lives inside the brand SVG — so this mirrors it, deliberately frame- and
 * duration-identical so the two declarations of the keyframes are interchangeable.
 *
 * Under `prefers-reduced-motion: reduce` the cursor goes **solid, not hidden**:
 * it is a load-bearing part of the glyph, and hiding it mutilates the mark.
 */
const LOADER_STYLES = `
@keyframes oc-cursor-blink { 0%, 49% { opacity: 1 } 50%, 100% { opacity: 0 } }
.oc-loader line { animation: oc-cursor-blink 1.1s steps(1, end) infinite }
@media (prefers-reduced-motion: reduce) {
  .oc-loader line { animation: none; opacity: 1 }
}
`

interface OvercastLoaderProps {
  /** Rendered width and height in pixels. */
  size?: number
  /** Announced to assistive tech — the loader is the only status glyph shown. */
  label?: string
  className?: string
}

export function OvercastLoader({
  size = 72,
  label = "Connecting",
  className,
}: OvercastLoaderProps) {
  return (
    <span role="img" aria-label={label} className={cn("inline-flex", className)}>
      {/* Hoisted and de-duplicated by React on `href`, so repeat renders emit one tag. */}
      <style href="oc-loader" precedence="medium">
        {LOADER_STYLES}
      </style>
      <OvercastMark size={size} className="oc-loader" />
    </span>
  )
}
