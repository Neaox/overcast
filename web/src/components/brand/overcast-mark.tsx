interface OvercastMarkProps {
  /** Rendered width and height in pixels. */
  size?: number
  className?: string
}

/**
 * Overcast cloud mark. Colours come from CSS tokens so the mark follows the
 * active theme without a JS theme prop.
 */
export function OvercastMark({ size = 24, className }: OvercastMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 96 96"
      fill="none"
      aria-hidden="true"
      focusable="false"
      className={className}
    >
      <path
        d="M 20.38 74.23 A 16.38 16.38 0 1 1 20.54 41.47 A 26.51 26.51 0 0 1 69.28 32.64 A 20.84 20.84 0 1 1 71.16 74.23 Z"
        fill="var(--cloud)"
        stroke="var(--cloud-stroke)"
        strokeWidth={1.8}
      />
      <path
        d="M 21.33 40.51 L 37.61 51.93 L 21.33 63.36"
        fill="none"
        stroke="var(--accent)"
        strokeWidth={2.6}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <line
        x1={47.29}
        y1={63.36}
        x2={70.17}
        y2={63.36}
        stroke="var(--accent-glow)"
        strokeWidth={2.6}
        strokeLinecap="round"
      />
    </svg>
  )
}
