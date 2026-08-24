import { isRedirect } from "@tanstack/react-router"
import { Route as BucketIndexRoute } from "./$bucket/index"
import { Route as ObjectBrowserRoute } from "./$bucket/objects/$"

describe("S3 bucket routes", () => {
  it("forwards the bucket's front door to the object browser without a history entry", () => {
    // Every existing link into a bucket points at /s3/<bucket>; the browser
    // itself now lives a segment down so the folder or object it shows can be
    // part of the path.
    let thrown: unknown
    try {
      BucketIndexRoute.options.beforeLoad?.({ params: { bucket: "demo" } } as never)
    } catch (err) {
      thrown = err
    }
    expect(isRedirect(thrown)).toBe(true)
    expect((thrown as { options: unknown }).options).toMatchObject({
      to: "/s3/$bucket/objects/$",
      params: { bucket: "demo", _splat: "" },
      replace: true,
    })
  })

  it("titles the browser by where in the bucket it is", async () => {
    // Splats as the router supplies them — the trailing slash of a folder is
    // already trimmed off the param by the time `head` sees it.
    const at = (splat: string) =>
      ObjectBrowserRoute.options.head?.({ params: { bucket: "demo", _splat: splat } } as never)

    expect((await at(""))?.meta?.[0]?.title).toBe("demo — S3 — Overcast")
    expect((await at("logs"))?.meta?.[0]?.title).toBe("logs — demo — S3 — Overcast")
    expect((await at("logs/app.log"))?.meta?.[0]?.title).toBe("logs/app.log — demo — S3 — Overcast")
  })

  it("takes the inspected revision from the query string and nothing else", () => {
    const validate = ObjectBrowserRoute.options.validateSearch as unknown as (
      search: Record<string, unknown>,
    ) => { versionId?: string }

    expect(validate({ versionId: "v2" })).toEqual({ versionId: "v2" })
    expect(validate({})).toEqual({ versionId: undefined })
    expect(validate({ versionId: 7 })).toEqual({ versionId: undefined })
  })
})
