import { SkeletonRows } from "@/components/ui/skeleton"

/**
 * Router-level pending fallback — `defaultPendingComponent` in main.tsx.
 *
 * Shown in place of a route's content while its code chunk or loader is
 * still pending, so a navigation paints feedback immediately instead of
 * looking like a dropped click. The app shell (sidebar, header) lives
 * outside the route Outlet and stays put; this only has to sketch the
 * `<main>` content area, and the design system's static skeleton rows
 * (see skeleton.tsx) are exactly that sketch.
 */
export function RoutePending() {
  return <SkeletonRows noun="page" />
}
