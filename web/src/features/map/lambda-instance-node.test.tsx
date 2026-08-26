import { render, screen } from "@/test/render"
import type { LambdaInstance } from "@/types"
import { LambdaInstanceCard } from "./lambda-instance-node"

vi.mock("@/hooks/use-event-stream", () => ({
  useEventStream: () => ({ events: [] }),
}))

function makeInstance(overrides: Partial<LambdaInstance> = {}): LambdaInstance {
  return {
    instanceId: "instance-abc12345",
    functionName: "my-func",
    status: "idle",
    startedAt: 0,
    lastUsed: 0,
    expiresAt: 15 * 60 * 1000,
    logGroup: "/aws/lambda/my-func",
    logStream: "2026/08/26/[$LATEST]abc",
    triggerEvent: "",
    memoryUsedMB: 32,
    cpuPercent: 1,
    ...overrides,
  }
}

describe("LambdaInstanceCard", () => {
  it("shows the proactive pill when initOrigin is proactive", () => {
    render(<LambdaInstanceCard instance={makeInstance({ initOrigin: "proactive" })} />)
    expect(screen.getByText("proactive")).toBeInTheDocument()
  })

  it("does not show the proactive pill for on-demand instances", () => {
    render(<LambdaInstanceCard instance={makeInstance({ initOrigin: "on-demand" })} />)
    expect(screen.queryByText("proactive")).not.toBeInTheDocument()
  })

  it("does not show the proactive pill when initOrigin is absent", () => {
    render(<LambdaInstanceCard instance={makeInstance()} />)
    expect(screen.queryByText("proactive")).not.toBeInTheDocument()
  })

  it("still shows the prov pill for provisioned instances", () => {
    render(<LambdaInstanceCard instance={makeInstance({ provisioned: true, expiresAt: 0 })} />)
    expect(screen.getByText("prov")).toBeInTheDocument()
  })

  it("shows both prov and proactive pills when an instance is provisioned-origin", () => {
    render(
      <LambdaInstanceCard
        instance={makeInstance({ provisioned: true, expiresAt: 0, initOrigin: "provisioned" })}
      />,
    )
    expect(screen.getByText("prov")).toBeInTheDocument()
    // "provisioned" initOrigin is not "proactive" — no proactive pill expected.
    expect(screen.queryByText("proactive")).not.toBeInTheDocument()
  })

  it("shows the mapped eviction-reason label on a ghost card with a known reason", () => {
    render(
      <LambdaInstanceCard
        instance={makeInstance({ status: "idle" })}
        isGhost
        deletedAt={0}
        evictedReason="idle-ttl"
      />,
    )
    expect(screen.getByText("idle timeout")).toBeInTheDocument()
    // The raw status badge is replaced by the reason label.
    expect(screen.queryByText("idle")).not.toBeInTheDocument()
  })

  it("maps each evicted reason to its human label", () => {
    const cases: Array<[LambdaInstance["evictedReason"], string]> = [
      ["idle-ttl", "idle timeout"],
      ["config-change", "config changed"],
      ["function-deleted", "function deleted"],
      ["container-died", "container died"],
      ["unhealthy", "unhealthy"],
      ["surplus", "surplus"],
      ["memory-pressure", "memory pressure"],
      ["shutdown", "shutdown"],
    ]
    for (const [reason, label] of cases) {
      const { unmount } = render(
        <LambdaInstanceCard
          instance={makeInstance()}
          isGhost
          deletedAt={0}
          evictedReason={reason}
        />,
      )
      expect(screen.getByText(label)).toBeInTheDocument()
      unmount()
    }
  })

  it("falls back to the raw status badge on a ghost card with no known reason", () => {
    render(<LambdaInstanceCard instance={makeInstance({ status: "idle" })} isGhost deletedAt={0} />)
    expect(screen.getByText("idle")).toBeInTheDocument()
  })

  it("does not show an eviction-reason label on a live (non-ghost) card", () => {
    render(
      <LambdaInstanceCard
        instance={makeInstance({ status: "idle" })}
        evictedReason="idle-ttl"
        isGhost={false}
      />,
    )
    expect(screen.queryByText("idle timeout")).not.toBeInTheDocument()
    expect(screen.getByText("idle")).toBeInTheDocument()
  })
})
