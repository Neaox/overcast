import { readFileSync } from "node:fs"
import { sourceFiles } from "@/test/source-files"
import { canReadClipboardText, readClipboardText, writeClipboardText } from "./clipboard"

/**
 * jsdom implements neither `navigator.clipboard` nor `document.execCommand`, so
 * both are installed per test — which makes the insecure-context case (no async
 * clipboard at all) the *default* here rather than an exotic override, exactly
 * as a browser on `http://localhost.overcast.sh` sees it.
 */
function stubAsyncClipboard(clipboard: unknown) {
  Object.defineProperty(globalThis.navigator, "clipboard", { value: clipboard, configurable: true })
}

/**
 * Stands in for `document.execCommand("copy")`, recording what was actually
 * selected at the moment of the copy — jsdom does not maintain a document
 * selection, so the focused textarea and its selection range are the only
 * observable proof that the fallback set the copy up correctly.
 */
function stubExecCommand(outcome: boolean | Error) {
  const selected: string[] = []
  document.execCommand = vi.fn((command: string) => {
    expect(command).toBe("copy")
    const field = document.activeElement
    if (field instanceof HTMLTextAreaElement) {
      selected.push(field.value.slice(field.selectionStart, field.selectionEnd))
    }
    if (outcome instanceof Error) throw outcome
    return outcome
  })
  return selected
}

describe("writeClipboardText", () => {
  beforeEach(() => {
    stubAsyncClipboard(undefined)
    // @ts-expect-error — put jsdom's "not implemented" state back between tests
    delete document.execCommand
  })

  describe("in a secure context", () => {
    it("writes through the async clipboard API", async () => {
      const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined)
      stubAsyncClipboard({ writeText })

      await writeClipboardText("arn:aws:sqs:us-east-1:000000000000:jobs")

      expect(writeText).toHaveBeenCalledWith("arn:aws:sqs:us-east-1:000000000000:jobs")
    })

    it("does not reach for the fallback when the async write succeeds", async () => {
      stubAsyncClipboard({ writeText: vi.fn().mockResolvedValue(undefined) })
      const selected = stubExecCommand(true)

      await writeClipboardText("value")

      expect(selected).toEqual([])
    })
  })

  describe("in an insecure context", () => {
    it("copies via the textarea fallback when navigator.clipboard is absent", async () => {
      const selected = stubExecCommand(true)

      await writeClipboardText("localhost:5111/backend/api")

      expect(selected).toEqual(["localhost:5111/backend/api"])
    })

    it("selects multi-line values in full", async () => {
      const selected = stubExecCommand(true)

      await writeClipboardText("KEY=one\nOTHER=two")

      expect(selected).toEqual(["KEY=one\nOTHER=two"])
    })

    it("leaves no textarea behind in the document", async () => {
      stubExecCommand(true)

      await writeClipboardText("value")

      expect(document.querySelector("textarea")).toBeNull()
    })

    it("restores focus to the control that triggered the copy", async () => {
      stubExecCommand(true)
      const button = document.createElement("button")
      document.body.appendChild(button)
      button.focus()

      await writeClipboardText("value")

      expect(document.activeElement).toBe(button)
      button.remove()
    })

    it("mounts the textarea beside the focused control, inside any focus trap", async () => {
      const dialog = document.createElement("div")
      const button = document.createElement("button")
      dialog.appendChild(button)
      document.body.appendChild(dialog)
      button.focus()
      let host: HTMLElement | null | undefined
      document.execCommand = vi.fn(() => {
        host = dialog.querySelector("textarea")?.parentElement
        return true
      })

      await writeClipboardText("value")

      expect(host).toBe(dialog)
      dialog.remove()
    })
  })

  describe("when the async write is rejected", () => {
    beforeEach(() => {
      stubAsyncClipboard({ writeText: vi.fn().mockRejectedValue(new Error("NotAllowedError")) })
    })

    it("still copies through the fallback", async () => {
      const selected = stubExecCommand(true)

      await writeClipboardText("value")

      expect(selected).toEqual(["value"])
    })

    it("rejects with a presentable message when the fallback is refused too", async () => {
      stubExecCommand(false)

      await expect(writeClipboardText("value")).rejects.toThrow(
        "The browser blocked access to the clipboard",
      )
    })
  })

  it("rejects when the fallback throws rather than returning false", async () => {
    stubExecCommand(new Error("not supported"))

    await expect(writeClipboardText("value")).rejects.toThrow(
      "The browser blocked access to the clipboard",
    )
  })

  it("rejects when the browser offers no route to the clipboard at all", async () => {
    await expect(writeClipboardText("value")).rejects.toThrow(
      "The browser blocked access to the clipboard",
    )
  })
})

describe("reading the clipboard", () => {
  afterEach(() => stubAsyncClipboard(undefined))

  it("reports reads as unavailable outside a secure context", () => {
    stubAsyncClipboard(undefined)
    expect(canReadClipboardText()).toBe(false)
  })

  it("reports reads as unavailable where only writeText is implemented", () => {
    // Firefox: page scripts are given `writeText` and never `readText`.
    stubAsyncClipboard({ writeText: vi.fn() })
    expect(canReadClipboardText()).toBe(false)
  })

  it("reports reads as available when readText is implemented", () => {
    stubAsyncClipboard({ readText: vi.fn() })
    expect(canReadClipboardText()).toBe(true)
  })

  it("returns the clipboard contents when reads are available", async () => {
    stubAsyncClipboard({ readText: vi.fn().mockResolvedValue("PORT=8080") })
    await expect(readClipboardText()).resolves.toBe("PORT=8080")
  })

  it("rejects rather than throwing synchronously when reads are unavailable", async () => {
    await expect(readClipboardText()).rejects.toThrow("does not allow reading the clipboard")
  })
})

/**
 * The whole point of this module is that no component reaches past it. A direct
 * `navigator.clipboard.writeText` is not a style preference — it is a button
 * that does nothing at all on `http://localhost.overcast.sh`, silently, which
 * is the bug that produced this file.
 */
describe("guardrail", () => {
  /** A call, not a mention — the module name appears in prose all over `src/`. */
  const PLATFORM_CALL = /navigator\.clipboard\s*\??\.\s*\w+\s*\(/

  it("is the only module in src/ that calls the platform clipboard", () => {
    const offenders = sourceFiles("src").filter(
      (file) =>
        !/\.test\.tsx?$/.test(file) &&
        !/[\\/]clipboard\.ts$/.test(file) &&
        PLATFORM_CALL.test(readFileSync(file, "utf8")),
    )

    expect(
      offenders,
      `Use <CopyButton> or useCopyToClipboard() instead of navigator.clipboard:\n${offenders.join("\n")}`,
    ).toEqual([])
  })
})
