import { describe, expect, it } from "vitest"
import { s3CopyFormats } from "./copy-formats"

const BASE = "http://localhost:4566"

function format(label: string, baseUrl: string, bucket: string, key?: string): string {
  const match = s3CopyFormats(baseUrl, bucket, key).find((f) => f.label === label)
  if (!match) throw new Error(`no ${label} format`)
  return match.value
}

const pathStyle = (bucket: string, key?: string) => format("Path-style", BASE, bucket, key)
const s3Uri = (bucket: string, key?: string) => format("S3 URI", BASE, bucket, key)

describe("s3CopyFormats", () => {
  it("builds bucket-only URLs without a trailing slash", () => {
    expect(pathStyle("my-bucket")).toBe(`${BASE}/my-bucket`)
    expect(s3Uri("my-bucket")).toBe("s3://my-bucket")
  })

  it("leaves plain keys readable", () => {
    expect(pathStyle("my-bucket", "report.csv")).toBe(`${BASE}/my-bucket/report.csv`)
  })

  describe("path-style URL encoding", () => {
    it("encodes # so the key is not truncated into a fragment", () => {
      expect(pathStyle("b", "notes#1.txt")).toBe(`${BASE}/b/notes%231.txt`)
    })

    it("encodes ? so the key is not truncated into a query string", () => {
      expect(pathStyle("b", "what?.txt")).toBe(`${BASE}/b/what%3F.txt`)
    })

    it("encodes spaces", () => {
      expect(pathStyle("b", "my report.csv")).toBe(`${BASE}/b/my%20report.csv`)
    })

    it("encodes unicode", () => {
      expect(pathStyle("b", "résumé.pdf")).toBe(`${BASE}/b/r%C3%A9sum%C3%A9.pdf`)
    })

    it("preserves / between segments while encoding within them", () => {
      expect(pathStyle("b", "a b/c#d/e.txt")).toBe(`${BASE}/b/a%20b/c%23d/e.txt`)
    })

    it("keeps the trailing slash of a prefix", () => {
      expect(pathStyle("b", "folder one/")).toBe(`${BASE}/b/folder%20one/`)
    })
  })

  describe("s3:// URI stays raw", () => {
    it("does not encode the key — the AWS CLI expects the literal characters", () => {
      expect(s3Uri("b", "a b/c#d/é.txt")).toBe("s3://b/a b/c#d/é.txt")
    })
  })
})
