/**
 * Create-bucket dialog: SDK-input mapping and namespace-preview behaviour.
 *
 * The AWS SDK is not something MSW can intercept — its own HTTP handler is
 * not the global fetch the interceptor patches — so the command it builds is
 * recorded at the client boundary instead, the same pattern as
 * bucket-versioning.test.tsx.
 */
import { renderWithData, screen, waitFor } from "@/test/render"
import { s3BucketsQueryOptions } from "@/features/s3/data"
import { BucketList } from "./bucket-list"

const nav = vi.hoisted(() => ({ navigate: vi.fn() }))

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => nav.navigate,
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => <button type="button">Docs</button>,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/features/debug/raw-state-link", () => ({
  RawStateLink: () => null,
}))

const clients = vi.hoisted(() => ({
  sent: [] as { name: string; input: Record<string, unknown> }[],
  fail: null as Error | null,
  account: "123456789012",
  reset() {
    this.sent = []
    this.fail = null
    this.account = "123456789012"
  },
}))

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    s3: () => ({
      send: (command: { constructor: { name: string }; input: Record<string, unknown> }) => {
        clients.sent.push({ name: command.constructor.name, input: command.input })
        return clients.fail ? Promise.reject(clients.fail) : Promise.resolve({})
      },
    }),
    sts: () => ({
      // GetCallerIdentityCommand — only the field the dialog reads is stubbed.
      send: () => Promise.resolve({ Account: clients.account }),
    }),
  },
}))

beforeEach(() => clients.reset())

function createBucketInput() {
  return clients.sent.find((c) => c.name === "CreateBucketCommand")?.input
}

async function openDialog() {
  const rendered = renderWithData(<BucketList />, [[s3BucketsQueryOptions().queryKey, []]])
  // Two "Create bucket" triggers exist while the list is empty: the header
  // action and the empty-state action. Either opens the same dialog.
  await rendered.user.click(screen.getAllByRole("button", { name: "Create bucket" })[0])
  return rendered
}

describe("CreateBucketDialog > global namespace (default)", () => {
  it("submits the raw name with no BucketNamespace", async () => {
    const { user } = await openDialog()
    // The required-field asterisk is a sibling span with no separating space,
    // so the label's full text is "Bucket name*" — match the prefix instead.
    // The `$` anchor still excludes "Bucket name prefix" (account-regional mode).
    await user.type(screen.getByLabelText(/^Bucket name\*?$/), "my-bucket")
    await user.click(screen.getByRole("button", { name: "Create" }))

    await waitFor(() => expect(createBucketInput()).toBeDefined())
    expect(createBucketInput()).toEqual({ Bucket: "my-bucket" })
  })
})

describe("CreateBucketDialog > account regional namespace", () => {
  it("shows a live full-name preview built from the caller identity and region", async () => {
    const { user } = await openDialog()
    await user.click(screen.getByRole("radio", { name: /Account regional/ }))
    await user.type(screen.getByLabelText(/Bucket name prefix/), "logs")

    expect(await screen.findByText("logs-123456789012-us-east-1-an")).toBeInTheDocument()
  })

  it("submits the full suffixed name plus BucketNamespace: account-regional", async () => {
    const { user } = await openDialog()
    await user.click(screen.getByRole("radio", { name: /Account regional/ }))
    await user.type(screen.getByLabelText(/Bucket name prefix/), "logs")
    await user.click(screen.getByRole("button", { name: "Create" }))

    await waitFor(() => expect(createBucketInput()).toBeDefined())
    expect(createBucketInput()).toEqual({
      Bucket: "logs-123456789012-us-east-1-an",
      BucketNamespace: "account-regional",
    })
  })

  it("rejects a prefix that pushes the full name past 63 characters", async () => {
    const { user } = await openDialog()
    await user.click(screen.getByRole("radio", { name: /Account regional/ }))
    // The us-east-1/123456789012 suffix is 30 chars; 40 more pushes the total past 63.
    await user.type(screen.getByLabelText(/Bucket name prefix/), "a".repeat(40))

    expect(await screen.findByRole("button", { name: "Create" })).toBeDisabled()
  })

  it("requires a non-empty prefix", async () => {
    const { user } = await openDialog()
    await user.click(screen.getByRole("radio", { name: /Account regional/ }))
    await user.click(screen.getByLabelText(/Bucket name prefix/))
    await user.tab()

    expect(await screen.findByText(/Prefix is required/)).toBeInTheDocument()
  })
})
