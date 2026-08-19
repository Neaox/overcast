/**
 * The highlight kernel's tokenization worker.
 *
 * One persistent worker serves every ranges-backend consumer — spawned once
 * by the facade ([highlight-code.ts](./highlight-code.ts)), never per call.
 * That inversion is why Prism's own async mode being off by default (its FAQ:
 * a worker per call pays spawn + serialization that dwarfs small-snippet
 * tokenize time) does not apply here: spawn is amortized to zero, cache hits
 * never reach the worker at all, and the facade keeps documents below its
 * measured threshold on the main thread where a round-trip would cost more
 * than the tokenize.
 *
 * The protocol is one request message, one response message, correlated by
 * `id`. `handleHighlightRequest` is exported so tests exercise the protocol
 * without a real worker; the `onmessage` wiring below only engages inside an
 * actual worker scope.
 */
import { packTokenRanges, tokenizeToRanges, type PackedTokenRanges } from "./prism-ranges"

export interface HighlightWorkRequest {
  id: number
  text: string
  language: string
}

/**
 * The reply travels packed, not as range objects: the packed buffer moves as
 * a zero-copy transfer, where structured-cloning one range object per token
 * measured slower than tokenizing (docs/plans/logs-view-performance.md §3f).
 *
 * Memory-strategy note, per direction: the request stays a plain clone — a
 * string cannot be transferred, its clone measured ≤0.03 ms at the largest
 * legal document, and pre-encoding it into a transferable buffer would cost
 * the main thread more than the clone it avoids. The reply's offsets are the
 * measured hot spot, hence the transfer; its `types` table stays a clone
 * (bounded by the handful of distinct token classes). `SharedArrayBuffer` is
 * deliberately not used: it requires cross-origin isolation the console does
 * not ship, adds ring-buffer sizing for an output whose length is unknown
 * before tokenizing, and cannot beat a pointer-handoff transfer for a
 * one-shot request/reply — the remaining main-thread costs (unpacking, DOM
 * `Range` construction) live outside the messaging layer entirely.
 */
export interface HighlightWorkResponse extends PackedTokenRanges {
  id: number
}

/** The worker's whole job, as a pure function: tokenize one request. */
export function handleHighlightRequest(request: HighlightWorkRequest): HighlightWorkResponse {
  return { id: request.id, ...packTokenRanges(tokenizeToRanges(request.text, request.language)) }
}

// A dedicated worker scope has `self` and no `window`; the main thread and
// jsdom both have `window`. Guarding on that keeps this module importable
// everywhere (tests import the handler; the facade imports the types).
if (typeof window === "undefined" && typeof self !== "undefined" && "postMessage" in self) {
  const scope = self as unknown as {
    onmessage: ((event: MessageEvent<HighlightWorkRequest>) => void) | null
    postMessage: (message: HighlightWorkResponse, transfer: Transferable[]) => void
  }
  scope.onmessage = (event) => {
    const response = handleHighlightRequest(event.data)
    scope.postMessage(response, [response.packed.buffer])
  }
}
