/**
 * The object browser's virtualized listing: row identity and paging.
 *
 * jsdom gives every element a zero height, so the real virtualizer renders no
 * rows; the mock below renders them all and captures the options the browser
 * configured it with — `getItemKey` is asserted through that capture. The S3
 * client is replaced at the `services/api` seam (MSW cannot intercept the AWS
 * SDK's HTTP handler), returning canned listing pages.
 */
import { renderWithRouter, screen, waitFor } from "@/test/render"
import type { S3Object, S3ObjectVersion, S3Prefix } from "@/types"
// Type-only, so referencing it inside the hoisted vi.mock factory is legal:
// types are erased and never captured.
import type * as ApiModule from "@/services/api"
import { BucketDetail } from "./bucket-detail"

interface VirtualizerOptions {
  count: number
  getItemKey?: (index: number) => React.Key
}

const virt = vi.hoisted(() => ({
  options: null as VirtualizerOptions | null,
}))

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: (options: VirtualizerOptions) => {
    virt.options = options
    const { count } = options
    return {
      getTotalSize: () => count * 41,
      getVirtualItems: () =>
        Array.from({ length: count }, (_, index) => ({
          index,
          key: options.getItemKey ? options.getItemKey(index) : index,
          start: index * 41,
          end: index * 41 + 41,
        })),
      measureElement: vi.fn(),
      measure: vi.fn(),
      scrollToIndex: vi.fn(),
      scrollOffset: 0,
    }
  },
}))

// The component reads its bucket from the file route; the test mounts it on a
// synthetic route instead.
vi.mock("@/routes/s3/$bucket/index", () => ({
  Route: { useParams: () => ({ bucket: "demo" }) },
}))

// The ownership banner's reverse-map query goes through the AWS SDK, which no
// test interceptor reaches; it is not what is under test here.
vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

interface ObjectPage {
  objects: S3Object[]
  prefixes: S3Prefix[]
  nextContinuationToken?: string
}

interface VersionPage {
  versions: S3ObjectVersion[]
  prefixes: S3Prefix[]
  isTruncated: boolean
  nextKeyMarker?: string
  nextVersionIdMarker?: string
}

const api = vi.hoisted(() => ({
  objectPages: [] as ObjectPage[],
  versionPages: [] as VersionPage[],
  versioning: "",
  /** The `token` of every listObjects call, in order. */
  listTokens: [] as (string | undefined)[],
  reset() {
    this.objectPages = []
    this.versionPages = []
    this.versioning = ""
    this.listTokens = []
  },
}))

vi.mock("@/services/api", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiModule>()
  return {
    ...actual,
    s3: {
      ...actual.s3,
      listObjects: (_bucket: string, opts: { token?: string }) => {
        api.listTokens.push(opts.token)
        // Tokens name the page that follows them: "page-2" fetches index 1.
        const index = opts.token ? Number(opts.token.replace("page-", "")) - 1 : 0
        return Promise.resolve(api.objectPages[index])
      },
      listObjectVersions: (_bucket: string, opts: { keyMarker?: string }) => {
        const index = opts.keyMarker ? Number(opts.keyMarker.replace("page-", "")) - 1 : 0
        return Promise.resolve(api.versionPages[index])
      },
      getBucketVersioning: () => Promise.resolve(api.versioning),
      getBucketLifecycle: () => Promise.resolve(null),
    },
  }
})

beforeEach(() => {
  api.reset()
  virt.options = null
})

function obj(key: string): S3Object {
  return {
    key,
    size: 100,
    lastModified: "2026-01-01T00:00:00.000Z",
    etag: "abc",
    storageClass: "STANDARD",
  }
}

function version(
  key: string,
  versionId: string,
  over: Partial<S3ObjectVersion> = {},
): S3ObjectVersion {
  return {
    key,
    versionId,
    isLatest: false,
    isDeleteMarker: false,
    lastModified: "2026-01-01T00:00:00.000Z",
    size: 10,
    etag: "abc",
    storageClass: "STANDARD",
    ...over,
  }
}

function renderBrowser() {
  return renderWithRouter(BucketDetail, { path: "/s3/$bucket", initialEntry: "/s3/demo" })
}

describe("BucketDetail > row identity", () => {
  it("keys rows by their S3 identity, not their index", async () => {
    api.objectPages = [
      {
        prefixes: [{ prefix: "logs/" }],
        objects: [obj("report.csv")],
      },
    ]
    renderBrowser()

    await waitFor(() => expect(screen.getByText("report.csv")).toBeInTheDocument())
    // A sort flip or a filter change reorders indexes; a key that names the
    // prefix or object survives it, so React moves rows instead of
    // remounting and re-measuring every one of them.
    expect(virt.options?.getItemKey).toBeDefined()
    expect(virt.options!.getItemKey!(0)).toBe("p:logs/")
    expect(virt.options!.getItemKey!(1)).toBe("o:report.csv")
  })

  it("gives every version and delete marker of one key its own row key", async () => {
    api.versioning = "Enabled"
    api.objectPages = [{ prefixes: [], objects: [obj("a.txt")] }]
    api.versionPages = [
      {
        prefixes: [],
        isTruncated: false,
        versions: [
          version("a.txt", "marker-1", { isLatest: true, isDeleteMarker: true }),
          version("a.txt", "v2"),
          version("a.txt", "v1"),
        ],
      },
    ]
    const { user } = renderBrowser()

    await user.click(
      await screen.findByRole("button", { name: "Show every version and delete marker" }),
    )
    await waitFor(() => expect(screen.getByText("Delete marker")).toBeInTheDocument())

    const keys = [0, 1, 2].map((i) => virt.options!.getItemKey!(i))
    expect(new Set(keys).size).toBe(3)
  })
})

describe("BucketDetail > paging", () => {
  it("walks the continuation tokens to the end while the user sits at the edge", async () => {
    // Every response here resolves fast enough that no isFetchingNextPage
    // render may ever commit — the token-keyed effect must chain anyway.
    api.objectPages = [
      { prefixes: [], objects: [obj("a.txt")], nextContinuationToken: "page-2" },
      { prefixes: [], objects: [obj("b.txt")], nextContinuationToken: "page-3" },
      { prefixes: [], objects: [obj("c.txt")] },
    ]
    renderBrowser()

    await waitFor(() => expect(screen.getByText("c.txt")).toBeInTheDocument())
    expect(api.listTokens).toEqual([undefined, "page-2", "page-3"])
    expect(screen.getByText("a.txt")).toBeInTheDocument()
    expect(screen.getByText("b.txt")).toBeInTheDocument()
  })
})

describe("BucketDetail > selection", () => {
  /**
   * The download is a form submission rather than a fetch — that is what lets
   * the browser stream the archive to disk instead of buffering it — so the
   * form is what a test can inspect. jsdom does not implement submit(), and
   * the component removes the form straight after calling it, so the spy is
   * also how the form is captured.
   */
  function captureArchiveSubmit() {
    return vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(() => {})
  }

  function fieldValues(form: HTMLFormElement, name: string): string[] {
    return [...form.querySelectorAll<HTMLInputElement>(`input[name="${name}"]`)].map((i) => i.value)
  }

  it("offers no selection until a row is ticked", async () => {
    api.objectPages = [{ prefixes: [], objects: [obj("a.txt")] }]
    renderBrowser()

    await waitFor(() => expect(screen.getByText("a.txt")).toBeInTheDocument())
    expect(screen.queryByRole("button", { name: /Download \.zip/ })).not.toBeInTheDocument()
  })

  it("counts and sizes what has been ticked", async () => {
    api.objectPages = [{ prefixes: [], objects: [obj("a.txt"), obj("b.txt")] }]
    const { user } = renderBrowser()

    await user.click(await screen.findByRole("checkbox", { name: "Select a.txt" }))
    expect(screen.getByText("1 object selected")).toBeInTheDocument()

    await user.click(screen.getByRole("checkbox", { name: "Select b.txt" }))
    expect(screen.getByText("2 objects selected")).toBeInTheDocument()
    // Both rows are 100 B in this fixture.
    expect(screen.getByText("200 B")).toBeInTheDocument()
  })

  it("ticks and clears the whole listing from the header box", async () => {
    api.objectPages = [{ prefixes: [], objects: [obj("a.txt"), obj("b.txt")] }]
    const { user } = renderBrowser()

    const all = await screen.findByRole("checkbox", { name: "Select every listed object" })
    await user.click(all)
    expect(screen.getByText("2 objects selected")).toBeInTheDocument()

    await user.click(all)
    expect(screen.queryByText(/objects selected/)).not.toBeInTheDocument()
  })

  it("posts the ticked keys, and the folder they were ticked in, to the archive endpoint", async () => {
    const submit = captureArchiveSubmit()
    api.objectPages = [{ prefixes: [], objects: [obj("a.txt"), obj("b.txt")] }]
    const { user } = renderBrowser()

    await user.click(await screen.findByRole("checkbox", { name: "Select a.txt" }))
    await user.click(screen.getByRole("button", { name: /Download \.zip/ }))

    expect(submit).toHaveBeenCalledOnce()
    const form = submit.mock.instances[0] as HTMLFormElement
    expect(form.method).toBe("post")
    expect(form.getAttribute("action")).toContain("/s3/buckets/demo/objects/archive")
    expect(fieldValues(form, "key")).toEqual(["a.txt"])
    expect(fieldValues(form, "prefix")).toEqual([""])
    // The keys ride in the body: a selection has no length limit, a URL does.
    expect(form.getAttribute("action")).not.toContain("a.txt")
  })

  it("drops the selection when the browser moves to another folder", async () => {
    api.objectPages = [{ prefixes: [{ prefix: "logs/" }], objects: [obj("a.txt")] }]
    const { user } = renderBrowser()

    await user.click(await screen.findByRole("checkbox", { name: "Select a.txt" }))
    expect(screen.getByText("1 object selected")).toBeInTheDocument()

    // A selection made here must not follow the user into a folder where its
    // rows are not even listed.
    await user.click(screen.getByText("logs/"))
    await waitFor(() => expect(screen.queryByText(/object selected/)).not.toBeInTheDocument())
  })

  it("offers no tick boxes in the version listing", async () => {
    api.versioning = "Enabled"
    api.objectPages = [{ prefixes: [], objects: [obj("a.txt")] }]
    api.versionPages = [
      {
        prefixes: [],
        isTruncated: false,
        versions: [version("a.txt", "v2", { isLatest: true }), version("a.txt", "v1")],
      },
    ]
    const { user } = renderBrowser()

    await user.click(
      await screen.findByRole("button", { name: "Show every version and delete marker" }),
    )
    await waitFor(() => expect(screen.getAllByText("a.txt").length).toBeGreaterThan(0))
    // Two versions of one key cannot both be a file of the same name in one
    // archive, so the version view does not offer the choice.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument()
  })
})
