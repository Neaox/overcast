import { renderWithData, screen } from "@/test/render"
import { s3BucketVersioningQueryOptions } from "@/features/s3/data"
import type { S3VersioningStatus } from "@/types"
import { BucketVersioningPanel } from "./bucket-versioning"

/**
 * The S3 client the panel writes through. MSW cannot stand in here: the AWS
 * SDK's own HTTP handler is not the global fetch the interceptor patches, so
 * the command is asserted where it is built instead.
 */
const s3Client = vi.hoisted(() => ({
  sent: [] as { name: string; input: Record<string, unknown> }[],
  fail: null as Error | null,
  reset() {
    this.sent = []
    this.fail = null
  },
}))

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    s3: () => ({
      send: (command: { constructor: { name: string }; input: Record<string, unknown> }) => {
        s3Client.sent.push({ name: command.constructor.name, input: command.input })
        return s3Client.fail ? Promise.reject(s3Client.fail) : Promise.resolve({})
      },
    }),
  },
}))

const BUCKET = "assets"

beforeEach(() => s3Client.reset())

function renderPanel(status: S3VersioningStatus) {
  return renderWithData(<BucketVersioningPanel bucket={BUCKET} />, [
    [s3BucketVersioningQueryOptions(BUCKET).queryKey, status],
  ])
}

/** Opens the edit dialog and returns the `user` that opened it. */
async function openDialog(status: S3VersioningStatus) {
  const { user } = renderPanel(status)
  await user.click(screen.getByRole("button", { name: "Edit versioning" }))
  return user
}

function putVersioningInput() {
  return s3Client.sent.find((c) => c.name === "PutBucketVersioningCommand")?.input
}

describe("BucketVersioningPanel > status", () => {
  it("reports a versioned bucket as enabled", () => {
    renderPanel("Enabled")
    expect(screen.getByText("Enabled")).toBeInTheDocument()
  })

  it("reports a bucket that has never been versioned as not enabled", () => {
    renderPanel("")
    expect(screen.getByText("Not enabled")).toBeInTheDocument()
  })

  it("says a suspended bucket keeps the versions it already stored", () => {
    renderPanel("Suspended")
    expect(screen.getByText(/Versions already stored are kept/)).toBeInTheDocument()
  })
})

describe("BucketVersioningPanel > editing", () => {
  it("explains that suspending is not the same as turning versioning off", async () => {
    await openDialog("Enabled")
    expect(await screen.findByText(/not the same as turning versioning off/)).toBeInTheDocument()
  })

  it("cannot save the state the bucket is already in", async () => {
    await openDialog("Enabled")
    expect(await screen.findByRole("button", { name: "Save" })).toBeDisabled()
  })

  it("suspends versioning when Suspended is chosen", async () => {
    const user = await openDialog("Enabled")
    await user.click(await screen.findByRole("radio", { name: /Suspended/ }))
    await user.click(screen.getByRole("button", { name: "Save" }))

    expect(await screen.findByText("Versioning suspended")).toBeInTheDocument()
    expect(putVersioningInput()).toEqual({
      Bucket: BUCKET,
      VersioningConfiguration: { Status: "Suspended" },
    })
  })

  it("enables versioning on a bucket that has never been versioned", async () => {
    const user = await openDialog("")
    await user.click(screen.getByRole("button", { name: "Save" }))

    expect(await screen.findByText("Versioning enabled")).toBeInTheDocument()
    expect(putVersioningInput()).toEqual({
      Bucket: BUCKET,
      VersioningConfiguration: { Status: "Enabled" },
    })
  })

  it("reports a failed save rather than closing the dialog", async () => {
    s3Client.fail = new Error("Access Denied")
    const user = await openDialog("Enabled")
    await user.click(await screen.findByRole("radio", { name: /Suspended/ }))
    await user.click(screen.getByRole("button", { name: "Save" }))

    expect(await screen.findByText("Save failed")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument()
  })
})
