import { http, HttpResponse } from "msw"
import { TestTab } from "@/features/lambda/components/test-tab"
import { server } from "@/test/server"
import { render, screen } from "@/test/render"
import type { InvokeResult } from "@/types"

const baseResult: InvokeResult = {
  statusCode: 200,
  payload: '{"ok":true}',
  functionError: null,
  logResult: null,
  executedVersion: "$LATEST",
  logGroupName: null,
  logStreamName: null,
}

/** The BFF answers the invoke with SSE — one progress event, then the result. */
function respondWithLogTail(logResult: string) {
  server.use(
    http.get("/api/lambda/functions/:name/test-events", () => HttpResponse.json([])),
    http.post(
      "/api/lambda/functions/:name/invoke-with-progress",
      () =>
        new HttpResponse(
          `event: progress\ndata: Invoking\n\n` +
            `event: result\ndata: ${JSON.stringify({ ...baseResult, logResult })}\n\n`,
          { headers: { "Content-Type": "text/event-stream" } },
        ),
    ),
  )
}

/** The bytes of `text`, base64-encoded — what `LogResult` carries on the wire. */
function encode(text: string): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(text)))
}

async function invoke() {
  const { user } = render(<TestTab name="utf8-logger" />)
  await user.click(screen.getByRole("button", { name: "Test" }))
  expect(await screen.findByText("Execution succeeded")).toBeInTheDocument()
}

describe("TestTab > log output", () => {
  it("shows a non-ASCII log tail as the handler printed it", async () => {
    respondWithLogTail(encode("café au lait ☕\nこんにちは 🌍\n"))
    await invoke()
    expect(screen.getByText(/café au lait ☕/)).toBeInTheDocument()
  })

  it("says the log output is unavailable when the log tail is not base64", async () => {
    respondWithLogTail("@@@ not base64 @@@")
    await invoke()
    expect(screen.getByText(/Log output unavailable/)).toBeInTheDocument()
  })

  it("still shows the result panel when the log tail is not base64", async () => {
    respondWithLogTail("@@@ not base64 @@@")
    await invoke()
    expect(screen.getByText("Status: 200")).toBeInTheDocument()
  })
})
