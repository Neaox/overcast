import { Link as RouterLink } from "@tanstack/react-router"
import { describeOmission } from "../omission"

/**
 * The two ways a body loss is shown, split so that a caller can render both
 * unconditionally and get exactly the right one:
 *
 * - `BodyOmissionChip` annotates a body that is still on screen (a size
 *   truncation kept its prefix).
 * - `BodyOmissionNotice` stands in for a body that is not there at all.
 *
 * Each returns null in the other's case, and both return null when nothing was
 * lost. A body panel must never render empty and unexplained: that is
 * indistinguishable from a request that carried no body.
 */

interface Props {
  reason: string | undefined
  hasBody: boolean
  /**
   * The hop's own request ID, when it was dispatched through the router. The
   * parent's copy of a hop body is a duplicate of that trace's, so a body
   * dropped here is still readable there.
   */
  ownRequestId?: string
}

export function BodyOmissionChip({ reason, hasBody }: Props) {
  const notice = describeOmission(reason, hasBody)
  if (!notice?.partial) return null
  return (
    <span className="ml-2 text-warning" title={notice.detail}>
      ({notice.label})
    </span>
  )
}

export function BodyOmissionNotice({ reason, hasBody, ownRequestId }: Props) {
  const notice = describeOmission(reason, hasBody, !!ownRequestId)
  if (!notice || notice.partial) return null
  return (
    <div className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-warning">
      <span className="font-medium">{notice.label}</span> — {notice.detail}
      {notice.seeOwnTrace && ownRequestId && (
        <>
          {" "}
          <RouterLink
            to="/debug/traces/$requestId"
            params={{ requestId: ownRequestId }}
            className="font-mono underline hover:no-underline"
          >
            View it
          </RouterLink>
        </>
      )}
    </div>
  )
}
