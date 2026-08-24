import { http, HttpResponse } from "msw"
import { render, screen, waitFor, within } from "@/test/render"
import { server } from "@/test/server"
import { HttpApiMonitorTab } from "./http-api-monitor-tab"
import type { MonitorResponse } from "@/types"

/** A minimal but realistic MonitorResponse fixture for an HTTP (v2) API — note the lowercase 4xx/5xx metric names. */
function monitorResponse(): MonitorResponse {
  return {
    enabled: true,
    range: "1h",
    periodSeconds: 60,
    series: [
      {
        metric: "Count",
        statistic: "Sum",
        unit: "Count",
        points: [{ timestamp: new Date().toISOString(), value: 5 }],
      },
      {
        metric: "4xx",
        statistic: "Sum",
        unit: "Count",
        points: [{ timestamp: new Date().toISOString(), value: 1 }],
      },
      {
        metric: "5xx",
        statistic: "Sum",
        unit: "Count",
        points: [{ timestamp: new Date().toISOString(), value: 0 }],
      },
      {
        metric: "Latency",
        statistic: "Average",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 20 }],
      },
      {
        metric: "Latency",
        statistic: "Maximum",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 55 }],
      },
      {
        metric: "IntegrationLatency",
        statistic: "Average",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 15 }],
      },
      {
        metric: "IntegrationLatency",
        statistic: "Maximum",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 40 }],
      },
    ],
  }
}

describe("HttpApiMonitorTab", () => {
  it("renders the card catalogue from the fetched series", async () => {
    // Given: the emulator answers the HTTP API's aggregate (no-stage) metrics.
    let lastUrl: string | undefined
    server.use(
      http.get("/api/apigateway/apis/:apiId/metrics", ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json(monitorResponse())
      }),
    )

    // When: the tab renders with one known stage.
    render(
      <HttpApiMonitorTab apiId="xyz789" stages={[{ stageName: "$default", autoDeploy: true }]} />,
    )

    // Then: all three cards render, and the aggregate request carried no stage param.
    expect(await screen.findByText("Requests & Errors")).toBeInTheDocument()
    expect(screen.getByText("Latency")).toBeInTheDocument()
    expect(screen.getByText("Integration latency")).toBeInTheDocument()
    await waitFor(() => expect(lastUrl).toContain("range=1h"))
    expect(lastUrl).not.toContain("stage=")
  })

  it("refires the metrics request with &stage= when a stage is selected", async () => {
    // Given: the emulator answers whatever stage is requested.
    const urls: string[] = []
    server.use(
      http.get("/api/apigateway/apis/:apiId/metrics", ({ request }) => {
        urls.push(request.url)
        return HttpResponse.json(monitorResponse())
      }),
    )
    const { user } = render(
      <HttpApiMonitorTab apiId="xyz789" stages={[{ stageName: "$default", autoDeploy: true }]} />,
    )
    await screen.findByText("Requests & Errors")
    expect(urls).toHaveLength(1)
    expect(urls[0]).not.toContain("stage=")

    // When: the stage selector is switched to "$default".
    await user.selectOptions(screen.getByLabelText("Stage"), "$default")

    // Then: a new request goes out scoped to that stage.
    await waitFor(() => expect(urls).toHaveLength(2))
    expect(urls[1]).toContain(`stage=${encodeURIComponent("$default")}`)
    expect(screen.getByText("Requests & Errors")).toBeInTheDocument()
  })

  it("shows only 'All stages' without crashing when the API has no stages", async () => {
    // Given: an HTTP API with no stages yet.
    server.use(
      http.get("/api/apigateway/apis/:apiId/metrics", () => HttpResponse.json(monitorResponse())),
    )

    // When: the tab renders with an empty stage list.
    render(<HttpApiMonitorTab apiId="xyz789" stages={[]} />)

    // Then: the selector still renders, offering only "All stages", and it's
    // not disabled.
    await screen.findByText("Requests & Errors")
    const select = screen.getByLabelText("Stage")
    expect(select).toBeEnabled()
    expect(within(select).getAllByRole("option")).toHaveLength(1)
    expect(within(select).getByRole("option", { name: "All stages" })).toBeInTheDocument()
  })
})
