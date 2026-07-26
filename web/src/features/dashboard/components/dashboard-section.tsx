import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

export type SectionTone = "strong" | "muted" | "subtle"

const TONE_CLASS: Record<SectionTone, string> = {
  strong: "text-fg",
  muted: "text-fg-muted",
  subtle: "text-fg-subtle",
}

export function DashboardSection({
  title,
  count,
  note,
  tone = "subtle",
  children,
}: {
  title: string
  count: number
  note?: string
  tone?: SectionTone
  children: ReactNode
}) {
  return (
    <section aria-label={title} className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <span className={cn("font-mono text-[10px] tracking-[0.16em] uppercase", TONE_CLASS[tone])}>
          {title}
        </span>
        <span className="font-mono text-[10px] text-fg-subtle">{count}</span>
        <span className="h-px flex-1 bg-border" />
        {note && <span className="font-mono text-[10px] text-fg-subtle">{note}</span>}
      </div>
      {children}
    </section>
  )
}
