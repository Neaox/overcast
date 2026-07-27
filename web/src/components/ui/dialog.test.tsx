import { render, screen } from "@/test/render"
import { Button } from "@/components/ui/button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogKeyHint,
  DialogTitle,
} from "@/components/ui/dialog"

/** A form dialog, which is where the multi-line and disabled cases live. */
function FormDialogHarness({
  onPrimaryAction,
  primaryActionDisabled,
}: {
  onPrimaryAction: () => void
  primaryActionDisabled?: boolean
}) {
  return (
    <Dialog open>
      <DialogContent
        onPrimaryAction={onPrimaryAction}
        primaryActionDisabled={primaryActionDisabled}
      >
        <DialogHeader>
          <DialogTitle>Create log group</DialogTitle>
        </DialogHeader>
        <input aria-label="Name" />
        <textarea aria-label="Policy document" />
        <DialogFooter hint={<DialogKeyHint action="create" />}>
          <Button>Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function renderConfirm(overrides: Partial<React.ComponentProps<typeof ConfirmDialog>> = {}) {
  const onConfirm = vi.fn()
  const onOpenChange = vi.fn()
  const result = render(
    <ConfirmDialog
      open
      onOpenChange={onOpenChange}
      title="Delete 2 objects"
      description="Objects are removed from the emulator's local state immediately."
      onConfirm={onConfirm}
      {...overrides}
    />,
  )
  return { ...result, onConfirm, onOpenChange }
}

describe("Dialog > anatomy", () => {
  it("seats the footer actions on the page surface, a different band from the body", () => {
    render(<FormDialogHarness onPrimaryAction={vi.fn()} />)

    const footer = screen.getByText("⏎ to create · esc to cancel").parentElement
    expect(footer).toHaveClass("bg-bg", "border-t")
  })

  it("renders a 30x30 icon tile in the header band when given a glyph", () => {
    renderConfirm()

    expect(document.querySelector('[data-slot="dialog-icon"]')).toHaveClass(
      "h-[30px]",
      "w-[30px]",
      "border-danger",
    )
  })

  it("advertises the keyboard contract in the footer", () => {
    renderConfirm({ confirmLabel: "Delete" })

    expect(screen.getByText("⏎ to delete · esc to cancel")).toBeInTheDocument()
  })
})

describe("Dialog > keyboard contract", () => {
  it("runs the primary action when Enter is pressed in the dialog", async () => {
    const { user, onConfirm } = renderConfirm()

    await user.keyboard("{Enter}")

    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it("runs the primary action when Enter is pressed in a single-line field", async () => {
    const onPrimaryAction = vi.fn()
    const { user } = render(<FormDialogHarness onPrimaryAction={onPrimaryAction} />)

    await user.click(screen.getByLabelText("Name"))
    await user.keyboard("{Enter}")

    expect(onPrimaryAction).toHaveBeenCalledTimes(1)
  })

  it("leaves Enter to insert a newline when focus is in a textarea", async () => {
    const onPrimaryAction = vi.fn()
    const { user } = render(<FormDialogHarness onPrimaryAction={onPrimaryAction} />)

    await user.click(screen.getByLabelText("Policy document"))
    await user.keyboard("{Enter}")

    expect(onPrimaryAction).not.toHaveBeenCalled()
  })

  it("ignores Enter while the primary action is disabled", async () => {
    const onPrimaryAction = vi.fn()
    const { user } = render(
      <FormDialogHarness onPrimaryAction={onPrimaryAction} primaryActionDisabled />,
    )

    await user.click(screen.getByLabelText("Name"))
    await user.keyboard("{Enter}")

    expect(onPrimaryAction).not.toHaveBeenCalled()
  })

  it("ignores Enter while the confirm mutation is in flight", async () => {
    const { user, onConfirm } = renderConfirm({ isPending: true })

    await user.keyboard("{Enter}")

    expect(onConfirm).not.toHaveBeenCalled()
  })

  it("leaves Shift+Enter alone, so a modifier never submits", async () => {
    const onPrimaryAction = vi.fn()
    const { user } = render(<FormDialogHarness onPrimaryAction={onPrimaryAction} />)

    await user.click(screen.getByLabelText("Name"))
    await user.keyboard("{Shift>}{Enter}{/Shift}")

    expect(onPrimaryAction).not.toHaveBeenCalled()
  })

  it("cancels the dialog when Escape is pressed", async () => {
    const { user, onOpenChange, onConfirm } = renderConfirm()

    await user.keyboard("{Escape}")

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(onConfirm).not.toHaveBeenCalled()
  })
})

describe("ConfirmDialog", () => {
  it("focuses the panel rather than Cancel, so Enter confirms instead of dismissing", () => {
    renderConfirm()

    expect(document.activeElement).toHaveAttribute("role", "dialog")
  })

  it("still confirms when the confirm button itself is clicked", async () => {
    const { user, onConfirm } = renderConfirm({ confirmLabel: "Delete" })

    await user.click(screen.getByRole("button", { name: "Delete" }))

    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it("fires the confirm action once, not twice, when Enter is pressed on the confirm button", async () => {
    const { user, onConfirm } = renderConfirm({ confirmLabel: "Delete" })

    screen.getByRole("button", { name: "Delete" }).focus()
    await user.keyboard("{Enter}")

    expect(onConfirm).toHaveBeenCalledTimes(1)
  })
})
