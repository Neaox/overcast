import { describe, expect, it } from "vitest"
import { formatTriggerEvent } from "./trigger-event-format"

describe("formatTriggerEvent", () => {
  it("decodes base64 JSON trigger events before formatting", () => {
    const encoded = btoa(JSON.stringify({ Records: [{ eventSource: "aws:sqs" }] }))

    expect(formatTriggerEvent(encoded)).toEqual({
      language: "json",
      text: '{\n  "Records": [\n    {\n      "eventSource": "aws:sqs"\n    }\n  ]\n}',
    })
  })

  it("falls back to decoded plain text when the trigger is not JSON", () => {
    expect(formatTriggerEvent(btoa("not json"))).toEqual({
      language: "plain",
      text: "not json",
    })
  })

  it("falls back to the original text when the trigger is not base64", () => {
    expect(formatTriggerEvent("not json")).toEqual({
      language: "plain",
      text: "not json",
    })
  })
})
