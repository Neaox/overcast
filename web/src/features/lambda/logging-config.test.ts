/**
 * Every rule pinned here is one the Lambda API enforces with a 400 or a 501, so
 * the failure mode of getting it wrong is a save that looks fine in the form and
 * is refused on the wire.
 */
import {
  buildLoggingConfig,
  DEFAULT_APPLICATION_LOG_LEVEL,
  DEFAULT_SYSTEM_LOG_LEVEL,
  loggingConfigForm,
} from "./logging-config"

describe("loggingConfigForm", () => {
  it("starts both levels at INFO, which is what a JSON function reports back", () => {
    expect(loggingConfigForm({ LogFormat: "JSON" })).toMatchObject({
      applicationLogLevel: DEFAULT_APPLICATION_LOG_LEVEL,
      systemLogLevel: DEFAULT_SYSTEM_LOG_LEVEL,
    })
  })

  it("reads the format a function was configured with", () => {
    expect(loggingConfigForm({ LogFormat: "JSON" }).logFormat).toBe("JSON")
  })

  it("treats a function stored before LoggingConfig existed as Text", () => {
    expect(loggingConfigForm(undefined).logFormat).toBe("Text")
    expect(loggingConfigForm({}).logFormat).toBe("Text")
  })

  it("keeps the levels a JSON function already has", () => {
    expect(
      loggingConfigForm({
        LogFormat: "JSON",
        ApplicationLogLevel: "DEBUG",
        SystemLogLevel: "WARN",
      }),
    ).toMatchObject({ applicationLogLevel: "DEBUG", systemLogLevel: "WARN" })
  })

  it("carries the log group so a save cannot drop it", () => {
    expect(loggingConfigForm({ LogGroup: "/custom/group" }).logGroup).toBe("/custom/group")
  })
})

describe("buildLoggingConfig", () => {
  const text = {
    logGroup: "",
    logFormat: "Text",
    applicationLogLevel: "DEBUG",
    systemLogLevel: "DEBUG",
  } as const

  it("never sends a level under the Text format — the API answers 400 for it", () => {
    const payload = buildLoggingConfig(text)
    expect(payload.ApplicationLogLevel).toBeUndefined()
    expect(payload.SystemLogLevel).toBeUndefined()
  })

  it("names the format even when nothing else changed, so the object is never empty", () => {
    // UpdateFunctionConfiguration answers 501 to an explicitly empty
    // LoggingConfig, and switching JSON back to Text has nothing else to say.
    expect(buildLoggingConfig(text)).toEqual({ LogFormat: "Text" })
  })

  it("sends both levels under the JSON format", () => {
    expect(buildLoggingConfig({ ...text, logFormat: "JSON" })).toEqual({
      LogFormat: "JSON",
      ApplicationLogLevel: "DEBUG",
      SystemLogLevel: "DEBUG",
    })
  })

  it("carries the log group, because the service rebuilds the member wholesale", () => {
    expect(buildLoggingConfig({ ...text, logGroup: "/custom/group" }).LogGroup).toBe(
      "/custom/group",
    )
  })

  it("omits a blank log group rather than sending one, which fails length validation", () => {
    expect(buildLoggingConfig({ ...text, logGroup: "   " })).not.toHaveProperty("LogGroup")
  })

  it("trims the log group", () => {
    expect(buildLoggingConfig({ ...text, logGroup: "  /custom/group  " }).LogGroup).toBe(
      "/custom/group",
    )
  })

  it("round-trips a function's own configuration unchanged", () => {
    const config = {
      LogGroup: "/custom/group",
      LogFormat: "JSON",
      ApplicationLogLevel: "WARN",
      SystemLogLevel: "DEBUG",
    } as const
    expect(buildLoggingConfig(loggingConfigForm(config))).toEqual(config)
  })
})
