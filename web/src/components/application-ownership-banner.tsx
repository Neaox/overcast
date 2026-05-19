/**
 * ApplicationOwnershipBanner — shows "this resource belongs to application X"
 * with a link to the application detail page, when any of the supplied
 * candidate keys is claimed by an AppRegistry application.
 *
 * Pass every identifier you have on hand — ARN, physical ID, bare name —
 * and the hook tries each in order. Renders nothing when no candidate
 * matches, so it's safe to drop on any resource detail page unconditionally.
 */

import { Link } from "@tanstack/react-router"
import { Boxes } from "lucide-react"
import { useOwningApplication } from "@/hooks/use-owning-application"

interface Props {
  candidates: (string | undefined)[]
}

export function ApplicationOwnershipBanner({ candidates }: Props) {
  const { app } = useOwningApplication(candidates)
  if (!app) return null

  return (
    <div className="flex items-center gap-3 rounded-md border border-cyan-300/30 bg-cyan-300/10 px-4 py-2.5 text-sm">
      <Boxes className="h-4 w-4 shrink-0 text-cyan-300" />
      <span className="text-fg">
        This resource belongs to application{" "}
        <Link
          to="/applications/$applicationId"
          params={{ applicationId: app.id }}
          className="font-medium text-cyan-300 hover:underline"
        >
          {app.name}
        </Link>
        . Click to manage it.
      </span>
    </div>
  )
}
