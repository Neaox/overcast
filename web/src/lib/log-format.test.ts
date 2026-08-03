/**
 * These helpers had no tests while they were copy-pasted into four components,
 * which is how the two row-tint maps drifted apart without anyone noticing.
 * Extracting them is only worth it if the shared behaviour is pinned.
 */
import {
  detectLogLevel,
  formatLogDate,
  formatLogTime,
  highlightJSON,
  logLevelBadgeClass,
  logLevelRowClass,
  stringifyJSON,
  tryParseJSON,
} from "./log-format"

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
    expect(detectLogLevel("Certificate request self-signature ok")).toBeNull()
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
