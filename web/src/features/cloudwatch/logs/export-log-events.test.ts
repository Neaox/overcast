/**
 * Export mirrors the AWS console's search-results CSV: ISO 8601 timestamps and
 * the raw stored message. Raw means raw — an export that dropped ANSI escapes
 * or swapped in a platform-record summary would lose bytes the stream holds.
 */
import { describe, expect, it } from "vitest"
import type { FilteredLogEvent } from "@/types/logs"
import { buildLogEventsCsv, buildLogEventsJson } from "./export-log-events"

const EVENTS: FilteredLogEvent[] = [
  {
    timestamp: Date.UTC(2026, 7, 18, 9, 5, 7, 42),
    ingestionTime: Date.UTC(2026, 7, 18, 9, 5, 7, 900),
    logStreamName: "2026/08/18/[$LATEST]abc",
    message: "plain line",
  },
  {
    timestamp: Date.UTC(2026, 7, 18, 9, 5, 8, 0),
    ingestionTime: Date.UTC(2026, 7, 18, 9, 5, 8, 1),
    logStreamName: "s2",
    message: 'says "hi", then\nwraps',
  },
]

describe("buildLogEventsCsv", () => {
  it("renders the console's column shape with ISO 8601 timestamps", () => {
    const csv = buildLogEventsCsv([EVENTS[0]])

    expect(csv).toBe(
      "timestamp,logStreamName,message\r\n" +
        "2026-08-18T09:05:07.042Z,2026/08/18/[$LATEST]abc,plain line\r\n",
    )
  })

  it("quotes and escapes fields carrying quotes, commas or newlines", () => {
    const csv = buildLogEventsCsv([EVENTS[1]])

    expect(csv).toBe(
      "timestamp,logStreamName,message\r\n" +
        '2026-08-18T09:05:08.000Z,s2,"says ""hi"", then\nwraps"\r\n',
    )
  })

  it("keeps the given order — the export is the list as displayed", () => {
    const csv = buildLogEventsCsv([EVENTS[1], EVENTS[0]])
    const lines = csv.split("\r\n")

    expect(lines[1]).toContain("s2")
    expect(lines[2]).toContain("plain line")
  })

  it("exports the raw stored message, ANSI escapes and all", () => {
    const ansi = "\u001b[31mred alert\u001b[0m"
    const csv = buildLogEventsCsv([{ ...EVENTS[0], message: ansi }])

    expect(csv).toContain(ansi)
  })

  it("leaves the timestamp cell empty rather than inventing an epoch", () => {
    const csv = buildLogEventsCsv([{ logStreamName: "s1", message: "no time" }])

    expect(csv.split("\r\n")[1]).toBe(",s1,no time")
  })
})

describe("buildLogEventsJson", () => {
  it("emits an array of the four raw fields", () => {
    const parsed = JSON.parse(buildLogEventsJson(EVENTS)) as unknown[]

    expect(parsed).toEqual([
      {
        timestamp: EVENTS[0].timestamp,
        ingestionTime: EVENTS[0].ingestionTime,
        logStreamName: EVENTS[0].logStreamName,
        message: EVENTS[0].message,
      },
      {
        timestamp: EVENTS[1].timestamp,
        ingestionTime: EVENTS[1].ingestionTime,
        logStreamName: EVENTS[1].logStreamName,
        message: EVENTS[1].message,
      },
    ])
  })
})
