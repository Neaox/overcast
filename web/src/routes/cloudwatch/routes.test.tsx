import { Route as CloudwatchIndexRoute } from "./index"

describe("CloudWatch routes", () => {
  it("defines the CloudWatch landing page title", async () => {
    const head = await CloudwatchIndexRoute.options.head?.({} as never)
    expect(head?.meta?.[0]?.title).toBe("CloudWatch — Overcast")
  })
})
