/**
 * DockerHealthPanel — Docker connectivity overview for the Metrics & Health
 * page. Shows per-service connection status, last event, and a warning when
 * any service is disconnected.
 */
import { useQuery } from "@tanstack/react-query"
import { healthQueryOptions } from "@/hooks/use-health"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { sectionLabel } from "@/lib/typography"
import { AlertCircle } from "lucide-react"
import type { DockerServiceHealth } from "@/types/common"

function healthBadge(svc: DockerServiceHealth) {
  if (svc.connected) return <Badge variant="success">Connected</Badge>
  return <Badge variant="danger">Disconnected</Badge>
}

export function DockerHealthPanel() {
  const { data: health } = useQuery(healthQueryOptions)
  const docker = health?.docker
  if (!docker || docker.services.length === 0) return null

  const disconnected = docker.services.filter((s) => !s.connected)

  return (
    <div className="flex flex-col gap-3">
      <h2 className={cn(sectionLabel, "text-fg-muted")}>Docker</h2>

      {disconnected.length > 0 && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-lg border border-amber-400/30 bg-amber-400/5 p-3 text-sm text-amber-300"
        >
          <AlertCircle size={16} className="mt-0.5 shrink-0" />
          <div>
            <p className="font-medium">
              {docker.available
                ? `${disconnected.length} service${disconnected.length > 1 ? "s" : ""} disconnected`
                : "Docker is not available"}
            </p>
            <p className="text-xs text-amber-300/70">
              Disconnected services operate in metadata-only mode — resources have no
              running containers.
            </p>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-border">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border bg-bg-muted/50">
              <th className="px-3 py-2 text-left font-medium text-fg-muted">Service</th>
              <th className="px-3 py-2 text-left font-medium text-fg-muted">Status</th>
              <th className="hidden px-3 py-2 text-left font-medium text-fg-muted sm:table-cell">
                Last Event
              </th>
            </tr>
          </thead>
          <tbody>
            {docker.services.map((svc) => (
              <tr key={svc.service} className="border-b border-border last:border-0">
                <td className="px-3 py-2 font-mono uppercase">{svc.service}</td>
                <td className="px-3 py-2">{healthBadge(svc)}</td>
                <td className="hidden px-3 py-2 text-fg-muted sm:table-cell">
                  {docker.lastEvent ? (
                    <span className="font-mono text-[10px]">{docker.lastEvent}</span>
                  ) : (
                    <span className="text-fg-subtle">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {docker.lastEventAt && (
        <p className="text-xs text-fg-subtle">
          Last Docker event at{" "}
          {new Date(docker.lastEventAt).toLocaleTimeString(undefined, {
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
          })}
        </p>
      )}
    </div>
  )
}
