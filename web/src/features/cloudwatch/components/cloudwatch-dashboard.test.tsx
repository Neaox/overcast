import { Route as CloudwatchLogsIndexRoute } from "@/routes/cloudwatch/logs/index"

describe("CloudWatch Logs route metadata", () => {
  it("defines the CloudWatch Logs page title", async () => {
    const head = await CloudwatchLogsIndexRoute.options.head?.({} as never)
    expect(head?.meta?.[0]?.title).toBe("CloudWatch Logs — Overcast")
  })
})
