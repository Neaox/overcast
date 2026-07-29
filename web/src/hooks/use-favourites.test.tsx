import { beforeEach, describe, expect, it } from "vitest"
import { FavouritesProvider, useFavourites } from "@/hooks/use-favourites"
import { renderWithData, screen } from "@/test/render"

const FAVOURITES_KEY = "overcast-favourites"

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

function renderPins() {
  return renderWithData(
    <FavouritesProvider>
      <Pins />
    </FavouritesProvider>,
    [],
  )
}

describe("FavouritesProvider pins", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  // Every service is always running, so there is no enabled subset to seed
  // pins from — the sidebar starts empty until the user chooses.
  it("pins nothing until the user picks something", () => {
    renderPins()

    expect(screen.getByText("pins:")).toBeInTheDocument()
    expect(localStorage.getItem(FAVOURITES_KEY)).toBeNull()
  })

  it("keeps pins the user chose", () => {
    localStorage.setItem(FAVOURITES_KEY, JSON.stringify(["/lambda"]))

    renderPins()

    expect(screen.getByText("pins: /lambda")).toBeInTheDocument()
  })

  it("honours an empty stored list rather than treating it as unset", () => {
    localStorage.setItem(FAVOURITES_KEY, JSON.stringify([]))

    renderPins()

    expect(screen.getByText("pins:")).toBeInTheDocument()
  })

  it("writes the pin the user adds", async () => {
    const { user } = renderPins()

    await user.click(screen.getByRole("button", { name: "toggle lambda" }))

    expect(localStorage.getItem(FAVOURITES_KEY)).toBe(JSON.stringify(["/lambda"]))
  })

  it("writes a reordered list", async () => {
    localStorage.setItem(FAVOURITES_KEY, JSON.stringify(["/s3", "/sqs"]))
    const { user } = renderPins()

    await user.click(screen.getByRole("button", { name: "reorder" }))

    expect(localStorage.getItem(FAVOURITES_KEY)).toBe(JSON.stringify(["/sqs", "/s3"]))
  })
})
