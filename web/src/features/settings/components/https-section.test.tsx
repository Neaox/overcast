import { describe, expect, it, beforeEach } from "vitest"
import { http, HttpResponse } from "msw"
import { server } from "@/test/server"
import { render, screen } from "@/test/render"
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

    await user.click(await screen.findByRole("tab", { name: "PowerShell" }))

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

    expect(
      await screen.findByRole("button", { name: "Prepare certificates" }),
    ).toBeInTheDocument()
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

    expect(
      await screen.findByRole("button", { name: /Download CA certificate/ }),
    ).toBeDisabled()
  })

  it("enables the certificate download once the CA exists", async () => {
    stubStatus({
      inContainer: true,
      trustStore: "unknown",
      caExists: true,
      hostCommand: "overcast https enable --endpoint http://localhost:4566",
    })
    render(<HttpsSection />)

    expect(
      await screen.findByRole("button", { name: /Download CA certificate/ }),
    ).toBeEnabled()
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
    server.use(
      http.get("/api/settings/https", () => new HttpResponse(null, { status: 502 })),
    )
    render(<HttpsSection />)

    expect(await screen.findByRole("alert")).toBeInTheDocument()
  })
})
