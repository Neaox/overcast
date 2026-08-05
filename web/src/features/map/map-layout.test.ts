/**
 * map-layout.test — verifies buildLayoutNodes never produces overlapping
 * boxes when node types have very different (and in Lambda's case,
 * data-dependent) dimensions.
 *
 * This is a pure-function test over the dagre-backed layout: no rendering,
 * no React Flow runtime — just geometry over the returned Node[] positions.
 */
import { describe, expect, it } from "vitest"
import type { Node } from "@xyflow/react"
import { buildLayoutNodes, NODE_WIDTH, NODE_HEIGHT, lambdaGroupHeight } from "./map-layout"
import { SQS_NODE_EXPANDED_H, LOGS_NODE_EXPANDED_H } from "./topology-nodes"
import type { TopologyEdge, TopologyNode } from "@/types"

interface Rect {
  id: string
  x: number
  y: number
  w: number
  h: number
}

/** Resolves every node's ABSOLUTE position by walking its parentId chain. */
function absoluteRects(nodes: Node[]): Rect[] {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  function resolve(n: Node): { x: number; y: number } {
    if (!n.parentId) return { x: n.position.x, y: n.position.y }
    const parent = byId.get(n.parentId)
    if (!parent) return { x: n.position.x, y: n.position.y }
    const p = resolve(parent)
    return { x: p.x + n.position.x, y: p.y + n.position.y }
  }
  return nodes.map((n) => {
    const { x, y } = resolve(n)
    return { id: n.id, x, y, w: n.width ?? 0, h: n.height ?? 0 }
  })
}

/** True if two axis-aligned rects overlap (touching edges is not an overlap). */
function overlaps(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
}

/**
 * Asserts no two rects in the same list overlap, EXCLUDING ancestor/descendant
 * pairs (a group container is expected to visually contain its children).
 */
function assertNoOverlaps(nodes: Node[]) {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  function isAncestor(maybeAncestorId: string, n: Node): boolean {
    let cur: Node | undefined = n
    while (cur?.parentId) {
      if (cur.parentId === maybeAncestorId) return true
      cur = byId.get(cur.parentId)
    }
    return false
  }

  const rects = absoluteRects(nodes)
  for (let i = 0; i < rects.length; i++) {
    for (let j = i + 1; j < rects.length; j++) {
      const a = rects[i]
      const b = rects[j]
      if (isAncestor(a.id, byId.get(b.id)!) || isAncestor(b.id, byId.get(a.id)!)) continue
      if (overlaps(a, b)) {
        throw new Error(
          `Nodes overlap: ${a.id} (${a.x},${a.y} ${a.w}x${a.h}) vs ${b.id} (${b.x},${b.y} ${b.w}x${b.h})`,
        )
      }
    }
  }
}

/** Builds the same per-type size overrides map-page.tsx computes for real data. */
function overridesFor(nodes: TopologyNode[], lambdaInstanceCounts: Record<string, number>) {
  const overrides: Record<string, { width: number; height: number }> = {}
  for (const n of nodes) {
    if (n.service === "lambda") {
      const count = lambdaInstanceCounts[n.label] ?? 0
      if (count > 0) overrides[n.id] = { width: NODE_WIDTH, height: lambdaGroupHeight(count) }
    } else if (
      n.service === "sqs" &&
      (n.approximateNumberOfMessages ?? 0) + (n.approximateNumberOfMessagesNotVisible ?? 0) > 0
    ) {
      overrides[n.id] = { width: NODE_WIDTH, height: SQS_NODE_EXPANDED_H }
    } else if (n.service === "logs") {
      overrides[n.id] = { width: NODE_WIDTH, height: LOGS_NODE_EXPANDED_H }
    }
  }
  return overrides
}

describe("buildLayoutNodes", () => {
  it("preserves ECS identity and health metadata for rendering and navigation", () => {
    const ecsService: TopologyNode = {
      id: "ecs-service",
      service: "ecs",
      label: "website",
      region: "ap-southeast-2",
      ecsResourceType: "service",
      clusterName: "demo",
      desiredCount: 2,
      runningCount: 1,
    }

    const result = buildLayoutNodes([ecsService])

    expect(result.find((node) => node.id === ecsService.id)?.data).toEqual(
      expect.objectContaining({
        ecsResourceType: "service",
        clusterName: "demo",
        desiredCount: 2,
        runningCount: 1,
      }),
    )
  })

  it("does not overlap nodes of mixed types with very different heights", () => {
    const region = "us-east-1"
    const nodes: TopologyNode[] = [
      // Several SQS queues, some with messages (tall, fixed max-height box).
      { id: "sqs-1", service: "sqs", label: "queue-one", region, approximateNumberOfMessages: 40 },
      {
        id: "sqs-2",
        service: "sqs",
        label: "queue-two",
        region,
        approximateNumberOfMessagesNotVisible: 5,
      },
      { id: "sqs-3", service: "sqs", label: "queue-three", region },
      // S3 buckets — plain default-sized nodes.
      { id: "s3-1", service: "s3", label: "bucket-one", region },
      { id: "s3-2", service: "s3", label: "bucket-two", region },
      // A VPC with an EC2 instance inside it (byVpc grouping).
      {
        id: "vpc-1",
        service: "vpc",
        label: "vpc-abc",
        region,
        cidrBlock: "10.0.0.0/16",
        subnetCount: 2,
      },
      { id: "ec2-1", service: "ec2", label: "i-abc123", region, vpcId: "vpc-1" },
      // Lambda functions with a wide spread of concurrent-instance counts —
      // one of these would previously grow unbounded tall.
      { id: "lambda-1", service: "lambda", label: "fn-quiet", region },
      { id: "lambda-2", service: "lambda", label: "fn-busy", region },
      { id: "lambda-3", service: "lambda", label: "fn-very-busy", region },
      // CloudWatch Logs group (also a tall, fixed max-height box).
      { id: "logs-1", service: "logs", label: "/aws/lambda/fn-busy", region },
    ]
    const edges: TopologyEdge[] = [
      { id: "e1", source: "lambda-2", target: "sqs-1", type: "invoke" },
      { id: "e2", source: "lambda-3", target: "s3-1", type: "invoke" },
    ]

    const lambdaInstanceCounts = {
      "fn-quiet": 1,
      "fn-busy": 4,
      // Far beyond LAMBDA_GROUP_MAX_VISIBLE — used to grow unbounded before capping.
      "fn-very-busy": 25,
    }
    const overrides = overridesFor(nodes, lambdaInstanceCounts)

    const result = buildLayoutNodes(nodes, edges, overrides, region)

    // Sanity: every leaf resource node made it into the result.
    for (const n of nodes) {
      expect(result.some((r) => r.id === n.id)).toBe(true)
    }

    assertNoOverlaps(result)
  })

  it("caps Lambda group height regardless of instance count", () => {
    // Height must stop growing once past LAMBDA_GROUP_MAX_VISIBLE rows —
    // otherwise a single busy function could grow tall enough to overlap
    // whatever dagre placed next to it.
    const capped = lambdaGroupHeight(4)
    expect(lambdaGroupHeight(25)).toBe(capped)
    expect(lambdaGroupHeight(1000)).toBe(capped)
    // ...but still shrinks for the (much more common) low-concurrency case.
    expect(lambdaGroupHeight(1)).toBeLessThan(capped)
  })

  it("still fits plain default-sized nodes without an override", () => {
    // Guards the NODE_WIDTH/NODE_HEIGHT re-export used by this test file.
    expect(NODE_WIDTH).toBeGreaterThan(0)
    expect(NODE_HEIGHT).toBeGreaterThan(0)
  })
})
