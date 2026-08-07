import { useEffect, useRef, useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useQuery } from "@tanstack/react-query"
import { z } from "zod"
import { AlertTriangle, Check, CircleX, Loader2, Server } from "lucide-react"
import { endpointStore } from "@/services/endpoint-store"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Combobox } from "@/components/ui/combobox"
import { FormField, fieldError } from "@/components/ui/form"
import { RegionSelect } from "@/components/ui/region-select"
import { Tooltip } from "@/components/ui/tooltip"

const ENDPOINT_SUGGESTIONS = ["http://localhost:", "http://localhost.overcast.sh:"]
const PROBE_DEBOUNCE_MS = 400

function useProbeUrl(rawUrl: string): string {
  const [debounced, setDebounced] = useState("")
  const timerRef = useRef<ReturnType<typeof setTimeout>>(null)

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => setDebounced(rawUrl), PROBE_DEBOUNCE_MS)
    return () => { if (timerRef.current) clearTimeout(timerRef.current) }
  }, [rawUrl])

  return debounced
}

const connectionSchema = z.object({
  baseUrl: z.string().min(1, "URL is required").refine((v) => { try { new URL(v); return true } catch { return false } }, "Enter a valid URL, e.g. http://localhost:4566"),
  region: z.string(),
  label: z.string(),
})

type ValidationState = { kind: "idle" } | { kind: "checking" } | { kind: "valid"; version: string } | { kind: "invalid"; reason: string }

function ValidationIcon({ state }: { state: ValidationState }) {
  switch (state.kind) {
    case "checking": return (<Tooltip content="Checking endpoint…" side="top"><Loader2 className="h-4 w-4 animate-spin text-fg-muted" /></Tooltip>)
    case "valid": return (<Tooltip content={`Overcast ${state.version} — reachable`} side="top"><Check className="h-4 w-4 text-success" /></Tooltip>)
    case "invalid": return (<Tooltip content={state.reason} side="top"><CircleX className="h-4 w-4 text-danger" /></Tooltip>)
    default: return null
  }
}

function useEndpointValidation(baseUrl: string): ValidationState {
  const urlOk = (() => {
    if (!baseUrl) return false
    try {
      const u = new URL(baseUrl)
      return u.protocol === "http:" || u.protocol === "https:"
    } catch { return false }
  })()
  const hasPort = (() => {
    if (!baseUrl) return false
    try { return !!new URL(baseUrl).port } catch { return false }
  })()

  const probeUrl = useProbeUrl(baseUrl)

  const { data, isLoading } = useQuery({
    queryKey: ["endpoint-validate", probeUrl],
    queryFn: async () => {
      try {
        const res = await fetch(`${probeUrl}/_/info`)
        if (!res.ok) return { ok: false as const, error: `HTTP ${res.status}` }
        const json = (await res.json()) as { version?: string }
        if (!json.version) return { ok: false as const, error: "Not an Overcast endpoint" }
        return { ok: true as const, version: json.version }
      } catch (e) { return { ok: false as const, error: e instanceof Error ? e.message : "Unreachable" } }
    },
    enabled: urlOk && hasPort && probeUrl.length > 0,
    staleTime: 0,
    retry: 0,
    refetchInterval: 5000,
    refetchOnWindowFocus: false,
  })

  if (!baseUrl) return { kind: "idle" }
  if (!urlOk) return { kind: "invalid", reason: "Invalid URL" }
  if (!hasPort) return { kind: "invalid", reason: "Port number is required" }
  if (isLoading) return { kind: "checking" }
  if (data?.ok) return { kind: "valid", version: data.version }
  if (data) return { kind: "invalid", reason: data.error }
  return { kind: "idle" }
}

export function ConnectionDialogContent() {
  const inDocker = typeof window !== "undefined" && window.__OVERCAST__?.inDocker === true
  const endpointUnknown = typeof window !== "undefined" && window.__OVERCAST__?.endpointKnown === false
  // Seed from the active endpoint, not DEFAULT_ENDPOINT: a user reopening the
  // dialog must see their configured endpoint, or clicking Connect would
  // silently revert it to the default.
  const [initial] = useState(() => endpointStore.get())
  const [baseUrlDraft, setBaseUrlDraft] = useState(initial.baseUrl)
  const validation = useEndpointValidation(baseUrlDraft)
  const labelPlaceholder = (() => { try { return new URL(baseUrlDraft).host } catch { return "Local (4566)" } })()
  const form = useForm({
    validators: { onChange: connectionSchema },
    defaultValues: { baseUrl: initial.baseUrl, region: initial.region, label: initial.label ?? "" },
    onSubmit: ({ value }) => {
      const url = new URL(value.baseUrl)
      endpointStore.set(
        { baseUrl: url.origin, region: value.region.trim() || "us-east-1", label: value.label.trim() || undefined },
        { explicit: true },
      )
    },
  })
  return (
    <div className="w-full max-w-md rounded-xl border border-border bg-bg-elevated p-8 shadow-2xl">
      <div className="mb-6 flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-accent/10"><Server className="h-5 w-5 text-accent" /></div>
        <div><h1 className="font-mono text-base font-semibold text-fg">Connect to Overcast</h1><p className="text-sm text-fg-muted">Configure your emulator endpoint</p></div>
      </div>
      {inDocker && endpointUnknown && (
        <div className="mb-5 flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
          <p className="text-sm text-fg-muted">Running inside Docker with remapped ports, but the Docker socket is not mounted. Overcast cannot determine the host API port automatically. Mount the socket (<code className="rounded bg-bg px-1 font-mono text-xs">-v /var/run/docker.sock:/var/run/docker.sock</code>) for automatic detection, or enter the API endpoint manually below.</p>
        </div>
      )}
      <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); e.stopPropagation(); void form.handleSubmit() }}>
        <form.Field name="baseUrl" validators={{ onChange: connectionSchema.shape.baseUrl }}>
          {(field) => (
            <FormField label="Endpoint URL" htmlFor="baseUrl" required hint="The host and port your emulator is running on." error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}>
              <div className="relative">
                <Combobox<{ value: string }>
                  value={field.state.value}
                  onChange={(v) => { field.handleChange(v); setBaseUrlDraft(v) }}
                  items={ENDPOINT_SUGGESTIONS.map((s) => ({ value: s }))}
                  filterFn={(item, q) => item.value.includes(q)}
                  getItemValue={(item) => item.value}
                  renderItem={(item) => (<span className="font-mono text-xs">{item.value}</span>)}
                  allowCustom
                  allowFreeText
                  placeholder="http://localhost:4566"
                  popoverWidth="w-full"
                />
                <div className="absolute right-8 top-1/2 -translate-y-1/2"><ValidationIcon state={validation} /></div>
              </div>
            </FormField>
          )}
        </form.Field>
        <form.Field name="region">{(field) => (<FormField label="Default Region" htmlFor="region"><RegionSelect id="region" value={field.state.value} onChange={(v) => field.handleChange(v)} /></FormField>)}</form.Field>
        <form.Field name="label">{(field) => (<FormField label="Label (optional)" htmlFor="label" hint="A friendly name shown in the header."><Input id="label" value={field.state.value} onChange={(e) => field.handleChange(e.target.value)} onBlur={field.handleBlur} placeholder={labelPlaceholder} /></FormField>)}</form.Field>
        <div className="mt-2 flex flex-col gap-2">
          <form.Subscribe selector={(s) => [s.canSubmit, s.isSubmitting]}>{([canSubmit, isSubmitting]) => (<Button type="submit" className="w-full" disabled={!canSubmit}>{isSubmitting ? "Connecting…" : "Connect"}</Button>)}</form.Subscribe>
          <p className="text-center text-xs text-fg-subtle">Settings are stored locally in this browser.</p>
        </div>
      </form>
    </div>
  )
}

export function ConnectionDialog() {
  return (<div className="fixed inset-0 z-50 flex items-center justify-center bg-bg p-4"><ConnectionDialogContent /></div>)
}

export function ConnectionDialogModal({ onClose }: { onClose: () => void }) {
  return (<div className="fixed inset-0 z-50 flex items-center justify-center bg-bg/60 backdrop-blur-sm p-4" onClick={onClose}><div onClick={(e) => e.stopPropagation()}><ConnectionDialogContent /></div></div>)
}
