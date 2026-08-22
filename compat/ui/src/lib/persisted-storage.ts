/**
 * persisted-storage — thin, failure-tolerant wrapper around localStorage.
 *
 * Persistence here is a convenience (remembering a status filter, a scroll
 * position) never a hard dependency, so every failure mode — storage
 * disabled (private browsing), quota exceeded, malformed JSON left over from
 * an older build — falls back to the caller's default rather than throwing
 * into a render.
 */

const PREFIX = "overcast-compat:";

/** Reads a JSON value written by `writePersisted`, or `fallback` on any
 * failure: missing key, malformed JSON, or storage unavailable entirely. */
export function readPersisted<T>(key: string, fallback: T): T {
  try {
    const raw = window.localStorage.getItem(PREFIX + key);
    if (raw === null) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

/** Writes a JSON-serialisable value. Swallows failures (quota, storage
 * disabled) — nothing here is allowed to break the UI it is decorating. */
export function writePersisted<T>(key: string, value: T): void {
  try {
    window.localStorage.setItem(PREFIX + key, JSON.stringify(value));
  } catch {
    /* ignore — persistence is best-effort */
  }
}
