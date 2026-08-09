import { useEffect, useRef, useState } from "react"
import { useDebouncedValue } from "./use-debounced-value"

/**
 * Two-way binding between a text input and a value that lives in the URL.
 *
 * The URL is the source of truth, so the input follows it whenever it changes
 * from the outside — Back/Forward, or opening a shared link — which is what
 * makes a restored history entry restore the text as well as the rest of the
 * filters. Typing updates the local value immediately (the input stays
 * responsive) and commits to the URL only once it has been stable for
 * `delayMs`, so a burst of typing costs one navigation instead of one per
 * keystroke.
 *
 * @param urlValue current value from the URL ("" when the param is absent)
 * @param commit   writes the settled value back to the URL
 * @returns `[value, setValue]` for the input element
 */
export function useDebouncedTextParam(
  urlValue: string,
  commit: (next: string) => void,
  delayMs = 300,
): [string, (next: string) => void] {
  const [value, setValue] = useState(urlValue)
  const [seenUrlValue, setSeenUrlValue] = useState(urlValue)

  // Adjust during render rather than in an effect (React's "resetting state
  // when a prop changes"): the input then never paints a frame of stale text
  // after a Back navigation.
  if (urlValue !== seenUrlValue) {
    setSeenUrlValue(urlValue)
    setValue(urlValue)
  }

  const debounced = useDebouncedValue(value, delayMs)

  // Read-only mirrors, so the effect below can stay keyed on the settled text
  // alone without ever reading a stale URL value or a stale callback. They are
  // refreshed in an effect declared *before* the one that reads them, so both
  // are already current by the time it runs in the same commit.
  const urlRef = useRef(urlValue)
  const commitRef = useRef(commit)
  useEffect(() => {
    urlRef.current = urlValue
    commitRef.current = commit
  })

  useEffect(() => {
    // Deliberately keyed on the settled text only. Re-running this when the
    // URL changes would make a Back navigation immediately re-commit the text
    // the user had typed before it, undoing the restore it just performed.
    if (debounced === urlRef.current) return
    commitRef.current(debounced)
  }, [debounced])

  return [value, setValue]
}
