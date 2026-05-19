import type { ComponentProps } from "react"
import { fireEvent, render, screen } from "@testing-library/react"
import { ServiceNode, type ServiceNodeData } from "./topology-nodes"

const navigateMock = vi.fn()
const setEndpointMock = vi.fn()
const receiveMessagesMock = vi.fn()

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}))

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>()
  return {
    ...actual,
    useQuery: vi.fn(() => ({ data: [] })),
    queryOptions: (value: unknown) => value,
  }
})

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

describe("ServiceNode ECR interactions", () => {
  beforeEach(() => {
    navigateMock.mockReset()
    setEndpointMock.mockReset()
    receiveMessagesMock.mockReset()

    Object.defineProperty(globalThis.navigator, "clipboard", {
      value: {
        writeText: vi.fn().mockResolvedValue(undefined),
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

  it("copies repository URI without triggering navigation", () => {
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

    fireEvent.click(screen.getByTitle("Copy repository URI"))

    expect(navigateMock).not.toHaveBeenCalled()
    expect(globalThis.navigator.clipboard.writeText).toHaveBeenCalledWith(
      "localhost:5111/backend/api",
    )
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
    expect(receiveMessagesMock).not.toHaveBeenCalled()
  })
})
