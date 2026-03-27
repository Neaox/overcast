import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { HardDrive, MessagesSquare, Database, Bell, Zap, type LucideIcon } from "lucide-react"
import { cn } from "@/lib/utils"
import { api } from "@/services/api"
import { Spinner } from "@/components/ui/primitives"

interface ServiceCardDef {
  name: string
  label: string
  to: string
  icon: LucideIcon
  color: string
  bgColor: string
  description: string
}

const ALL_SERVICES: ServiceCardDef[] = [
  {
    name: "s3",
    label: "S3",
    to: "/s3",
    icon: HardDrive,
    color: "text-orange-400",
    bgColor: "bg-orange-400/10",
    description: "Object storage — buckets, upload, download, and browse files.",
  },
  {
    name: "sqs",
    label: "SQS",
    to: "/sqs",
    icon: MessagesSquare,
    color: "text-yellow-400",
    bgColor: "bg-yellow-400/10",
    description: "Message queues — send, receive, and inspect messages.",
  },
  {
    name: "dynamodb",
    label: "DynamoDB",
    to: "/dynamodb",
    icon: Database,
    color: "text-blue-400",
    bgColor: "bg-blue-400/10",
    description: "NoSQL tables — manage tables, browse items, and run queries.",
  },
  {
    name: "sns",
    label: "SNS",
    to: "/sns",
    icon: Bell,
    color: "text-pink-400",
    bgColor: "bg-pink-400/10",
    description: "Pub/sub notifications — topics, subscriptions, and publishing.",
  },
  {
    name: "lambda",
    label: "Lambda",
    to: "/lambda",
    icon: Zap,
    color: "text-purple-400",
    bgColor: "bg-purple-400/10",
    description: "Serverless functions — deploy, invoke, and view logs.",
  },
]

export function Dashboard() {
  const { data, isLoading, isError } = useQuery({
    queryKey: ["health"],
    queryFn: () => api.health.check(),
    staleTime: 30_000,
    retry: 2,
  })

  const enabledSet = new Set(data?.services ?? [])

  return (
    <div className="mx-auto max-w-4xl py-8">
      <div className="mb-8">
        <h1 className="text-2xl font-semibold text-fg">Dashboard</h1>
        <p className="mt-1 text-sm text-fg-muted">
          AWS service emulator &mdash; select a service to get started.
        </p>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center py-20">
          <Spinner className="h-6 w-6" />
        </div>
      ) : isError ? (
        <ServiceGrid services={ALL_SERVICES} enabledServices={null} />
      ) : (
        <ServiceGrid services={ALL_SERVICES} enabledServices={enabledSet} />
      )}

      {data && (
        <p className="mt-6 text-center text-xs text-fg-subtle">
          Emulator {data.version} &middot; {enabledSet.size} of {ALL_SERVICES.length} services
          enabled
        </p>
      )}
    </div>
  )
}

function ServiceGrid({
  services,
  enabledServices,
}: {
  services: ServiceCardDef[]
  /** null = unknown (show all as enabled) */
  enabledServices: Set<string> | null
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {services.map((svc) => {
        const enabled = enabledServices === null || enabledServices.has(svc.name)
        return <ServiceCard key={svc.name} service={svc} enabled={enabled} />
      })}
    </div>
  )
}

function ServiceCard({ service, enabled }: { service: ServiceCardDef; enabled: boolean }) {
  const Icon = service.icon

  const card = (
    <div
      className={cn(
        "group relative flex flex-col gap-3 rounded-xl border p-5 transition-all",
        enabled
          ? "border-border bg-bg-elevated shadow-sm hover:border-accent/40 hover:shadow-md"
          : "cursor-not-allowed border-border-muted bg-bg-muted opacity-50",
      )}
    >
      <div className={cn("flex h-10 w-10 items-center justify-center rounded-lg", service.bgColor)}>
        <Icon className={cn("h-5 w-5", enabled ? service.color : "text-fg-subtle")} />
      </div>
      <div>
        <h3 className={cn("text-sm font-semibold", enabled ? "text-fg" : "text-fg-subtle")}>
          {service.label}
        </h3>
        <p
          className={cn(
            "mt-1 text-xs leading-relaxed",
            enabled ? "text-fg-muted" : "text-fg-subtle",
          )}
        >
          {service.description}
        </p>
      </div>
      {!enabled && (
        <span className="absolute top-3 right-3 rounded-full border border-border-muted px-2 py-0.5 text-[10px] font-medium text-fg-subtle">
          Disabled
        </span>
      )}
    </div>
  )

  if (!enabled) return card

  return (
    <Link to={service.to} className="rounded-xl focus-visible:outline-accent">
      {card}
    </Link>
  )
}
