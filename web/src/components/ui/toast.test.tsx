import { useEffect } from "react"
import { act, render, within } from "@/test/render"
import { useToast, type ToastOptions, type ToastVariant } from "@/components/ui/toast"

/**
 * `render` already wraps everything in `ToastContextProvider`, so the harness
 * only has to hand the context out. Driving the hook directly (rather than
 * through a button) keeps the timer assertions free of userEvent, which cannot
 * share a tick with fake timers.
 *
 * Queries go through `data-slot="toast"` rather than `getByRole("status")`:
 * Radix renders a separate visually-hidden live region that also carries that
 * role and echoes the toast's text.
 */
let api: ReturnType<typeof useToast>

function Harness() {
  const ctx = useToast()
  // Published from an effect, not during render — reassigning module scope
  // mid-render is a side effect React (and the lint rule) rightly rejects.
  useEffect(() => {
    api = ctx
  }, [ctx])
  return null
}

function show(item: ToastOptions): string {
  let id = ""
  act(() => {
    id = api.toast(item)
  })
  return id
}

function toasts(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('[data-slot="toast"]')]
}

function onlyToast(): HTMLElement {
  const [first, ...rest] = toasts()
  expect(first, "expected exactly one toast").toBeDefined()
  expect(rest).toHaveLength(0)
  return first
}

describe("Toast > variants", () => {
  it.each<[ToastVariant, string]>([
    ["success", "bg-success"],
    ["danger", "bg-danger"],
    ["pending", "bg-accent"],
  ])("paints the full-height left bar with the %s role colour", (variant, barClass) => {
    render(<Harness />)
    show({ title: "Log group created", variant })

    expect(onlyToast().querySelector(`span.${barClass}`)).toHaveClass(
      "absolute",
      "inset-y-0",
      "left-0",
      "w-[3px]",
    )
  })

  it.each<[ToastVariant, string]>([
    ["success", "text-success"],
    ["danger", "text-danger"],
  ])("renders the leading %s icon", (variant, iconClass) => {
    render(<Harness />)
    show({ title: "Log group created", variant })

    expect(onlyToast().querySelector(`svg.${iconClass}`)).toBeInTheDocument()
  })

  it("uses the brand cloud mark as the pending icon", () => {
    render(<Harness />)
    show({ title: "Uploading 3 objects…", variant: "pending" })

    expect(within(onlyToast()).getByRole("img", { name: "In progress" })).toBeInTheDocument()
  })

  it("shows no status icon on a neutral toast", () => {
    render(<Harness />)
    show({ title: "API key value copied" })

    expect(onlyToast().querySelector("svg.text-success")).toBeNull()
  })

  it("sets the title in mono bold", () => {
    render(<Harness />)
    show({ title: "Log group created", variant: "success" })

    expect(within(onlyToast()).getByText("Log group created")).toHaveClass("font-mono", "font-bold")
  })
})

describe("Toast > detail line", () => {
  it("typesets an identifier detail in mono", () => {
    render(<Harness />)
    show({
      title: "Log group created",
      description: "/aws/lambda/OvercastLocalStage-Ingest",
      variant: "success",
    })

    expect(within(onlyToast()).getByText("/aws/lambda/OvercastLocalStage-Ingest")).toHaveClass(
      "font-mono",
    )
  })

  it("typesets a failure detail in sans, because it reads as prose", () => {
    render(<Harness />)
    show({
      title: "Upload failed · 501",
      description: "Operation not implemented for this service.",
      variant: "danger",
    })

    expect(
      within(onlyToast()).getByText("Operation not implemented for this service."),
    ).not.toHaveClass("font-mono")
  })

  it("lets the caller override the per-variant default", () => {
    render(<Harness />)
    show({
      title: "Delete failed",
      description: "cdk-hnb659fds-assets-000000000000",
      variant: "danger",
      descriptionKind: "identifier",
    })

    expect(within(onlyToast()).getByText("cdk-hnb659fds-assets-000000000000")).toHaveClass(
      "font-mono",
    )
  })
})

describe("Toast > dismissal", () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it("auto-dismisses a success toast once its timer elapses", () => {
    render(<Harness />)
    show({ title: "Log group created", variant: "success" })

    act(() => void vi.advanceTimersByTime(10_000))

    expect(toasts()).toHaveLength(0)
  })

  it("keeps a pending toast open indefinitely, because no timer knows when the work ends", () => {
    render(<Harness />)
    show({ title: "Uploading 3 objects…", variant: "pending" })

    act(() => void vi.advanceTimersByTime(60_000))

    expect(toasts()).toHaveLength(1)
  })

  it("resolves a pending toast in place into its settled variant", () => {
    render(<Harness />)
    const id = show({ title: "Uploading 3 objects…", variant: "pending" })

    act(() => api.updateToast(id, { title: "Uploaded 3 objects", variant: "success" }))

    const toast = onlyToast()
    expect(within(toast).getByText("Uploaded 3 objects")).toBeInTheDocument()
    expect(toast.querySelector("span.bg-success")).toBeInTheDocument()
  })

  it("starts the auto-dismiss timer once a pending toast is resolved", () => {
    render(<Harness />)
    const id = show({ title: "Uploading 3 objects…", variant: "pending" })
    act(() => api.updateToast(id, { title: "Uploaded 3 objects", variant: "success" }))

    act(() => void vi.advanceTimersByTime(10_000))

    expect(toasts()).toHaveLength(0)
  })

  it("removes a toast when the caller dismisses it", () => {
    render(<Harness />)
    const id = show({ title: "Uploading 3 objects…", variant: "pending" })

    act(() => api.dismissToast(id))

    expect(toasts()).toHaveLength(0)
  })
})
