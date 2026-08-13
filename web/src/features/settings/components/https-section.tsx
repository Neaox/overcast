import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Download,
  Lock,
  RefreshCw,
  ShieldCheck,
} from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { Code, CodeBlock, Spinner } from "@/components/ui/primitives"
import { Tab, TabList, TabPanel, Tabs } from "@/components/ui/tabs"
import { useToast } from "@/components/ui/toast"
import { settings } from "@/services/api"
import type { HttpsStatus, HttpsSetupResult } from "@/services/api/settings"
import { enableHttpsMutationOptions, httpsStatusQueryOptions, settingsKeys } from "../data"

/**
 * Settings → HTTPS & certificates.
 *
 * One flow, environment-aware steps: prepare certificates → trust the CA →
 * restart with OVERCAST_TLS=auto → verify and switch. On a native daemon the
 * first two steps are a single click (the OS shows its approval prompt); in
 * Docker the daemon cannot reach the host's trust store, so step two becomes
 * a copyable host command or a certificate download with instructions. The
 * restart is unavoidable in every environment — the daemon's listeners are
 * single-mode and Overcast is configured via environment variables only.
 */

// ─── HTTPS liveness probe ───────────────────────────────────────────────────

type ProbeState = "idle" | "checking" | "reachable" | "unreachable"

/**
 * Manual-first probe of the https origin: nothing is fetched until the user
 * (or a completed setup) asks, then the check can be repeated. Deliberately
 * not an interval — the interesting moment (daemon restarted with TLS) is
 * user-driven, and the button doubles as the user's "I did the restart".
 */
function useHttpsProbe(httpsOrigin: string) {
  const [state, setState] = useState<ProbeState>("idle")

  const check = async () => {
    setState("checking")
    const ok = await settings.probeHttps(httpsOrigin)
    setState(ok ? "reachable" : "unreachable")
  }

  return { state, check }
}

/**
 * Best-effort client OS from the user agent — only ever used to pick which
 * command tab starts selected, so a wrong guess costs one click. Note the
 * match is on "Windows" (real browsers say "Windows NT ..."), not "win":
 * jsdom's UA says "win32" on Windows hosts, and tests must see the same
 * default tab on every development machine.
 */
type ClientOS = "windows" | "macos" | "linux"

function detectClientOS(): ClientOS {
  const ua = typeof navigator !== "undefined" ? navigator.userAgent : ""
  if (/Windows/.test(ua)) return "windows"
  if (/Macintosh|Mac OS X/.test(ua)) return "macos"
  return "linux"
}

// ─── Small pieces ───────────────────────────────────────────────────────────

function CommandRow({ command }: { command: string }) {
  return (
    <div className="flex items-center gap-2">
      <CodeBlock className="min-w-0 flex-1">{command}</CodeBlock>
      <CopyButton value={command} noun="command" />
    </div>
  )
}

interface CommandTabDef {
  id: string
  label: string
  command: string
  /** Optional context above the command — caveats, distro notes. */
  note?: string
}

/**
 * One command, several dialects — a restart line in bash vs PowerShell, a
 * trust install per OS. The default tab comes from the caller (usually the
 * detected client OS), so most users never touch the tabs at all.
 */
function CommandTabs({ tabs, defaultKey }: { tabs: CommandTabDef[]; defaultKey: string }) {
  const [selected, setSelected] = useState(defaultKey)
  const selectedKey = tabs.some((t) => t.id === selected) ? selected : tabs[0].id
  return (
    <Tabs selectedKey={selectedKey} onSelectionChange={setSelected}>
      <TabList>
        {tabs.map((t) => (
          <Tab key={t.id} id={t.id}>
            {t.label}
          </Tab>
        ))}
      </TabList>
      {tabs.map((t) => (
        <TabPanel key={t.id} id={t.id} className="pt-2">
          <div className="flex flex-col gap-2">
            {t.note && <p className="text-[13px] text-fg-muted">{t.note}</p>}
            <CommandRow command={t.command} />
          </div>
        </TabPanel>
      ))}
    </Tabs>
  )
}

function StatusRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-[13px] text-fg-muted">{label}</span>
      <span className="flex items-center gap-2">{children}</span>
    </div>
  )
}

function StepHeading({ n, title }: { n: number; title: string }) {
  return (
    <div className="flex items-center gap-2">
      <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent/10 font-mono text-[11px] font-bold text-accent">
        {n}
      </span>
      <h4 className="font-mono text-[13px] font-semibold text-fg">{title}</h4>
    </div>
  )
}

function trustBadge(status: HttpsStatus) {
  switch (status.trustStore) {
    case "installed":
      return <Badge variant="success">trusted</Badge>
    case "not_installed":
      return <Badge variant="warning">not trusted yet</Badge>
    case "unavailable":
      return <Badge variant="warning">manual install required</Badge>
    default:
      return <Badge>managed from the host</Badge>
  }
}

// ─── Switch-to-https affordance ─────────────────────────────────────────────

function SwitchToHttps({
  httpsOrigin,
  probe,
  hint,
}: {
  httpsOrigin: string
  probe: ReturnType<typeof useHttpsProbe>
  hint: string
}) {
  if (probe.state === "reachable") {
    return (
      <div className="flex flex-col gap-2">
        <p className="flex items-center gap-1.5 text-[13px] text-success">
          <CheckCircle2 size={14} /> HTTPS is answering at{" "}
          <Code className="text-success">{httpsOrigin}</Code>
        </p>
        <Button
          className="self-start"
          onClick={() => {
            window.location.href = `${httpsOrigin}/settings`
          }}
        >
          Switch to HTTPS <ArrowRight size={14} />
        </Button>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-2">
      <p className="text-[13px] text-fg-muted">{hint}</p>
      {probe.state === "unreachable" && (
        <p className="flex items-center gap-1.5 text-[13px] text-warning">
          <AlertCircle size={14} />
          Not answering over HTTPS yet — restart the daemon with TLS on, then check again.
        </p>
      )}
      <Button
        variant="secondary"
        className="self-start"
        busy={probe.state === "checking"}
        busyLabel="Checking"
        onClick={() => void probe.check()}
      >
        <RefreshCw size={14} /> Check HTTPS availability
      </Button>
    </div>
  )
}

// ─── The section ────────────────────────────────────────────────────────────

export function HttpsSection() {
  const qc = useQueryClient()
  const { toast } = useToast()
  const statusQuery = useQuery(httpsStatusQueryOptions())
  const pageIsHttps = window.location.protocol === "https:"
  const httpsOrigin = `https://${window.location.host}`
  const probe = useHttpsProbe(httpsOrigin)
  const clientOS = detectClientOS()

  const enable = useMutation({
    ...enableHttpsMutationOptions(),
    onSuccess: (result: HttpsSetupResult) => {
      void qc.invalidateQueries({ queryKey: settingsKeys.https() })
      toast({
        title: result.trustInstalled
          ? "HTTPS is set up on this machine"
          : "Certificates are ready",
        description: result.restartRequired
          ? "One step left: restart Overcast with OVERCAST_TLS=auto."
          : undefined,
        variant: "success",
      })
    },
    onError: (error: Error) => {
      toast({ title: "HTTPS setup failed", description: error.message, variant: "danger" })
    },
  })

  const downloadCert = async () => {
    try {
      const blob = await settings.fetchCACert()
      const url = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = url
      a.download = "overcast-ca.pem"
      a.click()
      URL.revokeObjectURL(url)
    } catch (error) {
      toast({
        title: "Certificate download failed",
        description: error instanceof Error ? error.message : String(error),
        variant: "danger",
      })
    }
  }

  if (statusQuery.isPending) {
    return (
      <div className="flex items-center gap-2 p-2 text-sm text-fg-muted">
        <Spinner /> Checking HTTPS status…
      </div>
    )
  }
  if (statusQuery.isError) {
    return (
      <p role="alert" className="flex items-center gap-1.5 text-[13px] text-danger">
        <AlertCircle size={14} /> Could not read the HTTPS status: {statusQuery.error.message}
      </p>
    )
  }

  const status = statusQuery.data
  const prepared = enable.data
  const restartCommand =
    prepared?.restartCommand ?? status.restartCommand ?? "OVERCAST_TLS=auto overcast serve"
  const hostCommand = prepared?.hostCommand ?? status.hostCommand
  // "One click does everything" is only honest when this daemon can reach the
  // trust store that matters: native, with a working platform backend.
  const oneClickTrust = !status.inContainer && status.trustStore !== "unavailable"
  const trustDone = status.trustStore === "installed" || prepared?.trustInstalled === true
  const certsDone = status.caExists || prepared?.caReady === true

  return (
    <div className="flex flex-col gap-5">
      {/* Current state, always visible. */}
      <div className="flex flex-col gap-2 rounded-md border border-border bg-bg p-3">
        <StatusRow label="Serving">
          {status.mode === "off" ? (
            <Badge variant="warning">plain HTTP</Badge>
          ) : (
            <Badge variant="success">
              <Lock size={11} className="mr-1" />
              HTTPS {status.mode === "auto" ? "(overcast CA)" : "(custom certificate)"}
            </Badge>
          )}
        </StatusRow>
        <StatusRow label="Certificates">
          {certsDone ? <Badge variant="success">ready</Badge> : <Badge>not created yet</Badge>}
        </StatusRow>
        {status.mode !== "explicit" && (
          <StatusRow label="Browser trust">{trustBadge(status)}</StatusRow>
        )}
        {status.inContainer && (
          <StatusRow label="Environment">
            <Badge>Docker container</Badge>
          </StatusRow>
        )}
      </div>

      {status.mode === "explicit" ? (
        <p className="text-[13px] text-fg-muted">
          Overcast is serving a certificate you configured via{" "}
          <Code>OVERCAST_TLS_CERT</Code> / <Code>OVERCAST_TLS_KEY</Code>, so trust management
          is in your hands. Nothing to do here.
        </p>
      ) : status.mode !== "off" && pageIsHttps ? (
        <div className="flex flex-col gap-3">
          <p className="flex items-center gap-1.5 text-[13px] text-success">
            <ShieldCheck size={14} /> You are using HTTPS with HTTP/2 — streams and uploads no
            longer compete for the browser&apos;s six-connection limit.
          </p>
          {!trustDone && oneClickTrust && (
            <div className="flex flex-col gap-2">
              <p className="text-[13px] text-fg-muted">
                This browser reached the page, but the overcast CA is not in the system trust
                store (you may have clicked through a warning). Install it to make the warning
                go away everywhere:
              </p>
              <Button
                className="self-start"
                busy={enable.isPending}
                busyLabel="Waiting for OS approval"
                onClick={() => enable.mutate()}
              >
                <ShieldCheck size={14} /> Install certificate trust
              </Button>
            </div>
          )}
          {!trustDone && !oneClickTrust && hostCommand && (
            <div className="flex flex-col gap-2">
              <p className="text-[13px] text-fg-muted">
                To make browsers and SDKs on this machine trust the daemon, run this on the
                host:
              </p>
              <CommandRow command={hostCommand} />
            </div>
          )}
        </div>
      ) : status.mode !== "off" ? (
        // Serving HTTPS, but this page is on plain HTTP (e.g. the dev server).
        <SwitchToHttps
          httpsOrigin={httpsOrigin}
          probe={probe}
          hint="The daemon is already serving HTTPS — this page just isn't using it."
        />
      ) : (
        // mode === "off": the guided enablement flow.
        <div className="flex flex-col gap-5">
          <p className="text-[13px] text-fg-muted">
            HTTPS unlocks HTTP/2 for the console, which keeps the UI responsive while event
            streams and Lambda invocations are running. Plain HTTP keeps working for SDKs and
            the CLI until you switch.
          </p>

          {/* Step 1 — certificates (+ trust, when we can do it from here). */}
          <div className="flex flex-col gap-2.5">
            <StepHeading n={1} title={oneClickTrust ? "Prepare certificates & trust" : "Prepare certificates"} />
            {trustDone && certsDone ? (
              <p className="flex items-center gap-1.5 text-[13px] text-success">
                <CheckCircle2 size={14} /> Done — certificates exist and the CA is trusted.
              </p>
            ) : oneClickTrust ? (
              <div className="flex flex-col gap-2">
                <p className="text-[13px] text-fg-muted">
                  Creates the local certificate authority, mints the server certificate, and
                  installs the CA into your system trust store. Your OS will ask you to
                  approve — that prompt is the only manual part.
                </p>
                <Button
                  className="self-start"
                  busy={enable.isPending}
                  busyLabel="Waiting for OS approval"
                  onClick={() => enable.mutate()}
                >
                  <ShieldCheck size={14} /> Enable HTTPS
                </Button>
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                {certsDone ? (
                  <p className="flex items-center gap-1.5 text-[13px] text-success">
                    <CheckCircle2 size={14} /> Certificates are ready.
                  </p>
                ) : (
                  <>
                    <p className="text-[13px] text-fg-muted">
                      Creates the certificate authority and server certificate inside the
                      container (persisted under <Code>/data</Code> — keep that on a volume,
                      or every recreation mints a fresh CA).
                    </p>
                    <Button
                      className="self-start"
                      busy={enable.isPending}
                      busyLabel="Preparing"
                      onClick={() => enable.mutate()}
                    >
                      Prepare certificates
                    </Button>
                  </>
                )}
              </div>
            )}
          </div>

          {/* Step 2 — trust, host-side, only when this daemon can't do it. */}
          {!oneClickTrust && (
            <div className="flex flex-col gap-2.5">
              <StepHeading n={2} title="Trust the certificate on this machine" />
              <p className="text-[13px] text-fg-muted">
                With the overcast CLI installed on the host, one command fetches and installs
                the CA:
              </p>
              {hostCommand && <CommandRow command={hostCommand} />}
              <p className="text-[13px] text-fg-muted">
                No CLI on the host? Download the CA certificate, then install it from the
                folder it landed in:
              </p>
              <Button
                variant="secondary"
                className="self-start"
                disabled={!certsDone}
                title={certsDone ? undefined : "Prepare certificates first"}
                onClick={() => void downloadCert()}
              >
                <Download size={14} /> Download CA certificate
              </Button>
              <CommandTabs
                defaultKey={clientOS}
                tabs={[
                  {
                    id: "windows",
                    label: "Windows",
                    note: "Approve the certificate-store confirmation dialog when it appears.",
                    command: "certutil -user -addstore Root overcast-ca.pem",
                  },
                  {
                    id: "macos",
                    label: "macOS",
                    note: "Installs into your login keychain — authorise the prompt.",
                    command:
                      "security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db overcast-ca.pem",
                  },
                  {
                    id: "linux",
                    label: "Linux",
                    note: "Debian/Ubuntu/Alpine shown — Fedora, Arch, and the Firefox/Chromium NSS store are covered in the guide below.",
                    command:
                      "sudo cp overcast-ca.pem /usr/local/share/ca-certificates/overcast-local-ca.crt && sudo update-ca-certificates",
                  },
                ]}
              />
              <p className="text-[13px] text-fg-muted">
                The{" "}
                <a
                  className="text-accent hover:underline"
                  href="/docs?path=https.md"
                  target="_blank"
                  rel="noreferrer"
                >
                  HTTPS guide
                </a>{" "}
                has the full per-OS details, WSL included.
              </p>
            </div>
          )}

          {/* Restart with TLS on. */}
          <div className="flex flex-col gap-2.5">
            <StepHeading
              n={oneClickTrust ? 2 : 3}
              title="Restart Overcast with TLS enabled"
            />
            {status.inContainer ? (
              <>
                <p className="text-[13px] text-fg-muted">
                  Recreate the container with <Code>OVERCAST_TLS=auto</Code> in its
                  environment:
                </p>
                <CommandTabs
                  defaultKey="compose"
                  tabs={[
                    {
                      id: "compose",
                      label: "compose",
                      note: "Add to the service, then recreate with docker compose up -d.",
                      command: "environment:\n  OVERCAST_TLS: auto",
                    },
                    {
                      id: "docker-run",
                      label: "docker run",
                      note: "Keep /data on a volume — the CA lives there.",
                      command:
                        "docker run -d -e OVERCAST_TLS=auto -v overcast-data:/data -p 4566:4566 -p 4567:4567 ghcr.io/neaox/overcast:alpha",
                    },
                  ]}
                />
              </>
            ) : (
              <>
                <p className="text-[13px] text-fg-muted">
                  Overcast is configured through environment variables, so this is the one
                  step the console cannot do for you:
                </p>
                <CommandTabs
                  defaultKey={clientOS === "windows" ? "powershell" : "shell"}
                  tabs={[
                    { id: "shell", label: "macOS / Linux", command: restartCommand },
                    {
                      id: "powershell",
                      label: "PowerShell",
                      command: '$env:OVERCAST_TLS = "auto"; overcast serve',
                    },
                    {
                      id: "cmd",
                      label: "CMD",
                      command: "set OVERCAST_TLS=auto && overcast serve",
                    },
                  ]}
                />
              </>
            )}
          </div>

          {/* Verify & switch. */}
          <div className="flex flex-col gap-2.5">
            <StepHeading n={oneClickTrust ? 3 : 4} title="Switch this page to HTTPS" />
            <SwitchToHttps
              httpsOrigin={httpsOrigin}
              probe={probe}
              hint="After the restart, plain-HTTP pages like this one stop working — check that HTTPS is up, then switch."
            />
          </div>
        </div>
      )}
    </div>
  )
}
