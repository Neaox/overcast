import { render, screen } from "@/test/render"
import { CopyButton } from "./copy-button"

/**
 * `userEvent.setup()` — which `render` calls — installs its own
 * `navigator.clipboard` stub over whatever is there. Both helpers below must
 * therefore run *after* render, or user-event's stub silently wins and every
 * test ends up exercising the secure-context path.
 */

/** Installs an async clipboard, as a secure context (https, or localhost) has. */
function secureContext() {
  const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined)
  Object.defineProperty(globalThis.navigator, "clipboard", {
    value: { writeText },
    configurable: true,
  })
  return writeText
}

/**
 * Removes the async clipboard, as a browser on `http://localhost.overcast.sh`
 * does, and answers the `document.execCommand("copy")` fallback with `allowed`.
 * Returns the values the fallback managed to put up for copying.
 */
function insecureContext(allowed: boolean) {
  Object.defineProperty(globalThis.navigator, "clipboard", { value: undefined, configurable: true })
  const copied: string[] = []
  document.execCommand = vi.fn(() => {
    const field = document.activeElement
    if (field instanceof HTMLTextAreaElement) copied.push(field.value)
    return allowed
  })
  return copied
}

describe("CopyButton", () => {
  it("names itself after the thing it copies", () => {
    render(<CopyButton value="backend/api" noun="repository URI" />)

    expect(screen.getByRole("button", { name: "Copy repository URI" })).toBeInTheDocument()
  })

  describe("in a secure context", () => {
    it("puts the value on the clipboard", async () => {
      const { user } = render(<CopyButton value="backend/api" noun="repository URI" />)
      const writeText = secureContext()

      await user.click(screen.getByRole("button", { name: "Copy repository URI" }))

      expect(writeText).toHaveBeenCalledWith("backend/api")
    })

    it("confirms the copy by name", async () => {
      const { user } = render(<CopyButton value="backend/api" noun="repository URI" />)
      secureContext()

      await user.click(screen.getByRole("button", { name: "Copy repository URI" }))

      expect(await screen.findByText("Copied repository URI")).toBeInTheDocument()
    })

    it("acknowledges the copy on the button itself", async () => {
      const { user } = render(<CopyButton value="backend/api" noun="repository URI" />)
      secureContext()
      const button = screen.getByRole("button", { name: "Copy repository URI" })

      await user.click(button)

      expect(await screen.findByText("Copied repository URI")).toBeInTheDocument()
      expect(button.querySelector(".text-success")).toBeInTheDocument()
    })
  })

  describe("in an insecure context", () => {
    it("still copies, through the textarea fallback", async () => {
      const { user } = render(<CopyButton value="localhost:5111/backend/api" noun="URI" />)
      const copied = insecureContext(true)

      await user.click(screen.getByRole("button", { name: "Copy URI" }))

      expect(copied).toEqual(["localhost:5111/backend/api"])
    })

    it("confirms the copy just as a secure context does", async () => {
      const { user } = render(<CopyButton value="localhost:5111/backend/api" noun="URI" />)
      insecureContext(true)

      await user.click(screen.getByRole("button", { name: "Copy URI" }))

      expect(await screen.findByText("Copied URI")).toBeInTheDocument()
    })
  })

  describe("when the browser blocks the clipboard entirely", () => {
    it("says so rather than failing silently", async () => {
      const { user } = render(<CopyButton value="backend/api" noun="repository URI" />)
      insecureContext(false)

      await user.click(screen.getByRole("button", { name: "Copy repository URI" }))

      expect(await screen.findByText("Could not copy repository URI")).toBeInTheDocument()
    })

    it("does not claim the copy succeeded", async () => {
      const { user } = render(<CopyButton value="backend/api" noun="repository URI" />)
      insecureContext(false)

      await user.click(screen.getByRole("button", { name: "Copy repository URI" }))

      expect(await screen.findByText("Could not copy repository URI")).toBeInTheDocument()
      expect(screen.queryByText("Copied repository URI")).not.toBeInTheDocument()
    })
  })

  it("does not activate the row it sits in", async () => {
    const onRowClick = vi.fn()
    const { user } = render(
      <div onClick={onRowClick}>
        <CopyButton value="backend/api" noun="repository URI" />
      </div>,
    )
    secureContext()

    await user.click(screen.getByRole("button", { name: "Copy repository URI" }))

    expect(onRowClick).not.toHaveBeenCalled()
  })
})
