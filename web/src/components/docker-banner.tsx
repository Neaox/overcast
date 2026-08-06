/**
 * DockerBanner — shown at the top of Docker-backed resource pages when
 * Docker is not available. Tells the user that resources shown are
 * metadata-only and have no running containers.
 */
import { useQuery } from "@tanstack/react-query"
import { healthQueryOptions } from "@/hooks/use-health"
import { Boxes, AlertCircle } from "lucide-react"
import { Link } from "@tanstack/react-router"

const DOCKER_SERVICES = new Set([
  "ecs", "rds", "elasticache", "msk", "lambda", "eks", "efs",
])

interface DockerBannerProps {
  /** Service name to check against Docker health. */
  forService: string
}

export function DockerBanner({ forService }: DockerBannerProps) {
  if (!DOCKER_SERVICES.has(forService)) return null

  const { data: health } = useQuery({ ...healthQueryOptions, staleTime: 30000 })
  const docker = health?.docker
  if (!docker) return null

  const svc = docker.services.find((s) => s.service === forService)
  if (!svc || svc.connected) return null

  return (
    <div
      role="alert"
      className="flex flex-col gap-2 rounded-lg border border-amber-400/30 bg-amber-400/5 p-4 text-amber-300 sm:flex-row sm:items-start sm:gap-3"
    >
      <AlertCircle size={18} className="mt-0.5 shrink-0" />
      <div className="flex flex-col gap-1">
        <p className="text-sm font-medium">
          Docker is not available for{" "}
          <span className="font-mono uppercase">{forService}</span>
        </p>
        <p className="text-xs text-amber-300/70">
          Resources shown are metadata-only and have no running containers.
          {svc.error ? ` ${svc.error}` : ""}
        </p>
      </div>
      <Link
        to="/events"
        className="mt-1 flex shrink-0 items-center gap-1.5 self-start rounded-md border border-amber-400/30 px-3 py-1.5 text-xs font-medium text-amber-300 transition-colors hover:bg-amber-400/10 sm:ml-auto sm:mt-0"
      >
        <Boxes size={14} />
        View Docker events
      </Link>
    </div>
  )
}
