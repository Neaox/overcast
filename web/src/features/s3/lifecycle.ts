/**
 * Presentation helpers for S3 bucket lifecycle rules.
 *
 * `estimateExpiry` mirrors the emulator's own expiry arithmetic
 * (internal/services/s3/lifecycle.go) so the object list can show a per-row
 * hint without a HeadObject per row. It is deliberately an *estimate*: a
 * listing carries a key, a size and a last-modified date, but not the object's
 * tags, so a tag-filtered rule cannot be evaluated here. Rather than guess,
 * those rules report "unknown" and the UI says so — the object inspector shows
 * the server's authoritative x-amz-expiration instead.
 */
import type { S3LifecycleRule, S3LifecycleFilter } from "@/types"

const MS_PER_DAY = 24 * 60 * 60 * 1000

/** The listing fields a rule can be evaluated against. */
export interface LifecycleCandidate {
  key: string
  size: number
  lastModified: string
}

export type ExpiryEstimate =
  /** No enabled rule expires this object. */
  | { kind: "none" }
  /** A rule expires it at `date`, under rule `ruleId`. */
  | { kind: "expires"; date: Date; ruleId: string }
  /** A rule might expire it, but depends on tags a listing does not carry. */
  | { kind: "unknown"; ruleId: string }

/** The prefix a rule selects on, in either the legacy or the Filter form. */
export function rulePrefix(rule: S3LifecycleRule): string {
  if (rule.prefix !== undefined) return rule.prefix
  if (!rule.filter) return ""
  return rule.filter.and?.prefix ?? rule.filter.prefix ?? ""
}

/** Whether the rule's filter depends on object tags. */
function dependsOnTags(filter?: S3LifecycleFilter): boolean {
  if (!filter) return false
  return !!filter.tag || (filter.and?.tags.length ?? 0) > 0
}

/** AWS's object-size bounds are exclusive at both ends. */
function sizeWithin(size: number, greaterThan?: number, lessThan?: number): boolean {
  if (greaterThan !== undefined && size <= greaterThan) return false
  if (lessThan !== undefined && size >= lessThan) return false
  return true
}

function matchesIgnoringTags(rule: S3LifecycleRule, object: LifecycleCandidate): boolean {
  if (!object.key.startsWith(rulePrefix(rule))) return false
  const filter = rule.filter
  if (!filter) return true
  if (filter.and) {
    return sizeWithin(object.size, filter.and.objectSizeGreaterThan, filter.and.objectSizeLessThan)
  }
  return sizeWithin(object.size, filter.objectSizeGreaterThan, filter.objectSizeLessThan)
}

/**
 * Rounds a time up to the next UTC midnight, which is how S3 turns "N days
 * after the object was written" into a concrete expiry.
 *
 * A time already sitting on a midnight is returned unchanged — rounding it
 * forward would hand the object an extra day. Mirrors `nextUTCMidnight` in
 * internal/services/s3/lifecycle.go; the two must agree or the UI's hint and
 * the sweeper's deletion disagree by a day.
 */
export function nextUtcMidnight(at: Date): Date {
  const day = Math.floor(at.getTime() / MS_PER_DAY) * MS_PER_DAY
  return new Date(day === at.getTime() ? day : day + MS_PER_DAY)
}

/** When the given rule would expire the object, if it expires it at all. */
function ruleExpiry(rule: S3LifecycleRule, object: LifecycleCandidate): Date | undefined {
  if (rule.expirationDate) return new Date(rule.expirationDate)
  if (rule.expirationDays === undefined) return undefined
  const written = new Date(object.lastModified).getTime()
  if (Number.isNaN(written)) return undefined
  return nextUtcMidnight(new Date(written + rule.expirationDays * MS_PER_DAY))
}

/**
 * Estimates when the sweeper will expire the object. Where several enabled
 * rules select it, the earliest wins — the same resolution the emulator makes.
 */
export function estimateExpiry(
  rules: S3LifecycleRule[],
  object: LifecycleCandidate,
): ExpiryEstimate {
  let best: { date: Date; ruleId: string } | undefined
  let unknown: string | undefined

  for (const rule of rules) {
    if (rule.status !== "Enabled") continue
    if (rule.expirationDays === undefined && !rule.expirationDate) continue
    if (!matchesIgnoringTags(rule, object)) continue
    if (dependsOnTags(rule.filter)) {
      unknown ??= rule.id
      continue
    }
    const date = ruleExpiry(rule, object)
    if (!date) continue
    if (!best || date < best.date) best = { date, ruleId: rule.id }
  }

  if (best) return { kind: "expires", ...best }
  if (unknown) return { kind: "unknown", ruleId: unknown }
  return { kind: "none" }
}

/** A short relative rendering of an expiry, e.g. "in 3d" or "due". */
export function formatExpiryDistance(expiry: Date, now: Date = new Date()): string {
  const ms = expiry.getTime() - now.getTime()
  if (ms <= 0) return "due"
  const days = Math.floor(ms / MS_PER_DAY)
  if (days >= 1) return `in ${days}d`
  const hours = Math.max(1, Math.floor(ms / (60 * 60 * 1000)))
  return `in ${hours}h`
}

/** A human description of what a rule selects, for the configuration tab. */
export function describeLifecycleFilter(rule: S3LifecycleRule): string {
  const parts: string[] = []
  const prefix = rulePrefix(rule)
  parts.push(prefix ? `prefix ${prefix}` : "whole bucket")

  const filter = rule.filter
  const tags = filter?.and?.tags ?? (filter?.tag ? [filter.tag] : [])
  for (const tag of tags) parts.push(`tag ${tag.key}=${tag.value}`)

  const gt = filter?.and?.objectSizeGreaterThan ?? filter?.objectSizeGreaterThan
  const lt = filter?.and?.objectSizeLessThan ?? filter?.objectSizeLessThan
  if (gt !== undefined) parts.push(`size > ${gt} B`)
  if (lt !== undefined) parts.push(`size < ${lt} B`)

  return parts.join(" · ")
}

/** Human descriptions of every action a rule takes. */
export function describeLifecycleActions(rule: S3LifecycleRule): string[] {
  const actions: string[] = []
  if (rule.expirationDays !== undefined) {
    actions.push(`Expire after ${rule.expirationDays} day${rule.expirationDays === 1 ? "" : "s"}`)
  }
  if (rule.expirationDate) {
    actions.push(`Expire on ${rule.expirationDate.slice(0, 10)}`)
  }
  for (const transition of rule.transitions) {
    const when =
      transition.days !== undefined
        ? `after ${transition.days} day${transition.days === 1 ? "" : "s"}`
        : `on ${(transition.date ?? "").slice(0, 10)}`
    actions.push(`Mark ${transition.storageClass} ${when}`)
  }
  if (rule.abortIncompleteMultipartUploadDays !== undefined) {
    actions.push(
      `Abort incomplete uploads after ${rule.abortIncompleteMultipartUploadDays} day` +
        (rule.abortIncompleteMultipartUploadDays === 1 ? "" : "s"),
    )
  }
  return actions
}
