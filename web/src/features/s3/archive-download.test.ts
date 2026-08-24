import { describe, it, expect, vi, afterEach } from "vitest"
import { buildArchiveForm, startArchiveDownload } from "./archive-download"

function fields(form: HTMLFormElement, name: string): string[] {
  return [...form.querySelectorAll<HTMLInputElement>(`input[name="${name}"]`)].map((i) => i.value)
}

afterEach(() => {
  document.body.innerHTML = ""
})

describe("buildArchiveForm", () => {
  const args = { action: "/api/s3/buckets/demo/objects/archive?ep=x", prefix: "logs/", keys: [] }

  it("posts to the archive endpoint", () => {
    const form = buildArchiveForm(document, args)
    expect(form.method).toBe("post")
    // jsdom resolves `action` against the document's base URL.
    expect(form.getAttribute("action")).toBe("/api/s3/buckets/demo/objects/archive?ep=x")
  })

  it("carries every key as its own field, in order", () => {
    const form = buildArchiveForm(document, { ...args, keys: ["logs/a.txt", "logs/b.txt"] })
    expect(fields(form, "key")).toEqual(["logs/a.txt", "logs/b.txt"])
  })

  it("carries the folder the selection was made in", () => {
    expect(fields(buildArchiveForm(document, args), "prefix")).toEqual(["logs/"])
  })

  it("sends the keys in the body, never in the URL", () => {
    // The point of the POST: a selection has no length limit and a URL does.
    const form = buildArchiveForm(document, { ...args, keys: ["logs/a.txt"] })
    expect(form.getAttribute("action")).not.toContain("logs/a.txt")
  })

  it("keeps a key with a space or an ampersand intact", () => {
    const form = buildArchiveForm(document, { ...args, keys: ["a b&c.txt"] })
    expect(fields(form, "key")).toEqual(["a b&c.txt"])
  })
})

describe("startArchiveDownload", () => {
  const args = { action: "/api/s3/buckets/demo/objects/archive", prefix: "", keys: ["a.txt"] }

  it("submits into a hidden iframe, leaving the page the user is on alone", () => {
    const submit = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {})
    startArchiveDownload(document, args)

    expect(submit).toHaveBeenCalledOnce()
    const sink = document.querySelector<HTMLIFrameElement>("iframe")
    expect(sink).not.toBeNull()
    expect(sink!.hidden).toBe(true)
    const target = submit.mock.instances[0] as HTMLFormElement
    expect(target.target).toBe(sink!.name)
  })

  it("reuses the same iframe across downloads rather than stacking them up", () => {
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {})
    startArchiveDownload(document, args)
    startArchiveDownload(document, args)
    expect(document.querySelectorAll("iframe")).toHaveLength(1)
  })

  it("leaves no form behind in the document", () => {
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {})
    startArchiveDownload(document, args)
    expect(document.querySelector("form")).toBeNull()
  })

  it("does nothing at all when nothing is selected", () => {
    const submit = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {})
    startArchiveDownload(document, { ...args, keys: [] })
    expect(submit).not.toHaveBeenCalled()
    expect(document.querySelector("iframe")).toBeNull()
  })
})
