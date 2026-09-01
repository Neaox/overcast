import { beforeEach, describe, expect, it } from "vitest"
import { FavouritesProvider } from "@/hooks/use-favourites"
import { ServiceIconColorProvider } from "@/hooks/use-service-icon-color"
import { createEvent, fireEvent, renderWithRouter, screen, waitFor } from "@/test/render"
import { SidebarFavourites } from "./sidebar-favourites"

const FAVOURITES_STORAGE_KEY = "overcast-favourites"

/** dnd-kit removes its own post-drag `click` listener one tick of this length after a drag. */
const DND_KIT_CLICK_TEARDOWN_MS = 50

function PinnedSidebar() {
  return (
    <ServiceIconColorProvider>
      <FavouritesProvider>
        <SidebarFavourites
          collapsed={false}
          pathname="/"
          isExpanded={() => false}
          onToggleExpand={() => {}}
        />
      </FavouritesProvider>
    </ServiceIconColorProvider>
  )
}

async function renderPinned() {
  localStorage.setItem(FAVOURITES_STORAGE_KEY, JSON.stringify(["/s3", "/sqs"]))
  const result = renderWithRouter(PinnedSidebar)
  await screen.findByRole("link", { name: "S3" })
  return result
}

/** Presses the pointer on a row and moves far enough to clear the 6px activation distance. */
function dragTo(row: HTMLElement, clientX: number, clientY: number) {
  fireEvent.pointerDown(row, { isPrimary: true, button: 0, clientX: 0, clientY: 0 })
  fireEvent.pointerMove(document, { clientX, clientY })
  fireEvent.pointerUp(document, { clientX, clientY })
}

function pressWithoutDragging(row: HTMLElement) {
  fireEvent.pointerDown(row, { isPrimary: true, button: 0, clientX: 0, clientY: 0 })
  fireEvent.pointerUp(document, { clientX: 0, clientY: 0 })
}

/**
 * Dispatches the click a browser sends when the pointer is released over a link, and
 * reports whether the anchor's default action survived — i.e. whether the browser would
 * hard-navigate to the href.
 *
 * This is the assertion that catches the drag-becomes-page-load bug. dnd-kit stops the
 * post-drag click propagating but never cancels it, so `<Link>`'s own `preventDefault`
 * never runs and the browser follows the href instead.
 */
function clickFollowsHref(link: HTMLElement) {
  const click = createEvent.click(link, { bubbles: true, cancelable: true })
  link.dispatchEvent(click)
  return !click.defaultPrevented
}

describe("SidebarFavourites drag and drop", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("navigates to the pinned service when it is clicked without dragging", async () => {
    const { router } = await renderPinned()
    const link = screen.getByRole("link", { name: "S3" })

    pressWithoutDragging(link)
    fireEvent.click(link)

    await waitFor(() => expect(router.state.location.pathname).toBe("/s3"))
  })

  it("does not follow the link when a pinned item is dropped inside the sidebar", async () => {
    const { router } = await renderPinned()
    const link = screen.getByRole("link", { name: "S3" })

    dragTo(link, 0, 40)

    expect(clickFollowsHref(link)).toBe(false)
    expect(router.state.location.pathname).toBe("/")
  })

  it("does not follow the link when a pinned item is dropped outside the sidebar", async () => {
    const { router } = await renderPinned()
    const link = screen.getByRole("link", { name: "S3" })

    dragTo(link, 600, 400)

    expect(clickFollowsHref(link)).toBe(false)
    expect(router.state.location.pathname).toBe("/")
  })

  it("navigates on the next click after a drag that never produced a click", async () => {
    const { router } = await renderPinned()

    dragTo(screen.getByRole("link", { name: "S3" }), 600, 400)
    await new Promise((resolve) => setTimeout(resolve, DND_KIT_CLICK_TEARDOWN_MS + 10))

    const other = screen.getByRole("link", { name: "SQS" })
    pressWithoutDragging(other)
    fireEvent.click(other)

    await waitFor(() => expect(router.state.location.pathname).toBe("/sqs"))
  })
})
