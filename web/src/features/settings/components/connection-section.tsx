import { useEndpoint } from "@/hooks/use-endpoint"
import { isEndpointConfigurable } from "@/services/discovery"
import { ConnectionForm } from "@/components/layout/connection-dialog"
import { useToast } from "@/components/ui/toast"
import { Code } from "@/components/ui/primitives"

/**
 * Settings → Connection.
 *
 * The same form the first-run dialog and the header's plug modal show
 * (ConnectionForm, including its live reachability probe), editing the active
 * endpoint in place. All three render the one component, so the settings page
 * and the quick edit can never drift apart.
 *
 * Bundled builds derive the endpoint from `window.location`, so there is
 * nothing to configure; the section states that instead of offering fields
 * that could not stick.
 */
export function ConnectionSection() {
  const endpoint = useEndpoint()
  const { toast } = useToast()

  if (!isEndpointConfigurable()) {
    return (
      <div className="flex flex-col gap-2 text-[13px] text-fg-muted">
        <p>
          Connected to <Code>{endpoint.baseUrl}</Code> — derived from this page&apos;s own
          address, so it always points at the daemon that served the console.
        </p>
        <p>The active region is changeable from the header on any page.</p>
      </div>
    )
  }

  return (
    <ConnectionForm
      // ConnectionForm seeds itself from the active endpoint once, on mount.
      // Remount when the endpoint changes elsewhere (e.g. another tab) so the
      // form always starts from the active values.
      key={`${endpoint.baseUrl}|${endpoint.region}|${endpoint.label ?? ""}`}
      submitLabel="Save connection"
      submittingLabel="Saving…"
      onSubmitted={() => toast({ title: "Connection updated", variant: "success" })}
    />
  )
}
