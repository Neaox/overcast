import { useState } from "react"
import { Link, useRouterState } from "@tanstack/react-router"
import {
  HardDrive,
  MessagesSquare,
  Database,
  Bell,
  Zap,
  ChevronLeft,
  ChevronRight,
  Cloud,
  LayoutDashboard,
  Activity,
} from "lucide-react"
import { cn } from "@/lib/utils"

const NAV_ITEMS = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, color: "text-fg-muted" },
  { to: "/s3", label: "S3", icon: HardDrive, color: "text-orange-400" },
  { to: "/sqs", label: "SQS", icon: MessagesSquare, color: "text-yellow-400" },
  { to: "/dynamodb", label: "DynamoDB", icon: Database, color: "text-blue-400" },
  { to: "/sns", label: "SNS", icon: Bell, color: "text-pink-400" },
  { to: "/lambda", label: "Lambda", icon: Zap, color: "text-purple-400" },
  { to: "/events", label: "Events", icon: Activity, color: "text-teal-400" },
]

export function Sidebar() {
  const [collapsed, setCollapsed] = useState(false)
  const router = useRouterState()
  const pathname = router.location.pathname

  return (
    <aside
      className={cn(
        "flex shrink-0 flex-col border-r border-sidebar-bg/50 bg-sidebar-bg transition-all duration-200",
        collapsed ? "w-14" : "w-52",
      )}
    >
      {/* Logo */}
      <div
        className={cn(
          "flex h-12 shrink-0 items-center gap-2.5 border-b border-sidebar-fg/10 px-3",
          collapsed && "justify-center px-0",
        )}
      >
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-accent">
          <Cloud className="h-4 w-4 text-fg-on-accent" />
        </div>
        {!collapsed && (
          <span className="text-sm font-semibold tracking-tight text-sidebar-fg-strong">
            Overcast
          </span>
        )}
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto py-2">
        {NAV_ITEMS.map(({ to, label, icon: Icon, color }) => {
          const active = to === "/" ? pathname === "/" : pathname.startsWith(to)
          return (
            <Link
              key={to}
              to={to}
              className={cn(
                "mx-2 my-0.5 flex h-8 items-center gap-2.5 rounded-md px-2 text-sm font-medium transition-colors",
                active
                  ? "bg-sidebar-item-active text-sidebar-item-active-fg"
                  : "text-sidebar-fg hover:bg-sidebar-item-hover hover:text-sidebar-fg-strong",
                collapsed && "mx-1 justify-center px-0",
              )}
              title={collapsed ? label : undefined}
            >
              <Icon className={cn("h-4 w-4 shrink-0", active ? "text-fg-on-accent" : color)} />
              {!collapsed && <span>{label}</span>}
            </Link>
          )
        })}
      </nav>

      {/* Collapse toggle */}
      <button
        onClick={() => setCollapsed((v) => !v)}
        className={cn(
          "flex h-9 items-center justify-center border-t border-sidebar-fg/10",
          "text-sidebar-fg transition-colors hover:bg-sidebar-item-hover hover:text-sidebar-fg-strong",
          "gap-1.5 text-sm",
        )}
        title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      >
        {collapsed ? (
          <ChevronRight className="h-3.5 w-3.5" />
        ) : (
          <>
            <ChevronLeft className="h-3.5 w-3.5" />
            <span>Collapse</span>
          </>
        )}
      </button>
    </aside>
  )
}
