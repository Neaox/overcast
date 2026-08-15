/**
 * RegionElsewhereNotice — "No stacks in us-east-1. There are 3 in ap-southeast-2."
 *
 * Rendered inside a list page's empty state, and only there. An empty list is
 * the symptom this explains, and it is the only place the explanation is worth
 * anything: a permanent banner about regions is a banner people stop reading,
 * and by the time they need this one they need to believe it.
 *
 * The trap it closes: a developer's tooling deploys into the region AWS_REGION
 * names while the console lists the region it defaults to. Neither side
 * mentions the other, so an empty page reads as a lost deploy or a broken
 * emulator. Nothing else in the picture can see both halves.
 *
 * It renders nothing unless the server says there is something to say — see
 * `preflightRegionQueryOptions`. The "only on a matched symptom" rule lives on
 * the server so that no console surface can hold a different opinion about
 * when this is allowed to speak up.
 */
import { useQuery } from "@tanstack/react-query"
import { Globe } from "lucide-react"
import { preflightRegionQueryOptions } from "@/features/preflight/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { endpointStore } from "@/services/endpoint-store"
import type { PreflightRegionElsewhere, PreflightRegionKind } from "@/services/api/preflight"

interface RegionElsewhereNoticeProps {
  kind: PreflightRegionKind
  /** Plural noun for what the page failed to show, e.g. "stacks". */
  noun: string
}

export function RegionElsewhereNotice({ kind, noun }: RegionElsewhereNoticeProps) {
  const endpoint = useEndpoint()
  // A failed check is not a finding. If the store read failed, or this
  // emulator is older than the route, the page keeps its ordinary empty state
  // rather than growing an error about a diagnostic nobody asked for — and
  // does not retry, because there is nothing here worth a second request.
  const { data } = useQuery({ ...preflightRegionQueryOptions(kind), retry: false })

  const elsewhere = data?.elsewhere ?? []
  if (!data || elsewhere.length === 0) return null

  return (
    <div className="mx-auto -mt-8 mb-8 flex max-w-lg flex-col items-center gap-2.5 rounded-control border border-border bg-bg-subtle px-4 py-3 text-center">
      <p className="text-[13px] text-fg-muted">
        <Globe className="mr-1.5 inline h-3.5 w-3.5 align-text-bottom text-fg-subtle" />
        No {noun} in <span className="font-mono text-fg">{data.region}</span>. {verb(elsewhere)}{" "}
        {elsewhere.map((other, i) => (
          <span key={other.region}>
            {separator(i, elsewhere.length)}
            <span className="text-fg tabular-nums">{other.count}</span> in{" "}
            <span className="font-mono text-fg">{other.region}</span>
          </span>
        ))}
        .
      </p>
      <div className="flex flex-wrap justify-center gap-2">
        {elsewhere.map((other) => (
          <button
            key={other.region}
            onClick={() => endpointStore.set({ ...endpoint, region: other.region })}
            className="rounded-control border border-border px-2 py-1 font-mono text-xs text-fg-muted transition-colors hover:border-accent hover:text-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none"
          >
            Switch to {other.region}
          </button>
        ))}
      </div>
    </div>
  )
}

/**
 * Agrees with the total, not with the number of regions: one resource spread
 * over one region still reads "There is 1 in …", and two regions holding one
 * each read "There are 1 in … and 1 in …", which is what English does.
 */
function verb(elsewhere: PreflightRegionElsewhere[]): string {
  const total = elsewhere.reduce((sum, other) => sum + other.count, 0)
  return total === 1 ? "There is" : "There are"
}

/** ", " between the middle items and " and " before the last. */
function separator(index: number, length: number): string {
  if (index === 0) return ""
  return index === length - 1 ? " and " : ", "
}
