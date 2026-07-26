import { render, screen } from "@/test/render"
import { Button } from "./button"

describe("Button > disabled", () => {
  it("does not fire its handler when clicked", async () => {
    const onClick = vi.fn()
    const { user } = render(
      <Button disabled onClick={onClick}>
        Delete
      </Button>,
    )

    await user.click(screen.getByRole("button", { name: "Delete" }))

    expect(onClick).not.toHaveBeenCalled()
  })

  it("shows the unavailable cursor rather than swallowing pointer events", () => {
    render(<Button disabled>Delete</Button>)

    expect(screen.getByRole("button", { name: "Delete" }).className).toContain(
      "disabled:cursor-not-allowed",
    )
  })

  it("dims itself so unavailability is visible, not only announced", () => {
    render(<Button disabled>Delete</Button>)

    expect(screen.getByRole("button", { name: "Delete" }).className).toContain(
      "disabled:opacity-50",
    )
  })
})

describe("Button > busy", () => {
  it("announces itself as busy", () => {
    render(<Button busy>Create</Button>)

    expect(screen.getByRole("button", { name: /Create/ })).toHaveAttribute("aria-busy", "true")
  })

  it("is not dimmed — a busy button must not read as a disabled one", () => {
    render(<Button busy>Create</Button>)

    const button = screen.getByRole("button", { name: /Create/ })
    expect(button).toBeEnabled()
    expect(button.className).toContain("aria-busy:opacity-100")
  })

  it("takes the default cursor, since there is nothing to click", () => {
    render(<Button busy>Create</Button>)

    expect(screen.getByRole("button", { name: /Create/ }).className).toContain(
      "aria-busy:cursor-default",
    )
  })

  it("does not fire its handler while work is in flight", async () => {
    const onClick = vi.fn()
    const { user } = render(
      <Button busy onClick={onClick}>
        Create
      </Button>,
    )

    await user.click(screen.getByRole("button", { name: /Create/ }))

    expect(onClick).not.toHaveBeenCalled()
  })

  it("swaps in the busy label, dropping the idle content and its icon", () => {
    render(
      <Button busy busyLabel="Refreshing">
        <span>icon</span>
        Refresh
      </Button>,
    )

    expect(screen.getByRole("button", { name: "Refreshing" })).toBeInTheDocument()
    expect(screen.queryByText("icon")).not.toBeInTheDocument()
  })

  it("appends the blinking caret the design uses in place of a spinner", () => {
    const { container } = render(<Button busy>Create</Button>)

    expect(container.querySelector(".oc-cursor-blink")).toBeInTheDocument()
  })
})
