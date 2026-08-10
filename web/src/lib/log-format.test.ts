/**
 * These helpers had no tests while they were copy-pasted into four components,
 * which is how the two row-tint maps drifted apart without anyone noticing.
 * Extracting them is only worth it if the shared behaviour is pinned.
 */
import {
  detectLogLevel,
  formatLogDate,
  formatLogTime,
  formatPlatformRecord,
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

describe("highlightJSON", () => {
  it("marks up the JSON grammar", () => {
    const html = highlightJSON('{"a": 1}')
    expect(html).toContain("token property")
    expect(html).toContain("token number")
  })

  it("escapes the text it highlights", () => {
    expect(highlightJSON('{"a": "<img src=x>"}')).not.toContain("<img")
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
