import { browserSplat, parseBrowserPath } from "./object-location"

describe("parseBrowserPath", () => {
  it("reads an empty splat as the bucket root", () => {
    expect(parseBrowserPath(undefined)).toEqual({ prefix: "" })
    expect(parseBrowserPath("")).toEqual({ prefix: "" })
  })

  it("reads a trailing slash as the folder to list", () => {
    expect(parseBrowserPath("logs/")).toEqual({ prefix: "logs/" })
    expect(parseBrowserPath("logs/2026/01/")).toEqual({ prefix: "logs/2026/01/" })
  })

  it("reads anything else as an object, and lists the folder it sits in", () => {
    expect(parseBrowserPath("logs/2026/app.log")).toEqual({
      prefix: "logs/2026/",
      objectKey: "logs/2026/app.log",
    })
  })

  it("puts an object at the bucket root in the root listing", () => {
    expect(parseBrowserPath("report.csv")).toEqual({ prefix: "", objectKey: "report.csv" })
  })

  it("keeps a key that only looks like a folder addressable as a folder", () => {
    // A console-written folder marker is stored as a real object whose key ends
    // in "/". It is drawn as a folder, so it is addressed as one too.
    expect(parseBrowserPath("empty-folder/")).toEqual({ prefix: "empty-folder/" })
  })
})

describe("browserSplat", () => {
  it("restores the trailing slash the router trimmed off the param", () => {
    // The URL keeps it, the splat param does not, and it is the only thing
    // separating the folder "logs/" from an object named "logs".
    expect(browserSplat("logs", "/s3/demo/objects/logs/")).toBe("logs/")
    expect(browserSplat("logs", "/s3/demo/objects/logs")).toBe("logs")
  })

  it("leaves a key alone", () => {
    expect(browserSplat("logs/app.log", "/s3/demo/objects/logs/app.log")).toBe("logs/app.log")
  })

  it("reads the bucket root the same with or without the slash", () => {
    expect(browserSplat("", "/s3/demo/objects")).toBe("")
    expect(browserSplat("", "/s3/demo/objects/")).toBe("")
    expect(browserSplat(undefined, "/s3/demo/objects")).toBe("")
  })
})
