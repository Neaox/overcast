/**
 * Sparkline — a compact SVG line chart for rendering metric history.
 *
 * Renders a filled area + stroke line. No external dependencies.
 * Pass `data` as an array of numbers (raw values, any scale).
 */
interface SparklineProps {
  data: number[]
  color?: string
  fillOpacity?: number
  height?: number
  width?: number
  className?: string
}

export function Sparkline({
  data,
  color = "currentColor",
  fillOpacity = 0.12,
  height = 48,
  width = 160,
  className,
}: SparklineProps) {
  if (data.length < 2) {
    return <div style={{ width, height }} className={className} />
  }

  const pad = 2
  const min = Math.min(...data)
  const max = Math.max(...data)
  const range = max - min || 1

  const xs = data.map((_, i) => pad + (i / (data.length - 1)) * (width - 2 * pad))
  const ys = data.map((v) => pad + (1 - (v - min) / range) * (height - 2 * pad))

  const linePath = xs
    .map((x, i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${ys[i].toFixed(1)}`)
    .join(" ")
  const fillPath = `${linePath} L${xs[xs.length - 1].toFixed(1)},${(height - pad).toFixed(1)} L${xs[0].toFixed(1)},${(height - pad).toFixed(1)} Z`

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={className}
      aria-hidden="true"
      preserveAspectRatio="none"
    >
      <path d={fillPath} fill={color} fillOpacity={fillOpacity} stroke="none" />
      <path
        d={linePath}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
