import { describe, expect, it } from "vitest"
import { HOST_ROUTE_LABELS, hostRoutedUrl, supportsHostRouting } from "./host-routed-url"

// The grammar under test is the same one the router dispatches on
// (internal/middleware/hostroute.go) and the server mints with
// (serviceutil.HostRoutedURLFromBase):
//
//   {scheme}://{id}.{label}.{region}.{host}[:{port}]{path}
//
// Keeping the console on it is what makes a URL the console hands out one the
// emulator can serve back — see docs/plans/host-routing-precedence.md §8.

describe("supportsHostRouting", () => {
  it("accepts a wildcard-DNS base", () => {
    expect(supportsHostRouting("http://localhost.overcast.sh:4566")).toBe(true)
  })

  it("accepts any multi-label host, since the operator chose it", () => {
    expect(supportsHostRouting("https://overcast.example.test")).toBe(true)
  })

  // *.localhost has no wildcard DNS on Windows or macOS, so a host-routed URL
  // minted on it would not resolve in the browser that was handed it.
  it("rejects a bare single-label host", () => {
    expect(supportsHostRouting("http://localhost:4566")).toBe(false)
    expect(supportsHostRouting("http://overcast:4566")).toBe(false)
  })

  it("rejects IP literals, which cannot carry a subdomain", () => {
    expect(supportsHostRouting("http://127.0.0.1:4566")).toBe(false)
    expect(supportsHostRouting("http://[::1]:4566")).toBe(false)
  })

  it("rejects a base that will not parse", () => {
    expect(supportsHostRouting("not a url")).toBe(false)
  })
})

describe("hostRoutedUrl", () => {
  it("mints the canonical execute-api shape with the base's port", () => {
    expect(
      hostRoutedUrl(
        "http://localhost.overcast.sh:4566",
        "execute-api",
        "145a073dea",
        "us-east-1",
        "/prod/jobs",
      ),
    ).toBe("http://145a073dea.execute-api.us-east-1.localhost.overcast.sh:4566/prod/jobs")
  })

  it("carries the base's scheme", () => {
    expect(
      hostRoutedUrl(
        "https://localhost.overcast.sh",
        HOST_ROUTE_LABELS.appsyncApi,
        "myapi",
        "eu-west-1",
        "/graphql",
      ),
    ).toBe("https://myapi.appsync-api.eu-west-1.localhost.overcast.sh/graphql")
  })

  it("omits the region segment rather than leaving it empty", () => {
    expect(hostRoutedUrl("http://localhost.overcast.sh:4566", "execute-api", "abc", "", "/")).toBe(
      "http://abc.execute-api.localhost.overcast.sh:4566/",
    )
  })

  it("tolerates a trailing slash on the base", () => {
    expect(
      hostRoutedUrl("http://localhost.overcast.sh:4566/", "execute-api", "abc", "us-east-1", ""),
    ).toBe("http://abc.execute-api.us-east-1.localhost.overcast.sh:4566")
  })

  it("returns undefined when the base cannot carry a host route", () => {
    expect(
      hostRoutedUrl("http://localhost:4566", "execute-api", "abc", "us-east-1", "/prod"),
    ).toBeUndefined()
    expect(
      hostRoutedUrl("http://127.0.0.1:4566", "execute-api", "abc", "us-east-1", "/prod"),
    ).toBeUndefined()
  })
})
