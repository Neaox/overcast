import { http, HttpResponse } from "msw"
import { render, screen, waitFor, within } from "@/test/render"
import { server } from "@/test/server"
import { RestApiMonitorTab } from "./rest-api-monitor-tab"
import type { MonitorResponse } from "@/types"

/** A minimal but realistic MonitorResponse fixture for a REST API. */
function monitorResponse(requests: number): MonitorResponse {
  return {
    enabled: true,
    range: "1h",
    periodSeconds: 60,
    series: [
      {
        metric: "Count",
        statistic: "Sum",
        unit: "Count",
        points: [{ timestamp: new Date().toISOString(), value: requests }],
      },
      {
        metric: "4XXError",
        statistic: "Sum",
        unit: "Count",
        points: [{ timestamp: new Date().toISOString(), value: 1 }],
      },
      {
        metric: "5XXError",
        statistic: "Sum",
        unit: "Count",
        points: [{ timestamp: new Date().toISOString(), value: 0 }],
      },
      {
        metric: "Latency",
        statistic: "Average",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 42 }],
      },
      {
        metric: "Latency",
        statistic: "Maximum",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 100 }],
      },
      {
        metric: "IntegrationLatency",
        statistic: "Average",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 30 }],
      },
      {
        metric: "IntegrationLatency",
        statistic: "Maximum",
        unit: "Milliseconds",
        points: [{ timestamp: new Date().toISOString(), value: 80 }],
      },
    ],
  }
}

describe("RestApiMonitorTab", () => {
  it("renders the card catalogue from the fetched series", async () => {
    // Given: the emulator answers the REST API's aggregate (no-stage) metrics.
    let lastUrl: string | undefined
    server.use(
      http.get("/api/apigateway/restapis/:apiId/metrics", ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json(monitorResponse(7))
      }),
    )

    // When: the tab renders with two known stages.
    render(
      <RestApiMonitorTab
        apiId="abc123"
        stages={[
          { stageName: "prod", deploymentId: "d1" },
          { stageName: "dev", deploymentId: "d2" },
        ]}
      />,
    )

    // Then: all three cards render, and the aggregate request carried no stage param.
    expect(await screen.findByText("Requests & Errors")).toBeInTheDocument()
    expect(screen.getByText("Latency")).toBeInTheDocument()
    expect(screen.getByText("Integration latency")).toBeInTheDocument()
    await waitFor(() => expect(lastUrl).toContain("range=1h"))
    expect(lastUrl).not.toContain("stage=")

    // And: the stage selector defaults to "All stages" and lists both stages.
    expect(screen.getByRole("option", { name: "All stages" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "prod" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "dev" })).toBeInTheDocument()
  })

  it("refires the metrics request with &stage= when a stage is selected", async () => {
    // Given: the emulator answers whatever stage is requested.
    const urls: string[] = []
    server.use(
      http.get("/api/apigateway/restapis/:apiId/metrics", ({ request }) => {
        urls.push(request.url)
        return HttpResponse.json(monitorResponse(3))
      }),
    )
    const { user } = render(
      <RestApiMonitorTab apiId="abc123" stages={[{ stageName: "prod", deploymentId: "d1" }]} />,
    )
    await screen.findByText("Requests & Errors")
    expect(urls).toHaveLength(1)
    expect(urls[0]).not.toContain("stage=")

    // When: the stage selector is switched to "prod".
    await user.selectOptions(screen.getByLabelText("Stage"), "prod")

    // Then: a new request goes out scoped to that stage, without remounting
    // the panel (the same cards and range control are still on screen).
    await waitFor(() => expect(urls).toHaveLength(2))
    expect(urls[1]).toContain("stage=prod")
    expect(screen.getByText("Requests & Errors")).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "Last 1 hour", selected: true })).toBeInTheDocument()
  })

  it("shows only 'All stages' without crashing when the API has no stages", async () => {
    // Given: an API with no deployed stages yet.
    server.use(
      http.get("/api/apigateway/restapis/:apiId/metrics", () =>
        HttpResponse.json(monitorResponse(0)),
      ),
    )

    // When: the tab renders with an empty stage list.
    render(<RestApiMonitorTab apiId="abc123" stages={[]} />)

    // Then: the selector still renders, offering only "All stages", and it's
    // not disabled.
    await screen.findByText("Requests & Errors")
    const select = screen.getByLabelText("Stage")
    expect(select).toBeEnabled()
    expect(within(select).getAllByRole("option")).toHaveLength(1)
    expect(within(select).getByRole("option", { name: "All stages" })).toBeInTheDocument()
  })
})
