/**
 * Why a body is missing from a trace, and how to say so.
 *
 * An empty body panel is ambiguous: a request that carried no body and a body
 * this emulator deleted look identical. The server now says which (trace.OmitReason
 * in internal/trace/omission.go); this turns that into something to render.
 */

/**
 * The caps quoted to the reader. They mirror Go constants — `maxTraceBody` in
 * internal/middleware/debugtrace.go and `MaxHopBody` / `MaxHopBodyBytes` in
 * internal/trace/trace.go — and are repeated here because the limits are not
 * served with the trace. If either changes, change it here too.
 */
const PER_BODY_CAP = "1 MiB"
const PER_TRACE_HOP_BUDGET = "8 MiB"

export interface OmissionNotice {
  /** Short label, shown next to (or in place of) the body. */
  label: string
  /** One sentence on what happened and what survived. */
  detail: string
  /**
   * Whether a prefix of the body is still on screen. Partial losses annotate
   * the body that is shown; total losses replace it.
   */
  partial: boolean
  /**
   * Whether the body is still retrievable from the hop's own trace.
   *
   * Internal dispatches go through the router, so a hop is also a request in
   * its own right: the parent's copy of the body is a duplicate, and dropping
   * it loses nothing as long as the callee's trace is still retained. When
   * this is set, the caller should link to it rather than imply the body is
   * gone.
   */
  seeOwnTrace?: boolean
}

/**
 * Describes a body loss, or returns null when nothing was lost.
 *
 * `hasBody` decides `partial` alongside the reason: a size truncation is only
 * partial if a prefix actually survived to be looked at.
 *
 * An unrecognised reason is still described rather than ignored — a server
 * ahead of this UI must not make deleted context read as absent context.
 */
export function describeOmission(
  reason: string | undefined,
  hasBody: boolean,
  hasOwnTrace = false,
): OmissionNotice | null {
  if (!reason) return null

  switch (reason) {
    case "size":
      return {
        label: `Truncated at ${PER_BODY_CAP}`,
        detail: `Only the first ${PER_BODY_CAP} was captured.`,
        partial: hasBody,
      }
    case "trace-budget":
      return {
        label: "Body not captured here",
        detail:
          `This trace had already retained ${PER_TRACE_HOP_BUDGET} of hop bodies, so this copy was dropped. ` +
          (hasOwnTrace
            ? `The hop was dispatched as its own request, and its trace still holds the body in full.`
            : `Everything else about the hop — service, operation, status, timing and ordering — is still recorded.`),
        partial: false,
        // Only offered when the hop really was dispatched through the router.
        // A hop with no request ID has no trace to send the reader to.
        seeOwnTrace: hasOwnTrace,
      }
    case "streaming":
      return {
        label: "Body not captured",
        detail: "The response was streamed, so no body was ever held.",
        partial: false,
      }
    default:
      return {
        label: "Body not captured",
        detail: `The server gave the reason as "${reason}".`,
        partial: false,
      }
  }
}
