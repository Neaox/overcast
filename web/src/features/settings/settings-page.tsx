import type { ComponentType } from "react"
import { Lock, Server } from "lucide-react"
import type { LucideIcon } from "lucide-react"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { PageHeader } from "@/components/ui/primitives"
import { ConnectionSection } from "./components/connection-section"
import { HttpsSection } from "./components/https-section"

/**
 * SettingsPage — /settings.
 *
 * Deliberately a registry, not a bespoke page: a future setting is one entry
 * here (id, title, blurb, icon, component) and nothing else. Section ids
 * double as anchor targets, so /settings#https deep-links work.
 */

interface SettingsSectionDef {
  id: string
  title: string
  description: string
  icon: LucideIcon
  Component: ComponentType
}

const SECTIONS: SettingsSectionDef[] = [
  {
    id: "connection",
    title: "Connection",
    description: "Which emulator this console talks to.",
    icon: Server,
    Component: ConnectionSection,
  },
  {
    id: "https",
    title: "HTTPS & certificates",
    description:
      "Serve the console and API over browser-trusted HTTPS — unlocking HTTP/2, which keeps the UI responsive under load.",
    icon: Lock,
    Component: HttpsSection,
  },
]

export function SettingsPage() {
  return (
    <div className="flex flex-col gap-6 p-6">
      <PageHeader title="Settings" description="Console and daemon configuration." />
      <div className="flex max-w-3xl flex-col gap-4">
        {SECTIONS.map(({ id, title, description, icon: Icon, Component }) => (
          <Card key={id} id={id} className="scroll-mt-6">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Icon size={14} className="text-accent" />
                {title}
              </CardTitle>
              <CardDescription>{description}</CardDescription>
            </CardHeader>
            <CardContent>
              <Component />
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
