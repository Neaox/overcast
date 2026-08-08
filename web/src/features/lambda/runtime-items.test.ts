import { describe, expect, it } from "vitest"

import { buildRuntimeItems } from "./runtime-items"
import type { LambdaRuntimeInfo } from "@/types"

function runtime(overrides: Partial<LambdaRuntimeInfo> & { id: string }): LambdaRuntimeInfo {
  return {
    name: overrides.id,
    family: "Node.js",
    defaultHandler: "index.handler",
    deprecated: false,
    createBlocked: false,
    updateBlocked: false,
    supported: true,
    ...overrides,
  }
}

describe("buildRuntimeItems", () => {
  it("offers only runtimes CreateFunction will accept", () => {
    const items = buildRuntimeItems([
      runtime({ id: "nodejs24.x" }),
      // Modeled but with no execution image — CreateFunction answers 501.
      runtime({ id: "go1.x", family: "Go", supported: false }),
      // Past AWS's block-create date — CreateFunction answers 400.
      runtime({ id: "nodejs14.x", deprecated: true, createBlocked: true, updateBlocked: true }),
    ])

    expect(items.map((i) => i.value)).toEqual(["nodejs24.x"])
  })

  it("keeps deprecated-but-deployable runtimes, labelled and sorted last in their family", () => {
    const items = buildRuntimeItems([
      runtime({ id: "nodejs20.x", deprecated: true }),
      runtime({ id: "nodejs24.x" }),
      runtime({ id: "python3.14", family: "Python", defaultHandler: "lambda_function.handler" }),
    ])

    expect(items.map((i) => i.value)).toEqual(["nodejs24.x", "nodejs20.x", "python3.14"])
    expect(items.find((i) => i.value === "nodejs20.x")?.deprecated).toBe(true)
    expect(items.find((i) => i.value === "python3.14")?.defaultHandler).toBe(
      "lambda_function.handler",
    )
  })

  it("groups a family that the AWS model order splits apart", () => {
    // The pinned model declares java8.al2023/java11.al2023/java17.al2023 after
    // the custom runtimes, so the raw catalog order interleaves families. The
    // combobox draws a header on every family change, so each family must end
    // up contiguous.
    const items = buildRuntimeItems([
      runtime({ id: "java21", family: "Java" }),
      runtime({ id: "provided.al2023", family: "Custom runtime" }),
      runtime({ id: "java17.al2023", family: "Java" }),
    ])

    expect(items.map((i) => i.group)).toEqual(["Java", "Java", "Custom runtime"])
    expect(items.map((i) => i.value)).toEqual(["java21", "java17.al2023", "provided.al2023"])
  })
})
