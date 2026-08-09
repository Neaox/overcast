/**
 * The section's job is to send a `LoggingConfig` the Lambda API will accept.
 * Everything asserted here is a shape the service refuses: a level under the
 * Text format is a 400, an empty object is a 501, and a payload that forgets the
 * log group silently moves the function's logs back to the default group.
 */
import type { UpdateFunctionConfigurationCommandInput } from "@aws-sdk/client-lambda"
import { render, screen } from "@/test/render"
import { lambda } from "@/services/api"
import type { LambdaFunction } from "@/types"
import { LoggingConfigSection } from "./configuration-tab"

/**
 * Captures what UpdateFunctionConfiguration was asked to send. The API client
 * is the boundary worth watching here — the section's whole job is the shape of
 * that one request — and `restoreMocks` puts it back after each test.
 */
function captureUpdate(): UpdateFunctionConfigurationCommandInput[] {
  const inputs: UpdateFunctionConfigurationCommandInput[] = []
  vi.spyOn(lambda, "updateFunctionConfiguration").mockImplementation((params) => {
    inputs.push(params)
    return Promise.resolve({} as Awaited<ReturnType<typeof lambda.updateFunctionConfiguration>>)
  })
  return inputs
}

const textFunction: LambdaFunction = {
  FunctionName: "fn",
  LoggingConfig: { LogGroup: "/aws/lambda/fn", LogFormat: "Text" },
}

const jsonFunction: LambdaFunction = {
  FunctionName: "fn",
  LoggingConfig: {
    LogGroup: "/custom/group",
    LogFormat: "JSON",
    ApplicationLogLevel: "INFO",
    SystemLogLevel: "INFO",
  },
}

describe("LoggingConfigSection > read-only view", () => {
  it("shows the log format a function is configured with", () => {
    render(<LoggingConfigSection fn={textFunction} />)
    expect(screen.getByText("Text")).toBeInTheDocument()
  })

  it("hides the log levels for a Text function, which cannot have them", () => {
    render(<LoggingConfigSection fn={textFunction} />)
    expect(screen.queryByText("Application log level")).not.toBeInTheDocument()
  })

  it("shows both log levels for a JSON function", () => {
    render(<LoggingConfigSection fn={jsonFunction} />)
    expect(screen.getByText("Application log level")).toBeInTheDocument()
    expect(screen.getByText("System log level")).toBeInTheDocument()
  })
})

describe("LoggingConfigSection > editing", () => {
  it("offers JSON as a log format, which the emulator used to answer 501 to", async () => {
    const { user } = render(<LoggingConfigSection fn={textFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    expect(screen.getByRole("option", { name: "JSON" })).toBeInTheDocument()
  })

  it("disables the log level controls while the format is Text", async () => {
    const { user } = render(<LoggingConfigSection fn={textFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    expect(screen.getByLabelText("Application log level")).toBeDisabled()
    expect(screen.getByLabelText("System log level")).toBeDisabled()
  })

  it("enables the log level controls once the format is JSON", async () => {
    const { user } = render(<LoggingConfigSection fn={textFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.selectOptions(screen.getByLabelText("Log format"), "JSON")
    expect(screen.getByLabelText("Application log level")).toBeEnabled()
    expect(screen.getByLabelText("System log level")).toBeEnabled()
  })

  it("defaults both log levels to INFO when JSON is selected", async () => {
    const { user } = render(<LoggingConfigSection fn={textFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.selectOptions(screen.getByLabelText("Log format"), "JSON")
    expect(screen.getByLabelText("Application log level")).toHaveValue("INFO")
    expect(screen.getByLabelText("System log level")).toHaveValue("INFO")
  })

  it("restores the saved configuration on cancel", async () => {
    const { user } = render(<LoggingConfigSection fn={textFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.selectOptions(screen.getByLabelText("Log format"), "JSON")
    await user.click(screen.getByRole("button", { name: "Cancel" }))
    await user.click(screen.getByRole("button", { name: "Edit" }))
    expect(screen.getByLabelText("Log format")).toHaveValue("Text")
  })
})

describe("LoggingConfigSection > saving", () => {
  it("sends both log levels when the format is JSON", async () => {
    const inputs = captureUpdate()
    const { user } = render(<LoggingConfigSection fn={textFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.selectOptions(screen.getByLabelText("Log format"), "JSON")
    await user.selectOptions(screen.getByLabelText("Application log level"), "DEBUG")
    await user.click(screen.getByRole("button", { name: "Save changes" }))

    await screen.findByRole("button", { name: "Edit" })
    expect(inputs[0]?.LoggingConfig).toMatchObject({
      LogFormat: "JSON",
      ApplicationLogLevel: "DEBUG",
      SystemLogLevel: "INFO",
    })
  })

  it("sends no log level when the format is Text", async () => {
    const inputs = captureUpdate()
    const { user } = render(<LoggingConfigSection fn={jsonFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.selectOptions(screen.getByLabelText("Log format"), "Text")
    await user.click(screen.getByRole("button", { name: "Save changes" }))

    await screen.findByRole("button", { name: "Edit" })
    expect(inputs[0]?.LoggingConfig).not.toHaveProperty("ApplicationLogLevel")
    expect(inputs[0]?.LoggingConfig).not.toHaveProperty("SystemLogLevel")
  })

  it("carries the custom log group, which the service would otherwise reset", async () => {
    const inputs = captureUpdate()
    const { user } = render(<LoggingConfigSection fn={jsonFunction} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.selectOptions(screen.getByLabelText("System log level"), "WARN")
    await user.click(screen.getByRole("button", { name: "Save changes" }))

    await screen.findByRole("button", { name: "Edit" })
    expect(inputs[0]?.LoggingConfig?.LogGroup).toBe("/custom/group")
  })

  it("never sends an empty LoggingConfig, which the emulator answers 501 to", async () => {
    const inputs = captureUpdate()
    const { user } = render(<LoggingConfigSection fn={{ FunctionName: "fn" }} />)
    await user.click(screen.getByRole("button", { name: "Edit" }))
    await user.click(screen.getByRole("button", { name: "Save changes" }))

    await screen.findByRole("button", { name: "Edit" })
    expect(Object.keys(inputs[0]?.LoggingConfig ?? {})).not.toHaveLength(0)
  })
})
