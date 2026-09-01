import { slug } from "@/lib/slug"

/**
 * The reader's preferred code-example language, shared by every CodeTabsGroup
 * (see code-tabs.tsx). localStorage carries the choice across pages; the
 * in-memory listener relay carries it across the groups mounted right now
 * (the `storage` event only fires in OTHER browsing contexts). Reads and
 * writes are try/caught: storage can be absent or throwing (private windows,
 * blocked site data) and the tabs must still render.
 */

const STORAGE_KEY = "overcast.docs.code-tab-language"

/** "Node.js (AWS SDK v3)" → "node-js": the cross-group persistence key. */
export function languageKey(label: string): string {
  return slug(label.replace(/\s*\([^)]*\)\s*$/, ""))
}

const listeners = new Set<(key: string) => void>()

export function getStoredLanguage(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

export function setPreferredLanguage(key: string): void {
  try {
    localStorage.setItem(STORAGE_KEY, key)
  } catch {
    // best-effort persistence only
  }
  for (const listener of listeners) listener(key)
}

export function subscribeLanguage(listener: (key: string) => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}
