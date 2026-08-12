/**
 * Runtime picker data, derived from the emulator's runtime catalog.
 *
 * The catalog (`GET /_overcast/lambda/runtimes`) is generated from the same table the
 * Lambda service validates CreateFunction against, so this module never
 * hard-codes a runtime: keeping a list here is exactly the drift that let the
 * UI offer runtimes the API refused.
 */
import type { LambdaRuntimeInfo } from "@/types"

export interface RuntimeItem {
  value: string
  group: string
  defaultHandler: string
  deprecated: boolean
}

/**
 * Build the grouped runtime items shown in the create wizard.
 *
 * Offer exactly what CreateFunction accepts: a runtime the emulator cannot
 * execute answers 501 and one AWS has blocked answers 400, so neither belongs
 * in a picker. Runtimes past their AWS deprecation date are still deployable —
 * they stay in the list, labelled rather than hidden, sorted below the current
 * runtimes of their family.
 */
export function buildRuntimeItems(runtimes: LambdaRuntimeInfo[]): RuntimeItem[] {
  const items = runtimes
    .filter((rt) => rt.supported && !rt.createBlocked)
    .map((rt, index) => ({
      value: rt.id,
      group: rt.family,
      defaultHandler: rt.defaultHandler,
      deprecated: rt.deprecated,
      index,
    }))

  // The catalog arrives in the AWS model's enum order, which does not keep a
  // family contiguous (the Amazon Linux 2023 Java runtimes are declared last).
  // The combobox emits a group header whenever the family changes, so the list
  // has to be grouped here or a family would get two headers.
  const familyOrder = new Map<string, number>()
  for (const item of items) {
    if (!familyOrder.has(item.group)) familyOrder.set(item.group, familyOrder.size)
  }

  return items
    .sort(
      (a, b) =>
        familyOrder.get(a.group)! - familyOrder.get(b.group)! ||
        Number(a.deprecated) - Number(b.deprecated) ||
        a.index - b.index,
    )
    .map(({ index: _index, ...item }) => item)
}
