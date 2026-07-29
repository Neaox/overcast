import { describe, expect, it } from "vitest"
import { restInvokeUrl } from "./invoke-url"

const wildcard = { baseUrl: "http://localhost.overcast.sh:4566", region: "us-east-1" }
const bare = { baseUrl: "http://localhost:4566", region: "us-east-1" }

describe("restInvokeUrl", () => {
  it("mints the host-routed shape an AWS SDK would produce", () => {
    expect(restInvokeUrl(wildcard, "145a073dea", "prod", "/jobs")).toBe(
      "http://145a073dea.execute-api.us-east-1.localhost.overcast.sh:4566/prod/jobs",
    )
  })

  it("keeps the root resource path", () => {
    expect(restInvokeUrl(wildcard, "145a073dea", "prod", "/")).toBe(
      "http://145a073dea.execute-api.us-east-1.localhost.overcast.sh:4566/prod/",
    )
  })

  it("honours the endpoint's region", () => {
    expect(restInvokeUrl({ ...wildcard, region: "eu-west-2" }, "abc", "dev", "/items")).toBe(
      "http://abc.execute-api.eu-west-2.localhost.overcast.sh:4566/dev/items",
    )
  })

  it("omits the stage segment when the API has no stage yet", () => {
    expect(restInvokeUrl(wildcard, "abc", undefined, "/jobs")).toBe(
      "http://abc.execute-api.us-east-1.localhost.overcast.sh:4566/jobs",
    )
  })

  // *.localhost has no wildcard DNS on Windows or macOS, so the path-style form
  // the emulator also serves stays the only URL that resolves there.
  it("falls back to path-style on a base that cannot carry a subdomain", () => {
    expect(restInvokeUrl(bare, "145a073dea", "prod", "/jobs")).toBe(
      "http://localhost:4566/restapis/145a073dea/prod/_user_request_/jobs",
    )
    expect(restInvokeUrl(bare, "145a073dea", undefined, "/jobs")).toBe(
      "http://localhost:4566/restapis/145a073dea/_user_request_/jobs",
    )
  })
})
