/*
 * The group page's DescribeLogStreams answering ResourceNotFoundException
 * means the group itself is missing from the selected region. It used to
 * render the ordinary "No log streams" empty state — the wrong-region trap
 * reads as an application that never logged, when the real story is the
 * region selector pointing away from where the group lives.
 */
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, renderWithRouter, screen } from "@/test/render"
import { LogGroupDetail } from "./log-group-detail"

const backend = vi.hoisted(() => ({
  streamsError: null as Error | null,
}))

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: (command: { constructor: { name: string } }) => {
        if (command.constructor.name === "DescribeLogStreamsCommand") {
          if (backend.streamsError) return Promise.reject(backend.streamsError)
          return Promise.resolve({ logStreams: [] })
        }
        if (command.constructor.name === "DescribeLogGroupsCommand") {
          return Promise.resolve({ logGroups: [] })
        }
        return Promise.resolve({ tags: {} })
      },
    }),
  },
}))

const GROUP = "/aws/lambda/checkout"

function renderDetail() {
  return renderWithRouter(() => <LogGroupDetail groupName={GROUP} />, {
    queryClient: createTestQueryClient(),
  })
}

describe("LogGroupDetail > resource not found", () => {
  it("shows a not-found state naming the group and region, not the empty-streams state", async () => {
    const error = new Error(`The specified log group does not exist: ${GROUP}`)
    error.name = "ResourceNotFoundException"
    backend.streamsError = error

    renderDetail()

    expect(await screen.findByText("Log group not found")).toBeInTheDocument()
    expect(
      screen.getByText(/No log group named .*checkout.* exists in us-east-1/),
    ).toBeInTheDocument()
    expect(screen.queryByText("No log streams")).not.toBeInTheDocument()
  })

  it("keeps the ordinary empty state for a group that exists and has no streams", async () => {
    backend.streamsError = null

    renderDetail()

    expect(await screen.findByText("No log streams")).toBeInTheDocument()
    expect(screen.queryByText("Log group not found")).not.toBeInTheDocument()
  })
})
