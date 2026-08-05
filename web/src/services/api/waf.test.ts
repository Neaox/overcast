import { describe, expect, it } from "vitest"
import { buildCreateWebACLInput } from "./waf"

describe("buildCreateWebACLInput", () => {
  it("builds a metadata-only Web ACL with explicit safe defaults", () => {
    expect(buildCreateWebACLInput({ name: "edge-acl", scope: "REGIONAL" })).toEqual({
      Name: "edge-acl",
      Scope: "REGIONAL",
      DefaultAction: { Allow: {} },
      Rules: [],
      VisibilityConfig: {
        CloudWatchMetricsEnabled: false,
        MetricName: "edge-acl",
        SampledRequestsEnabled: false,
      },
    })
  })
})
