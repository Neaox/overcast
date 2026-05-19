import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { Server } from "lucide-react"
import { endpointStore } from "@/services/endpoint-store"
import { DEFAULT_ENDPOINT } from "@/services/discovery"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
import { RegionSelect } from "@/components/ui/region-select"

interface ConnectionDialogProps {
  /** Called once the user has saved a valid endpoint. */
  onConnected: () => void
}

const connectionSchema = z.object({
  baseUrl: z
    .string()
    .min(1, "URL is required")
    .refine((v) => {
      try {
        new URL(v)
        return true
      } catch {
        return false
      }
    }, "Enter a valid URL, e.g. http://localhost:4566"),
  region: z.string(),
  label: z.string(),
})

export function ConnectionDialog({ onConnected }: ConnectionDialogProps) {
  const form = useForm({
    validators: { onChange: connectionSchema },
    defaultValues: {
      baseUrl: DEFAULT_ENDPOINT.baseUrl,
      region: DEFAULT_ENDPOINT.region,
      label: DEFAULT_ENDPOINT.label ?? "",
    },
    onSubmit: async ({ value }) => {
      // Quick health-check — emulator exposes /_health
      const url = new URL(value.baseUrl)
      try {
        const res = await fetch(`${url.origin}/_health`, { signal: AbortSignal.timeout(3000) })
        if (!res.ok) throw new Error(`Status ${res.status}`)
      } catch {
        // Don't block — emulator may not have a health endpoint configured yet
      }

      endpointStore.set({
        baseUrl: url.origin,
        region: value.region.trim() || "us-east-1",
        label: value.label.trim() || undefined,
      })
      onConnected()
    },
  })

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

        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          <form.Field name="baseUrl" validators={{ onChange: connectionSchema.shape.baseUrl }}>
            {(field) => (
              <FormField
                label="Endpoint URL"
                htmlFor="baseUrl"
                required
                hint="The host and port your emulator is running on."
                error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
              >
                <Input
                  id="baseUrl"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="http://localhost:4566"
                  spellCheck={false}
                />
              </FormField>
            )}
          </form.Field>

          <form.Field name="region">
            {(field) => (
              <FormField label="Default Region" htmlFor="region">
                <RegionSelect
                  id="region"
                  value={field.state.value}
                  onChange={(v) => field.handleChange(v)}
                />
              </FormField>
            )}
          </form.Field>

          <form.Field name="label">
            {(field) => (
              <FormField
                label="Label (optional)"
                htmlFor="label"
                hint="A friendly name shown in the header."
              >
                <Input
                  id="label"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="Local (4566)"
                />
              </FormField>
            )}
          </form.Field>

          <div className="mt-2 flex flex-col gap-2">
            <form.Subscribe selector={(s) => [s.canSubmit, s.isSubmitting]}>
              {([canSubmit, isSubmitting]) => (
                <Button type="submit" className="w-full" disabled={!canSubmit}>
                  {isSubmitting ? "Connecting…" : "Connect"}
                </Button>
              )}
            </form.Subscribe>
            <p className="text-center text-xs text-fg-subtle">
              Settings are stored in session storage only.
            </p>
          </div>
        </form>
      </div>
    </div>
  )
}
