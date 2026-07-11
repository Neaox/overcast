import type { ComponentProps } from "react"
import { fireEvent, render, screen } from "@/test/render"
import type { SQSMessage } from "@/types"
import { EventType } from "@/services/event-types"
import {
  ServiceNode,
  type ServiceNodeData,
} from "./topology-nodes"
import {
  computeSqsVisualMessages,
  createSqsVisualMessagesState,
  type SqsVisualMessagesState,
} from "./sqs-visual-messages"

const navigateMock = vi.fn()
const setEndpointMock = vi.fn()
const receiveMessagesMock = vi.fn()
const clipboardWriteTextMock = vi.fn()

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}))

vi.mock("@xyflow/react", () => ({
  Handle: () => <div data-testid="handle" />,
  Position: { Left: "left", Right: "right" },
  useNodeId: () => "us-east-1::ecr::backend/api",
}))

vi.mock("@/hooks/use-endpoint", () => ({
  useEndpoint: () => ({ baseUrl: "http://localhost:4566", region: "us-east-1" }),
}))

vi.mock("@/services/endpoint-store", () => ({
  endpointStore: {
    set: (...args: unknown[]) => setEndpointMock(...args),
    get: () => ({ baseUrl: "http://localhost:4566", region: "us-east-1" }),
    getKeys: () => ["endpoint", "http://localhost:4566", "us-east-1"],
  },
}))

vi.mock("@/hooks/use-event-stream", () => ({
  useEventStream: () => ({ events: [] }),
}))

vi.mock("@/services/api", () => ({
  sqs: {
    receiveMessages: (...args: unknown[]) => receiveMessagesMock(...args),
  },
}))

vi.mock("@/features/cloudwatch/logs/data", () => ({
  logsStreamsQueryOptions: () => ({ queryKey: ["logs"], queryFn: vi.fn() }),
}))

type ServiceNodeProps = ComponentProps<typeof ServiceNode>

function makeServiceNodeProps(data: ServiceNodeData): ServiceNodeProps {
  return {
    id: "us-east-1::ecr::backend/api",
    data,
    selected: false,
    dragging: false,
    draggable: false,
    selectable: true,
    deletable: false,
    zIndex: 0,
    type: "service",
    isConnectable: false,
    xPos: 0,
    yPos: 0,
    positionAbsoluteX: 0,
    positionAbsoluteY: 0,
  } as ServiceNodeProps
}

function makeSqsMessage(overrides: Partial<SQSMessage> = {}): SQSMessage {
  return {
    messageId: "msg-1",
    receiptHandle: "receipt-1",
    body: "hello",
    md5OfBody: "5d41402abc4b2a76b9719d911017c592",
    attributes: { SentTimestamp: "0" },
    messageAttributes: {},
    inflight: false,
    delayed: false,
    visibleAfter: 0,
    approximateReceiveCount: 0,
    ...overrides,
  }
}

function computeVisualMessages({
  liveMessages,
  state = createSqsVisualMessagesState(),
  nowMs = 0,
  sqsEvents = [],
  ghosts = new Map(),
}: {
  liveMessages: SQSMessage[]
  state?: SqsVisualMessagesState
  nowMs?: number
  sqsEvents?: Parameters<typeof computeSqsVisualMessages>[0]["sqsEvents"]
  ghosts?: Parameters<typeof computeSqsVisualMessages>[0]["ghosts"]
}) {
  vi.setSystemTime(nowMs)
  return computeSqsVisualMessages({
    queueName: "queue-a",
    liveMessages,
    ghosts,
    sqsEvents,
    nowMs,
    state,
  })
}

describe("computeSqsVisualMessages", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(0)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it("keeps a new in-flight message visible for the minimum dwell window", () => {
    const inflight = makeSqsMessage({ inflight: true, approximateReceiveCount: 1 })

    const first = computeVisualMessages({ liveMessages: [inflight], nowMs: 0 })
    expect(first.messages).toHaveLength(1)
    expect(first.messages[0].visualPhase).toBe("visible")

    const beforeDwell = computeVisualMessages({
      liveMessages: [inflight],
      state: first.state,
      nowMs: 999,
    })
    expect(beforeDwell.messages[0].visualPhase).toBe("visible")

    const afterDwell = computeVisualMessages({
      liveMessages: [inflight],
      state: beforeDwell.state,
      nowMs: 1_000,
    })
    expect(afterDwell.messages[0].visualPhase).toBe("inflight")
  })

  it("preserves the ghost exit sequence after a visible message is deleted", () => {
    const message = makeSqsMessage()
    const first = computeVisualMessages({ liveMessages: [message], nowMs: 0 })
    const ghosts = new Map([[message.messageId, { item: message, deletedAt: 500 }]])

    const justDeleted = computeVisualMessages({
      liveMessages: [],
      ghosts,
      state: first.state,
      nowMs: 500,
    })
    expect(justDeleted.messages[0]).toMatchObject({ isGhost: true, visualPhase: "visible" })

    const exiting = computeVisualMessages({
      liveMessages: [],
      ghosts,
      state: justDeleted.state,
      nowMs: 1_000,
    })
    expect(exiting.messages[0]).toMatchObject({ isGhost: true, visualPhase: "inflight" })

    const done = computeVisualMessages({
      liveMessages: [],
      ghosts,
      state: exiting.state,
      nowMs: 2_100,
    })
    expect(done.messages[0]).toMatchObject({ isGhost: true, visualPhase: "done" })
  })

  it("reprocesses events when the SSE event history shrinks", () => {
    const live = makeSqsMessage({ inflight: true, approximateReceiveCount: 1 })
    const staleState: SqsVisualMessagesState = {
      ...createSqsVisualMessagesState(),
      eventCursor: 3,
    }

    const result = computeVisualMessages({
      liveMessages: [live],
      state: staleState,
      nowMs: 1_000,
      sqsEvents: [
        {
          type: EventType.sqs.MessageVisible,
          time: new Date(1_000).toISOString(),
          payload: { queueName: "queue-a", messageId: live.messageId },
        },
      ],
    })

    expect(result.state.eventCursor).toBe(1)
    expect(result.messages[0].visualPhase).toBe("visible")
  })
})

describe("ServiceNode ECR interactions", () => {
  beforeEach(() => {
    navigateMock.mockReset()
    setEndpointMock.mockReset()
    receiveMessagesMock.mockReset()
    receiveMessagesMock.mockResolvedValue([])
    clipboardWriteTextMock.mockReset()
    clipboardWriteTextMock.mockResolvedValue(undefined)

    Object.defineProperty(globalThis.navigator, "clipboard", {
      value: {
        writeText: clipboardWriteTextMock,
      },
      configurable: true,
    })
  })

  it("navigates to repository detail when clicking the ECR node", () => {
    render(
      <ServiceNode
        {...makeServiceNodeProps({
          service: "ecr",
          label: "backend/api",
          repositoryUri: "localhost:5111/backend/api",
          region: "us-east-1",
        })}
      />,
    )

    const nodeButton = screen.getByText("backend/api").closest('[role="button"]')
    if (!nodeButton) {
      throw new Error("expected node button wrapper")
    }
    fireEvent.click(nodeButton)

    expect(navigateMock).toHaveBeenCalledWith({
      to: "/ecr/$repositoryName",
      params: { repositoryName: "backend/api" },
      search: undefined,
    })
    expect(setEndpointMock).not.toHaveBeenCalled()
  })

  it("copies repository URI without triggering navigation", async () => {
    const { user } = render(
      <ServiceNode
        {...makeServiceNodeProps({
          service: "ecr",
          label: "backend/api",
          repositoryUri: "localhost:5111/backend/api",
          region: "us-east-1",
        })}
      />,
    )
    const writeTextSpy = vi.spyOn(globalThis.navigator.clipboard, "writeText")

    await user.click(screen.getByTitle("Copy repository URI"))

    expect(navigateMock).not.toHaveBeenCalled()
    expect(writeTextSpy).toHaveBeenCalledWith("localhost:5111/backend/api")
  })

  it("event pulse counters remain visual-only for ECR nodes", () => {
    const { rerender } = render(
      <ServiceNode
        {...makeServiceNodeProps({
          service: "ecr",
          label: "backend/api",
          repositoryUri: "localhost:5111/backend/api",
          eventCount: 1,
          writeCount: 11,
        })}
      />,
    )

    expect(screen.getByText("1")).toBeInTheDocument()

    rerender(
      <ServiceNode
        {...makeServiceNodeProps({
          service: "ecr",
          label: "backend/api",
          repositoryUri: "localhost:5111/backend/api",
          eventCount: 2,
          writeCount: 12,
        })}
      />,
    )

    expect(screen.getByText("2")).toBeInTheDocument()
    expect(navigateMock).not.toHaveBeenCalled()
  })
})
