import { http, HttpResponse } from "msw"
import { render, screen, waitFor } from "@/test/render"
import { server } from "@/test/server"
import { endpointStore } from "@/services/endpoint-store"
import { RegionElsewhereNotice } from "./region-elsewhere-notice"

/**
 * Answers the preflight call, and counts it — the absence tests need to know
 * the check has actually run before they can claim it said nothing, or they
 * would pass just as happily against a component that never asked.
 */
function answerWith(respond: () => Response) {
  const calls = { count: 0 }
  server.use(
    http.get("/api/preflight/region", () => {
      calls.count++
      return respond()
    }),
  )
  return calls
}

function advisory(elsewhere: { region: string; count: number }[], region = "us-east-1") {
  return answerWith(() =>
    HttpResponse.json({ kind: "cloudformation-stacks", region, count: 0, elsewhere }),
  )
}

describe("RegionElsewhereNotice", () => {
  it("names the region that has the resources, and how many", async () => {
    advisory([{ region: "ap-southeast-2", count: 3 }])

    render(<RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />)

    await waitFor(() =>
      expect(screen.getByText(/No stacks in/)).toHaveTextContent(
        "No stacks in us-east-1. There are 3 in ap-southeast-2.",
      ),
    )
  })

  // The anti-cry-wolf case. An empty account is far and away the commonest
  // reason a list page is empty, and a notice that appears then is one people
  // learn to look past — taking every other preflight check with it.
  it("renders nothing when there is nothing in any other region", async () => {
    const calls = advisory([])

    render(<RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />)

    await waitFor(() => expect(calls.count).toBe(1))
    expect(screen.queryByText(/No stacks in/)).not.toBeInTheDocument()
  })

  // A diagnostic that fails is not a diagnosis. An older emulator has no such
  // route, and the page it is embedded in must not grow an error banner over
  // a check the user never asked to run.
  it("stays silent when the check itself fails", async () => {
    const calls = answerWith(() => new HttpResponse(null, { status: 500 }))

    render(<RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />)

    await waitFor(() => expect(calls.count).toBe(1))
    expect(screen.queryByText(/No stacks in/)).not.toBeInTheDocument()
  })

  it("agrees with the total: one resource reads 'There is'", async () => {
    advisory([{ region: "eu-west-1", count: 1 }])

    render(<RegionElsewhereNotice kind="sqs-queues" noun="queues" />)

    await waitFor(() =>
      expect(screen.getByText(/No queues in/)).toHaveTextContent(
        "No queues in us-east-1. There is 1 in eu-west-1.",
      ),
    )
  })

  it("lists every region that holds some", async () => {
    advisory([
      { region: "ap-southeast-2", count: 3 },
      { region: "eu-west-1", count: 1 },
    ])

    render(<RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />)

    await waitFor(() =>
      expect(screen.getByText(/No stacks in/)).toHaveTextContent(
        "No stacks in us-east-1. There are 3 in ap-southeast-2 and 1 in eu-west-1.",
      ),
    )
  })

  // The advisory's whole point is to end the search, so it has to be able to
  // finish the job rather than telling the reader to go and find the combobox.
  it("switches the console to the named region", async () => {
    advisory([{ region: "ap-southeast-2", count: 3 }])
    const before = endpointStore.get()
    try {
      const { user } = render(<RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />)

      await user.click(await screen.findByRole("button", { name: "Switch to ap-southeast-2" }))

      expect(endpointStore.get().region).toBe("ap-southeast-2")
    } finally {
      endpointStore.set(before)
    }
  })
})
