import { describe, expect, it } from "vitest"
import { s3CopyFormats } from "./copy-formats"

const BASE = "http://localhost:4566"

function format(
  label: string,
  baseUrl: string,
  bucket: string,
  key?: string,
  versionId?: string,
): string {
  const match = s3CopyFormats(baseUrl, bucket, key, versionId).find((f) => f.label === label)
  if (!match) throw new Error(`no ${label} format`)
  return match.value
}

const pathStyle = (bucket: string, key?: string, versionId?: string) =>
  format("Path-style", BASE, bucket, key, versionId)
const s3Uri = (bucket: string, key?: string, versionId?: string) =>
  format("S3 URI", BASE, bucket, key, versionId)

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

  describe("version ids", () => {
    it("addresses the named version in the path-style URL", () => {
      expect(pathStyle("b", "report.csv", "v2")).toBe(`${BASE}/b/report.csv?versionId=v2`)
    })

    it('emits "null", which is a real version id and not an absent one', () => {
      expect(pathStyle("b", "report.csv", "null")).toBe(`${BASE}/b/report.csv?versionId=null`)
    })

    it("leaves the URL alone when no version was named", () => {
      expect(pathStyle("b", "report.csv")).toBe(`${BASE}/b/report.csv`)
    })

    it("percent-encodes a version id so it cannot end the query string early", () => {
      expect(pathStyle("b", "report.csv", "a&b=c")).toBe(`${BASE}/b/report.csv?versionId=a%26b%3Dc`)
    })

    it("leaves the s3:// URI unversioned — the CLI takes --version-id separately", () => {
      expect(s3Uri("b", "report.csv", "v2")).toBe("s3://b/report.csv")
    })

    it("ignores a version id on a bucket-only URL, which addresses no object", () => {
      expect(pathStyle("b", undefined, "v2")).toBe(`${BASE}/b`)
    })
  })
})
