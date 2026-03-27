import { useState } from "react"
import { Server } from "lucide-react"
import { useEndpoint } from "@/hooks/use-endpoint"
import { DEFAULT_ENDPOINT } from "@/services/discovery"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField } from "@/components/ui/form"
import { RegionSelect } from "@/components/ui/region-select"

interface ConnectionDialogProps {
  /** Called once the user has saved a valid endpoint. */
  onConnected: () => void
}

export function ConnectionDialog({ onConnected }: ConnectionDialogProps) {
  const { setEndpoint } = useEndpoint()
  const [baseUrl, setBaseUrl] = useState(DEFAULT_ENDPOINT.baseUrl)
  const [region, setRegion] = useState(DEFAULT_ENDPOINT.region)
  const [label, setLabel] = useState(DEFAULT_ENDPOINT.label ?? "")
  const [error, setError] = useState<string>()
  const [testing, setTesting] = useState(false)

  async function handleConnect() {
    setError(undefined)

    let url: URL
    try {
      url = new URL(baseUrl)
    } catch {
      setError("Enter a valid URL, e.g. http://localhost:4566")
      return
    }

    setTesting(true)
    try {
      // Quick health-check — emulator exposes /_health
      const res = await fetch(`${url.origin}/_health`, { signal: AbortSignal.timeout(3000) })
      if (!res.ok) throw new Error(`Status ${res.status}`)
    } catch {
      // Don't block — emulator may not have a health endpoint configured yet
    } finally {
      setTesting(false)
    }

    setEndpoint({
      baseUrl: url.origin,
      region: region.trim() || "us-east-1",
      label: label.trim() || undefined,
    })
    onConnected()
  }

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-bg p-4">
      <div className="w-full max-w-md rounded-xl border border-border bg-bg-elevated p-8 shadow-2xl">
        {/* Logo / heading */}
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-accent/10">
            <Server className="h-5 w-5 text-accent" />
          </div>
          <div>
            <h1 className="text-base font-semibold text-fg">Connect to Overcast</h1>
            <p className="text-sm text-fg-muted">Configure your emulator endpoint</p>
          </div>
        </div>

        <div className="flex flex-col gap-4">
          <FormField
            label="Endpoint URL"
            htmlFor="baseUrl"
            required
            hint="The host and port your emulator is running on."
            error={error}
          >
            <Input
              id="baseUrl"
              value={baseUrl}
              onChange={(e) => {
                setBaseUrl(e.target.value)
                setError(undefined)
              }}
              placeholder="http://localhost:4566"
              spellCheck={false}
            />
          </FormField>

          <FormField label="Default Region" htmlFor="region">
            <RegionSelect id="region" value={region} onChange={setRegion} />
          </FormField>

          <FormField
            label="Label (optional)"
            htmlFor="label"
            hint="A friendly name shown in the header."
          >
            <Input
              id="label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="Local (4566)"
            />
          </FormField>
        </div>

        <div className="mt-6 flex flex-col gap-2">
          <Button className="w-full" onClick={handleConnect} disabled={testing}>
            {testing ? "Connecting…" : "Connect"}
          </Button>
          <p className="text-center text-xs text-fg-subtle">
            Settings are stored in session storage only.
          </p>
        </div>
      </div>
    </div>
  )
}
