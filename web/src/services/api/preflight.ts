import { apiFetch } from "./base"

/**
 * The kinds of resource the region preflight can answer about.
 *
 * The set is the emulator's, not the console's — `internal/router/preflight_region.go`
 * holds the registry, and asking for a kind it does not know is a 400 rather
 * than a quiet empty answer. Adding a page to this check therefore starts
 * there, and this union is what stops a typo reaching the network.
 */
export type PreflightRegionKind =
  | "cloudformation-stacks"
  | "sqs-queues"
  | "lambda-functions"
  | "dynamodb-tables"
  | "sns-topics"
  | "kinesis-streams"

/** One other region that holds resources of the kind the page found none of. */
export interface PreflightRegionElsewhere {
  region: string
  count: number
}

/**
 * Why a region-scoped list page is empty.
 *
 * `elsewhere` is empty whenever there is nothing worth saying — including when
 * the selected region is not actually empty — so a caller renders on
 * `elsewhere.length > 0` and needs no rule of its own. The judgement lives on
 * the server precisely so two console surfaces cannot disagree about when this
 * is allowed to speak up.
 */
export interface PreflightRegionAdvisory {
  kind: PreflightRegionKind
  region: string
  /** What the selected region holds — 0 whenever there is an advisory. */
  count: number
  elsewhere: PreflightRegionElsewhere[]
}

export const preflight = {
  region: (kind: PreflightRegionKind) =>
    apiFetch<PreflightRegionAdvisory>(`/preflight/region?kind=${encodeURIComponent(kind)}`),
}
