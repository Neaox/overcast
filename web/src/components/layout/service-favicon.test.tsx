import { describe, expect, it } from "vitest"
import { renderWithRouter, waitFor } from "@/test/render"
import { ServiceFavicon } from "./service-favicon"

function faviconHref(): string {
  return document.querySelector<HTMLLinkElement>("link[rel~='icon']")?.getAttribute("href") ?? ""
}

/** The painted glyph, decoded — the router renders the route a tick after mount. */
async function faviconSvg(): Promise<string> {
  await waitFor(() => expect(faviconHref()).toMatch(/^data:image\/svg\+xml,/))
  return decodeURIComponent(faviconHref().replace("data:image/svg+xml,", ""))
}

function renderAt(pathname: string) {
  return renderWithRouter(ServiceFavicon, { path: pathname, initialEntry: pathname })
}

describe("ServiceFavicon", () => {
  it("paints the active service's own glyph into the tab icon", async () => {
    renderAt("/s3")

    expect(await faviconSvg()).toContain("lucide-archive")
  })

  it("inks the glyph for both colour schemes, like the Overcast mark", async () => {
    renderAt("/s3")

    const svg = await faviconSvg()
    expect(svg).toContain("prefers-color-scheme:dark")
    expect(svg).toContain('stroke="currentColor"')
  })

  it("declares the SVG namespace exactly once, so the data URI parses", async () => {
    renderAt("/s3")

    expect((await faviconSvg()).match(/<svg\b[^>]*/)?.[0].match(/xmlns=/g)).toHaveLength(1)
  })

  it("resolves nested routes to their owning service", async () => {
    renderAt("/lambda/layers")

    expect(await faviconSvg()).toContain("lucide-zap")
  })

  it("covers services that are reachable but not in the sidebar nav", async () => {
    renderAt("/kms")

    expect(await faviconSvg()).toContain("lucide-key")
  })

  it("restores the Overcast mark away from a service", async () => {
    const { unmount } = renderAt("/s3")
    await faviconSvg()
    unmount()

    renderAt("/")

    await waitFor(() => expect(faviconHref()).toBe("/favicon.svg"))
  })
})
