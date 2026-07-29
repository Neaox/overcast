import { describe, expect, it } from "vitest"
import type { OriginGroups } from "@aws-sdk/client-cloudfront"

import { mapOriginGroups } from "./cloudfront"

describe("mapOriginGroups", () => {
  it("preserves member order, because the first member is the primary origin", () => {
    const groups: OriginGroups = {
      Quantity: 1,
      Items: [
        {
          Id: "group-1",
          Members: {
            Quantity: 2,
            Items: [{ OriginId: "origin-a" }, { OriginId: "origin-b" }],
          },
          FailoverCriteria: { StatusCodes: { Quantity: 2, Items: [502, 504] } },
        },
      ],
    }

    expect(mapOriginGroups(groups)).toEqual([
      { id: "group-1", members: ["origin-a", "origin-b"], failoverStatusCodes: [502, 504] },
    ])
  })

  it("returns an empty list when the distribution defines no groups", () => {
    expect(mapOriginGroups(undefined)).toEqual([])
    expect(mapOriginGroups({ Quantity: 0 })).toEqual([])
  })

  it("tolerates a group with missing members or criteria", () => {
    // Every level of the SDK type is optional, and a distribution that came
    // from a hand-written template can be missing any of them. The UI must
    // still render a row rather than throw.
    const groups: OriginGroups = { Quantity: 1, Items: [{ Id: "sparse" }] }

    expect(mapOriginGroups(groups)).toEqual([
      { id: "sparse", members: [], failoverStatusCodes: [] },
    ])
  })
})
