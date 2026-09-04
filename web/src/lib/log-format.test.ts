/**
 * These helpers had no tests while they were copy-pasted into four components,
 * which is how the two row-tint maps drifted apart without anyone noticing.
 * Extracting them is only worth it if the shared behaviour is pinned.
 */
import {
  describeLogEvent,
  detectLogLevel,
  formatLogDate,
  formatLogDelta,
  formatLogTime,
  formatPlatformRecord,
  jsonDocumentText,
  highlightJSON,
  logLevelBadgeClass,
  logLevelRowClass,
  parsePlatformRecord,
  stringifyJSON,
  summarisePlatformRecords,
  tryParseJSON,
} from "./log-format"

// The three system log records a JSON-format Lambda writes, verbatim in the
// shapes internal/services/lambda/logging_json.go emits.
const startRecord =
  '{"time":"2026-08-09T08:37:29.512Z","type":"platform.start","record":{"requestId":"8f1c","version":"$LATEST"}}'
const runtimeDoneRecord =
  '{"time":"2026-08-09T08:37:29.612Z","type":"platform.runtimeDone","record":{"requestId":"8f1c","status":"success","metrics":{"durationMs":98.5,"producedBytes":42}}}'
const reportRecord =
  '{"time":"2026-08-09T08:37:29.613Z","type":"platform.report","record":{"requestId":"8f1c","status":"success","metrics":{"durationMs":98.5,"billedDurationMs":99,"memorySizeMB":128,"maxMemoryUsedMB":47}}}'

describe("formatLogTime", () => {
  it("renders the clock time of an event to the millisecond", () => {
    const ts = new Date(2026, 7, 4, 9, 5, 7, 42).getTime()
    expect(formatLogTime(ts)).toBe("09:05:07.042")
  })

  it("pads every field, so the column stays aligned", () => {
    expect(formatLogTime(new Date(2026, 7, 4, 0, 0, 0, 0).getTime() + 1)).toBe("00:00:00.001")
  })

  it("shows a dash rather than 1970 for a missing timestamp", () => {
    expect(formatLogTime(undefined)).toBe("—")
    expect(formatLogTime(null)).toBe("—")
    expect(formatLogTime(0)).toBe("—")
  })

  it("renders the UTC clock time when asked, whatever the machine's zone", () => {
    // 2026-08-04T09:05:07.042Z, built from UTC parts so the expectation does
    // not depend on where the test runs.
    const ts = Date.UTC(2026, 7, 4, 9, 5, 7, 42)
    expect(formatLogTime(ts, true)).toBe("09:05:07.042")
  })

  it("defaults to local time, which is what every existing call site expects", () => {
    const ts = new Date(2026, 7, 4, 9, 5, 7, 42).getTime()
    expect(formatLogTime(ts)).toBe(formatLogTime(ts, false))
  })
})

describe("formatLogDelta", () => {
  it("renders sub-second gaps in milliseconds", () => {
    expect(formatLogDelta(3)).toBe("+3 ms")
    expect(formatLogDelta(999)).toBe("+999 ms")
  })

  it("renders sub-minute gaps in seconds", () => {
    expect(formatLogDelta(1_000)).toBe("+1.00 s")
    expect(formatLogDelta(1_240)).toBe("+1.24 s")
    expect(formatLogDelta(59_990)).toBe("+59.99 s")
  })

  it("renders minute-scale gaps as minutes and seconds", () => {
    expect(formatLogDelta(60_000)).toBe("+1m 0s")
    expect(formatLogDelta(125_000)).toBe("+2m 5s")
  })

  it("renders hour-scale gaps as hours and minutes", () => {
    expect(formatLogDelta(3_600_000)).toBe("+1h 0m")
    expect(formatLogDelta(7_380_000)).toBe("+2h 3m")
  })

  it("keeps the sign of a negative gap rather than hiding it", () => {
    // Deltas are display-only fiction over real timestamps: out-of-order
    // ingestion is a thing that happens, and a delta that lied about the
    // direction would imply event times that are not the event's.
    expect(formatLogDelta(-15)).toBe("-15 ms")
    expect(formatLogDelta(-1_240)).toBe("-1.24 s")
  })

  it("renders a same-millisecond neighbour as +0 ms", () => {
    expect(formatLogDelta(0)).toBe("+0 ms")
  })
})

describe("formatLogDate", () => {
  it("includes the date, which is the point of it", () => {
    const ts = new Date(2026, 7, 4, 9, 5).getTime()
    expect(formatLogDate(ts)).toBe(new Date(ts).toLocaleString())
  })

  it("shows a dash for a missing timestamp", () => {
    expect(formatLogDate(undefined)).toBe("—")
    expect(formatLogDate(0)).toBe("—")
  })

  it("renders in UTC when asked", () => {
    const ts = Date.UTC(2026, 7, 4, 9, 5)
    expect(formatLogDate(ts, true)).toBe(
      new Date(ts).toLocaleString(undefined, { timeZone: "UTC" }),
    )
  })
})

describe("detectLogLevel", () => {
  it("reads a structured level field", () => {
    expect(detectLogLevel('{"level":"error","msg":"boom"}')).toBe("error")
    expect(detectLogLevel('{"level":"WARNING","msg":"hm"}')).toBe("warn")
    expect(detectLogLevel('{"msg":"ok","level":"trace"}')).toBe("debug")
  })

  it("folds the severities that mean the same thing", () => {
    expect(detectLogLevel('{"level":"fatal"}')).toBe("error")
    expect(detectLogLevel('{"level":"critical"}')).toBe("error")
  })

  it("falls back to the text of the line", () => {
    expect(detectLogLevel("ERROR ==> WORDPRESS_PASSWORD must be set")).toBe("error")
    expect(detectLogLevel("WARN  ==> could not resolve host")).toBe("warn")
    expect(detectLogLevel("DEBUG connecting")).toBe("debug")
    expect(detectLogLevel("INFO  ==> Starting gosu")).toBe("info")
    expect(detectLogLevel("Certificate request self-signature ok")).toBeNull()
  })

  it("reads the level column of a Node runtime's console.* line", () => {
    // Where AWS puts it: after the timestamp and the request id, tab separated.
    // The request id alone is 36 characters, so this leans on the whole 80.
    const line = (level: string) =>
      `2026-08-10T02:34:39.674Z\t13aa488f-ea9e-4d38-bdfe-74ad3d71e708\t${level}\tCannot push rates`
    expect(detectLogLevel(line("ERROR"))).toBe("error")
    expect(detectLogLevel(line("WARN"))).toBe("warn")
    expect(detectLogLevel(line("INFO"))).toBe("info")
    expect(detectLogLevel(line("DEBUG"))).toBe("debug")
  })

  it("prefers the structured field over the text", () => {
    // The message mentions ERROR; the record says it is info, and it knows.
    expect(detectLogLevel('{"level":"info","msg":"retrying after ERROR response"}')).toBe("info")
  })

  it("only reads the first 80 characters, so a mid-line word is not a severity", () => {
    const late = `${"x".repeat(90)} ERROR`
    expect(detectLogLevel(late)).toBeNull()
  })

  it("needs a whole word, not a substring", () => {
    expect(detectLogLevel("TERRORISM statistics loaded")).toBeNull()
    expect(detectLogLevel("no-warnings mode")).toBeNull()
    expect(detectLogLevel("reinformation pipeline ready")).toBeNull()
  })
})

describe("level classes", () => {
  it("covers every level in both maps — a missing key renders an untinted row", () => {
    for (const level of ["error", "warn", "info", "debug"] as const) {
      expect(logLevelRowClass[level]).toBeDefined()
      expect(logLevelBadgeClass[level]).toBeTruthy()
    }
  })

  it("leaves the info row untinted, since most rows are info", () => {
    expect(logLevelRowClass.info).toBe("")
  })
})

describe("tryParseJSON", () => {
  it("parses a message that is one JSON document", () => {
    expect(tryParseJSON('  {"a":1}  ')).toEqual({ a: 1 })
    expect(tryParseJSON("[1,2]")).toEqual([1, 2])
  })

  it("returns null for anything else, without throwing", () => {
    expect(tryParseJSON("plain text")).toBeNull()
    expect(tryParseJSON('12:00:00 {"a":1}')).toBeNull() // prefixed, not a document
    expect(tryParseJSON('{"a":')).toBeNull() // truncated
    expect(tryParseJSON("")).toBeNull()
  })
})

describe("stringifyJSON", () => {
  it("indents when asked and stays on one line when not", () => {
    expect(stringifyJSON({ a: 1 }, true)).toBe('{\n  "a": 1\n}')
    expect(stringifyJSON({ a: 1 }, false)).toBe('{"a":1}')
  })
})

describe("jsonDocumentText", () => {
  it("re-serialises a JSON message in the asked-for form", () => {
    expect(jsonDocumentText('{"a":1}', true)).toBe('{\n  "a": 1\n}')
    expect(jsonDocumentText('{"a":1}', false)).toBe('{"a":1}')
  })

  it("returns the identical string on a repeat ask — the memo the row mounts lean on", () => {
    // Identity, not equality: a stable string is what lets React skip the DOM
    // write and an effect keyed on it skip re-application.
    const msg = `{"repeat": ${Math.PI}, "items": [1, 2, 3]}`
    expect(jsonDocumentText(msg, true)).toBe(jsonDocumentText(msg, true))
    expect(jsonDocumentText(msg, false)).toBe(jsonDocumentText(msg, false))
  })

  it("strips ANSI before deciding a colourised line is JSON", () => {
    const esc = String.fromCharCode(27)
    expect(jsonDocumentText(`${esc}[32m{"ok":true}${esc}[0m`, false)).toBe('{"ok":true}')
  })

  it("answers null for non-JSON, and caches that answer too", () => {
    expect(jsonDocumentText("plain line", true)).toBeNull()
    expect(jsonDocumentText("plain line", true)).toBeNull()
  })
})

// `highlightJSON` itself — grammar, escaping, and caching — is pinned in
// `highlight-code.test.ts`, next to where it now lives. This only guards the
// re-export: the log viewers keep importing it from here.
describe("highlightJSON", () => {
  it("is re-exported from lib/highlight-code", () => {
    expect(highlightJSON('{"a": 1}')).toContain("token property")
  })
})

describe("parsePlatformRecord", () => {
  it("reads a system log record's type and payload", () => {
    expect(parsePlatformRecord(reportRecord)).toMatchObject({
      type: "platform.report",
      time: "2026-08-09T08:37:29.613Z",
    })
  })

  it("returns null for the function's own output, JSON or not", () => {
    expect(parsePlatformRecord("hello from the handler")).toBeNull()
    expect(parsePlatformRecord('{"level":"INFO","message":"hello"}')).toBeNull()
  })

  it("returns null for a Text-format platform line", () => {
    expect(parsePlatformRecord("START RequestId: 8f1c Version: $LATEST")).toBeNull()
  })

  it("returns null rather than throwing on a truncated record", () => {
    expect(parsePlatformRecord('{"type":"platform.report","record":')).toBeNull()
  })

  it("returns null when the record member is not an object", () => {
    expect(parsePlatformRecord('{"type":"platform.report","record":"nope"}')).toBeNull()
  })

  it("is not fooled by application output that mentions the prefix", () => {
    expect(parsePlatformRecord('{"level":"INFO","msg":"saw \\"platform.report\\""}')).toBeNull()
  })
})

describe("detectLogLevel > system log records", () => {
  it("gives a successful report the level AWS assigns it", () => {
    expect(detectLogLevel(reportRecord)).toBe("info")
  })

  it("gives a successful runtimeDone DEBUG, so it stays out of the way", () => {
    expect(detectLogLevel(runtimeDoneRecord)).toBe("debug")
  })

  it("raises a failed invocation's records to a warning", () => {
    const failed = runtimeDoneRecord.replace('"status":"success"', '"status":"failure"')
    expect(detectLogLevel(failed)).toBe("warn")
    expect(detectLogLevel(reportRecord.replace('"success"', '"timeout"'))).toBe("warn")
  })

  it("treats an unmodelled platform event as routine rather than guessing", () => {
    expect(detectLogLevel('{"type":"platform.extension","record":{"name":"ext"}}')).toBe("info")
  })
})

describe("formatPlatformRecord", () => {
  const summaryOf = (line: string) => formatPlatformRecord(parsePlatformRecord(line)!)

  it("renders a start record as the START line it replaced", () => {
    expect(summaryOf(startRecord)).toBe("START RequestId: 8f1c Version: $LATEST")
  })

  it("renders a runtimeDone record as END plus what the text line could not carry", () => {
    expect(summaryOf(runtimeDoneRecord)).toBe(
      "END RequestId: 8f1c\tStatus: success\tDuration: 98.50 ms\tProduced Bytes: 42 bytes",
    )
  })

  it("renders a report record as the REPORT line it replaced", () => {
    expect(summaryOf(reportRecord)).toBe(
      "REPORT RequestId: 8f1c\tDuration: 98.50 ms\tBilled Duration: 99 ms\tMemory Size: 128 MB\tMax Memory Used: 47 MB",
    )
  })

  it("leaves a successful report unadorned, as AWS's text REPORT does", () => {
    expect(summaryOf(reportRecord)).not.toContain("Status")
  })

  it("names the status on a report that did not succeed", () => {
    const failed = reportRecord.replace('"status":"success"', '"status":"error"')
    expect(summaryOf(failed)).toContain("Status: error")
  })

  it("includes the init duration only on a cold start", () => {
    const cold = reportRecord.replace(
      '"maxMemoryUsedMB":47',
      '"maxMemoryUsedMB":47,"initDurationMs":210.5',
    )
    expect(summaryOf(cold)).toContain("Init Duration: 210.50 ms")
    expect(summaryOf(reportRecord)).not.toContain("Init Duration")
  })

  it("skips a field the record does not carry rather than printing undefined", () => {
    expect(summaryOf('{"type":"platform.report","record":{"requestId":"8f1c"}}')).toBe(
      "REPORT RequestId: 8f1c",
    )
  })

  it("returns null for a platform event Overcast does not emit", () => {
    expect(summaryOf('{"type":"platform.initStart","record":{"requestId":"8f1c"}}')).toBeNull()
  })
})

describe("summarisePlatformRecords", () => {
  it("summarises the system records in a log tail and leaves the rest alone", () => {
    const tail = [startRecord, '{"level":"INFO","message":"hello"}', reportRecord].join("\n")
    const lines = summarisePlatformRecords(tail).split("\n")
    expect(lines[0]).toBe("START RequestId: 8f1c Version: $LATEST")
    expect(lines[1]).toBe('{"level":"INFO","message":"hello"}')
    expect(lines[2]).toMatch(/^REPORT RequestId: 8f1c\t/)
  })

  it("returns a Text-format tail byte for byte", () => {
    const tail = "START RequestId: 8f1c Version: $LATEST\nhello\nEND RequestId: 8f1c\n"
    expect(summarisePlatformRecords(tail)).toBe(tail)
  })

  it("leaves a platform record it cannot summarise as it found it", () => {
    const line = '{"type":"platform.initStart","record":{"requestId":"8f1c"}}'
    expect(summarisePlatformRecords(line)).toBe(line)
  })
})

describe("describeLogEvent", () => {
  /** Escapes rather than literals, so this file carries no control bytes of its own. */
  const esc = String.fromCharCode(27)

  it("derives the plain text, level and summary of a line", () => {
    const meta = describeLogEvent({ message: `${esc}[31mERROR${esc}[0m upstream refused` })

    expect(meta.plain).toBe("ERROR upstream refused")
    expect(meta.level).toBe("error")
    expect(meta.summary).toBeNull()
  })

  it("reads a system log record as the line it replaced", () => {
    const meta = describeLogEvent({ message: reportRecord })

    expect(meta.summary).toMatch(/^REPORT RequestId: 8f1c\t/)
    expect(meta.level).toBe("info")
  })

  it("agrees with detectLogLevel on a record that did not succeed", () => {
    const timedOut = reportRecord.replace('"status":"success"', '"status":"timeout"')

    expect(describeLogEvent({ message: timedOut }).level).toBe("warn")
    expect(detectLogLevel(timedOut)).toBe("warn")
  })

  it("derives an event once, however often its row re-renders", () => {
    const event = { message: '{"level":"warn","message":"disk filling"}' }

    // A live tail used to re-derive its whole history for every event that
    // arrived; keyed on the event, history is never re-read.
    expect(describeLogEvent(event)).toBe(describeLogEvent(event))
    expect(describeLogEvent(event).level).toBe("warn")
  })

  it("treats a missing message as an empty line rather than throwing", () => {
    const meta = describeLogEvent({})

    expect(meta.plain).toBe("")
    expect(meta.level).toBeNull()
    expect(meta.summary).toBeNull()
    expect(meta.requestId).toBeNull()
  })
})

/*
 * Request-id detection, for the viewer's click-to-filter affordance. One id
 * per event, derived once with the rest of the metadata — never per render.
 * The affordance only ever feeds the id back as a quoted FilterLogEvents
 * term, so detection has no server-side semantics to get wrong; what it must
 * not do is claim an id a line does not carry.
 */
describe("describeLogEvent > request ids", () => {
  const UUID = "13aa488f-ea9e-4d38-bdfe-74ad3d71e708"

  it("reads the id from a Text-format platform line", () => {
    const meta = describeLogEvent({ message: `START RequestId: ${UUID} Version: $LATEST` })

    expect(meta.requestId).toBe(UUID)
  })

  it("reads the id from a JSON platform record's requestId field", () => {
    expect(describeLogEvent({ message: reportRecord }).requestId).toBe("8f1c")
  })

  it("reads a structured application log's requestId field", () => {
    const meta = describeLogEvent({
      message: `{"level":"info","requestId":"${UUID}","msg":"handled"}`,
    })

    expect(meta.requestId).toBe(UUID)
  })

  it("reads the UUID column of a Node runtime console line", () => {
    // Where AWS puts it: after the timestamp, tab separated, before the level.
    const meta = describeLogEvent({
      message: `2026-08-10T02:34:39.674Z\t${UUID}\tWARN\tCannot push rates`,
    })

    expect(meta.requestId).toBe(UUID)
  })

  it("finds no id in an ordinary line", () => {
    expect(
      describeLogEvent({ message: "Certificate request self-signature ok" }).requestId,
    ).toBeNull()
  })

  it("does not mistake a non-UUID tab column for an id", () => {
    expect(describeLogEvent({ message: "alpha\tbeta\tgamma" }).requestId).toBeNull()
  })

  it("detects exactly one id per event — the labelled form wins", () => {
    const meta = describeLogEvent({
      message: `END RequestId: ${UUID} {"requestId":"not-this-one"}`,
    })

    expect(meta.requestId).toBe(UUID)
  })
})
