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
    // The SDK types Members and FailoverCriteria as REQUIRED, but that is a
    // statement about well-formed AWS responses, not a guarantee about the
    // bytes on the wire — an emulator or a hand-written template can omit
    // either. The cast is the point of the test: it constructs the shape the
    // types forbid, so the mapper's optional chaining is exercised rather than
    // merely assumed, and the UI renders a row instead of throwing.
    const groups = { Quantity: 1, Items: [{ Id: "sparse" }] } as unknown as OriginGroups

    expect(mapOriginGroups(groups)).toEqual([
      { id: "sparse", members: [], failoverStatusCodes: [] },
    ])
  })
})
