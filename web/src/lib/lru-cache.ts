/**
 * Insertion-ordered LRU bounded by *cost*, not entry count — because
 * consumers' values have wildly different weights per entry: the highlight
 * kernel caches HTML strings (cost: characters) and token arrays (cost:
 * token count), the log formatter caches pretty-printed documents. An
 * entry-count bound sized for one is either useless or ruinous for another,
 * and a size *refusal* would deny caching to exactly the large values whose
 * recomputation is most expensive. One entry may take at most a quarter of
 * the budget, so a single pathological value cannot evict everything else.
 */
export class LruCache<V> {
  private readonly entries = new Map<string, { value: V; cost: number }>()
  private total = 0
  private readonly budget: number
  constructor(budget: number) {
    this.budget = budget
  }

  /** Re-inserts on hit so the map's insertion order is least-recent first. */
  get(key: string): V | undefined {
    const entry = this.entries.get(key)
    if (entry === undefined) return undefined
    this.entries.delete(key)
    this.entries.set(key, entry)
    return entry.value
  }

  put(key: string, value: V, cost: number): void {
    if (cost > this.budget / 4) return
    const prior = this.entries.get(key)
    if (prior !== undefined) {
      this.entries.delete(key)
      this.total -= prior.cost
    }
    while (this.total + cost > this.budget) {
      const oldest = this.entries.entries().next().value
      if (oldest === undefined) break
      this.entries.delete(oldest[0])
      this.total -= oldest[1].cost
    }
    this.entries.set(key, { value, cost })
    this.total += cost
  }
}
