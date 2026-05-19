import { tv } from "tailwind-variants";
import { cn } from "../lib/cn";

export function PassRateBar({
  pass,
  fail,
  skip = 0,
  unimplemented = 0,
}: {
  pass: number;
  fail: number;
  skip?: number;
  unimplemented?: number;
}) {
  // Skip and N/A are excluded from the denominator — they represent suite
  // gaps or SDK gaps, not Overcast incompatibilities.
  const tested = pass + fail + unimplemented;
  if (tested === 0) return null;
  const raw = (pass / tested) * 100;
  // Only show 100% when everything actually passed — avoid rounding 99.8 to 100.
  const pct = raw === 100 ? 100 : Math.min(Math.round(raw), 99);
  const level = pct === 100 ? "full" : pct >= 70 ? "good" : "low";

  return (
    <div className="flex items-center gap-2">
      <div
        className={cn(
          "flex-1 h-1.5 bg-gray-100 dark:bg-gray-700 rounded-full overflow-hidden min-w-24",
        )}
      >
        <div className={bar({ level })} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-xs text-gray-500 dark:text-gray-400 tabular-nums shrink-0 w-8 text-right">
        {pct}%
      </span>
    </div>
  );
}

const bar = tv({
  base: "h-full rounded-full transition-all duration-500",
  variants: {
    level: {
      full: "bg-green-500",
      good: "bg-amber-400",
      low: "bg-red-400",
    },
  },
});
