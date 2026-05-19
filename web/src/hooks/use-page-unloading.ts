import { useEffect, useRef } from "react"

/**
 * Returns a ref whose `.current` is `true` once the page has started
 * unloading (reload, tab close, or navigation away). Safe to read
 * synchronously inside event handlers and effect cleanups.
 */
export function usePageUnloading(): React.RefObject<boolean> {
  const ref = useRef(false)

  useEffect(() => {
    function handleUnload() {
      ref.current = true
    }
    window.addEventListener("pagehide", handleUnload)
    window.addEventListener("beforeunload", handleUnload)
    return () => {
      window.removeEventListener("pagehide", handleUnload)
      window.removeEventListener("beforeunload", handleUnload)
    }
  }, [])

  return ref
}
