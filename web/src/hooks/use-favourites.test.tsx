import { beforeEach, describe, expect, it } from "vitest"
import { FavouritesProvider, useFavourites } from "@/hooks/use-favourites"
import { SERVICES } from "@/lib/service-registry"
import { renderWithData, screen } from "@/test/render"
import type { HealthResponse } from "@/types/common"

const FAVOURITES_KEY = "overcast-favourites"

function healthWith(services: string[]): HealthResponse {
  return {
    status: "ok",
    timestamp: "2026-07-29T00:00:00Z",
    version: "0.1.0-test",
    services,
    storage: { default: "memory" },
  }
}

/** Every routed service switched on — what an emulator without OVERCAST_SERVICES reports. */
const EVERYTHING = healthWith(
  Object.entries(SERVICES)
    .filter(([, entry]) => "to" in entry)
    .map(([name]) => name),
)

/** A narrowed run: OVERCAST_SERVICES=s3,sqs. */
const NARROWED = healthWith(["s3", "sqs"])

function Pins() {
  const { favourites, reorderFavourites, toggleFavourite } = useFavourites()
  return (
    <>
      <p>pins: {favourites.join(" ")}</p>
      <button onClick={() => reorderFavourites([...favourites].reverse())}>reorder</button>
      <button onClick={() => toggleFavourite("/lambda")}>toggle lambda</button>
    </>
  )
}

function renderPins(health: HealthResponse) {
  return renderWithData(
    <FavouritesProvider>
      <Pins />
    </FavouritesProvider>,
    [[["health"], health]],
  )
}

describe("FavouritesProvider default pins", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("pins the enabled services when a narrowed run has no pins of its own", () => {
    renderPins(NARROWED)

    expect(screen.getByText("pins: /s3 /sqs")).toBeInTheDocument()
  })

  it("stores nothing until the user edits the defaults", () => {
    renderPins(NARROWED)

    expect(localStorage.getItem(FAVOURITES_KEY)).toBeNull()
  })

  it("pins nothing when every service is enabled", () => {
    renderPins(EVERYTHING)

    expect(screen.getByText("pins:")).toBeInTheDocument()
  })

  it("keeps pins the user chose, defaults never override them", () => {
    localStorage.setItem(FAVOURITES_KEY, JSON.stringify(["/lambda"]))

    renderPins(NARROWED)

    expect(screen.getByText("pins: /lambda")).toBeInTheDocument()
  })

  it("honours an empty stored list rather than treating it as unset", () => {
    localStorage.setItem(FAVOURITES_KEY, JSON.stringify([]))

    renderPins(NARROWED)

    expect(screen.getByText("pins:")).toBeInTheDocument()
  })

  it("commits the defaults when the user reorders them", async () => {
    const { user } = renderPins(NARROWED)

    await user.click(screen.getByRole("button", { name: "reorder" }))

    expect(screen.getByText("pins: /sqs /s3")).toBeInTheDocument()
    expect(localStorage.getItem(FAVOURITES_KEY)).toBe(JSON.stringify(["/sqs", "/s3"]))
  })

  it("commits the defaults when the user pins something alongside them", async () => {
    const { user } = renderPins(NARROWED)

    await user.click(screen.getByRole("button", { name: "toggle lambda" }))

    expect(localStorage.getItem(FAVOURITES_KEY)).toBe(JSON.stringify(["/s3", "/sqs", "/lambda"]))
  })
})
