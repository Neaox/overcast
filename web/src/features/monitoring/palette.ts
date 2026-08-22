/**
 * Fixed categorical series color order for Monitor tab/section charts
 * (dataviz skill: "Assign categorical hues in fixed order, never cycled").
 * Split into its own module (not co-located with metric-line-chart.tsx) so
 * that file only exports the component, satisfying react-refresh's
 * only-export-components rule.
 */
const SERIES_COLOR_CLASSES = [
  "text-cat-1",
  "text-cat-2",
  "text-cat-3",
  "text-cat-4",
  "text-cat-5",
] as const

export function seriesColorClass(index: number): string {
  return SERIES_COLOR_CLASSES[index % SERIES_COLOR_CLASSES.length]
}
