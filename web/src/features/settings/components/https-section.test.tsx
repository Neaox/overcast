import { describe, expect, it, beforeEach } from "vitest"
import { http, HttpResponse } from "msw"
import { server } from "@/test/server"
import { render, screen, within } from "@/test/render"
import type { HttpsStatus } from "@/services/api/settings"
import { HttpsSection } from "./https-section"

function stubStatus(status: Partial<HttpsStatus>) {
  server.use(
    http.get("/api/settings/https", () =>
      HttpResponse.json({
        mode: "off",
        caExists: false,
        trustStore: "not_installed",
        inContainer: false,
        restartCommand: "OVERCAST_TLS=auto overcast serve",
        httpsEndpoint: "https://localhost:4566",
        caEphemeral: false,
        caCertPath: "/home/dev/.overcast/data/ca/rootCA.pem",
        ...status,
      }),
    ),
  )
}

describe("HttpsSection > native daemon over plain HTTP", () => {
  it("offers the one-click enable that installs certificate trust", async () => {
    render(<HttpsSection />)

    expect(await screen.findByRole("button", { name: /Enable HTTPS/ })).toBeInTheDocument()
  })

  it("shows the restart command — the one step the console cannot perform", async () => {
    render(<HttpsSection />)

    expect(await screen.findByText("OVERCAST_TLS=auto overcast serve")).toBeInTheDocument()
  })

  it("shows the PowerShell restart spelling when that tab is selected", async () => {
    const { user } = render(<HttpsSection />)

    // Scoped to the restart step's tablist: the endpoint step below it offers
    // a "PowerShell" tab too.
    const restartTabs = await screen.findByRole("tablist", { name: "Restart commands" })
    await user.click(within(restartTabs).getByRole("tab", { name: "PowerShell" }))

    expect(
      await screen.findByText('$env:OVERCAST_TLS = "auto"; overcast serve'),
    ).toBeInTheDocument()
  })

  it("reports certificates ready and trust installed after enabling", async () => {
    const { user } = render(<HttpsSection />)

    await user.click(await screen.findByRole("button", { name: /Enable HTTPS/ }))

    expect(
      await screen.findByText(/Done — certificates exist and the CA is trusted/),
    ).toBeInTheDocument()
  })
})

describe("HttpsSection > ephemeral CA", () => {
  const shareCommand = "overcast https enable   # once per machine, on the host"

  it("warns that a container-minted CA will not survive recreation", async () => {
    stubStatus({
      inContainer: true,
      trustStore: "unknown",
      caEphemeral: true,
      caShareCommand: shareCommand,
    })
    render(<HttpsSection />)

    expect(await screen.findByText(/This CA dies with the container/)).toBeInTheDocument()
    // Matched loosely: the DOM collapses the runs of spaces in the command.
    expect(await screen.findByText(/once per machine, on the host/)).toBeInTheDocument()
  })

  it("stays quiet once OVERCAST_CA_DIR points at a host-owned CA", async () => {
    stubStatus({ inContainer: true, trustStore: "unknown", caEphemeral: false })
    render(<HttpsSection />)

    // Wait for the flow to render before asserting on an absence.
    await screen.findByRole("button", { name: "Prepare certificates" })
    expect(screen.queryByText(/This CA dies with the container/)).not.toBeInTheDocument()
  })

  it("warns on a daemon already serving HTTPS, where the cost is already sunk", async () => {
    stubStatus({
      mode: "auto",
      caExists: true,
      inContainer: true,
      trustStore: "unknown",
      caEphemeral: true,
      caShareCommand: shareCommand,
    })
    render(<HttpsSection />)

    expect(await screen.findByText(/This CA dies with the container/)).toBeInTheDocument()
  })

  it("never warns about a native daemon, whose CA is as durable as the machine", async () => {
    render(<HttpsSection />)

    await screen.findByRole("button", { name: /Enable HTTPS/ })
    expect(screen.queryByText(/This CA dies with the container/)).not.toBeInTheDocument()
  })
})

describe("HttpsSection > endpoint URL guidance", () => {
  it("tells the user to repoint SDKs at the https endpoint", async () => {
    render(<HttpsSection />)

    expect(await screen.findByText(/Update your endpoint URL to https/)).toBeInTheDocument()
    expect(
      await screen.findByText(/export AWS_ENDPOINT_URL=https:\/\/localhost:4566/),
    ).toBeInTheDocument()
  })

  it("names the CA bundle, which the AWS CLI needs even once the CA is trusted", async () => {
    render(<HttpsSection />)

    expect(
      await screen.findByText(/AWS_CA_BUNDLE=\/home\/dev\/\.overcast\/data\/ca\/rootCA\.pem/),
    ).toBeInTheDocument()
  })

  it("falls back to a placeholder path for a containerized daemon", async () => {
    // The daemon's own CA path names a file inside the container; the host
    // uses whatever copy it downloaded, which nothing here can know.
    stubStatus({ inContainer: true, trustStore: "unknown", caCertPath: undefined })
    const { user } = render(<HttpsSection />)

    const endpointTabs = await screen.findByRole("tablist", { name: "Endpoint URL commands" })
    await user.click(within(endpointTabs).getByRole("tab", { name: "One-off CLI call" }))

    expect(
      await screen.findByText("aws --endpoint-url https://localhost:4566 s3 ls"),
    ).toBeInTheDocument()
  })

  it("still shows the endpoint change once the daemon is already on HTTPS", async () => {
    stubStatus({ mode: "auto", caExists: true, trustStore: "installed" })
    render(<HttpsSection />)

    expect(
      await screen.findByText(/export AWS_ENDPOINT_URL=https:\/\/localhost:4566/),
    ).toBeInTheDocument()
  })
})

describe("HttpsSection > containerized daemon over plain HTTP", () => {
  beforeEach(() => {
    stubStatus({
      inContainer: true,
      trustStore: "unknown",
      hostCommand: "overcast https enable --endpoint http://localhost:4566",
    })
  })

  it("offers certificate preparation instead of a one-click trust install", async () => {
    render(<HttpsSection />)

    expect(await screen.findByRole("button", { name: "Prepare certificates" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Enable HTTPS/ })).not.toBeInTheDocument()
  })

  it("shows the host-side trust command", async () => {
    render(<HttpsSection />)

    expect(
      await screen.findByText("overcast https enable --endpoint http://localhost:4566"),
    ).toBeInTheDocument()
  })

  it("withholds the certificate download until certificates exist", async () => {
    render(<HttpsSection />)

    expect(await screen.findByRole("button", { name: /Download CA certificate/ })).toBeDisabled()
  })

  it("enables the certificate download once the CA exists", async () => {
    stubStatus({
      inContainer: true,
      trustStore: "unknown",
      caExists: true,
      hostCommand: "overcast https enable --endpoint http://localhost:4566",
    })
    render(<HttpsSection />)

    expect(await screen.findByRole("button", { name: /Download CA certificate/ })).toBeEnabled()
  })
})

describe("HttpsSection > daemon already serving HTTPS", () => {
  it("offers a reachability check when this page is still on plain HTTP", async () => {
    stubStatus({ mode: "auto", caExists: true, trustStore: "installed" })
    render(<HttpsSection />)

    expect(
      await screen.findByRole("button", { name: /Check HTTPS availability/ }),
    ).toBeInTheDocument()
  })
})

describe("HttpsSection > explicit certificate mode", () => {
  it("explains trust is operator-managed and offers no actions", async () => {
    stubStatus({ mode: "explicit", caExists: false, trustStore: "unknown" })
    render(<HttpsSection />)

    expect(await screen.findByText(/trust management/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /Enable HTTPS/ })).not.toBeInTheDocument()
  })
})

describe("HttpsSection > status endpoint failure", () => {
  it("shows an error rather than a broken flow", async () => {
    server.use(http.get("/api/settings/https", () => new HttpResponse(null, { status: 502 })))
    render(<HttpsSection />)

    expect(await screen.findByRole("alert")).toBeInTheDocument()
  })
})
