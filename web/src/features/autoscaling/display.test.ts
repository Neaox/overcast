import { describe, it, expect } from "vitest"
import {
  activityTone,
  capacitySummary,
  formatTime,
  healthTone,
  isConverged,
  lifecycleTone,
} from "./display"

describe("lifecycleTone", () => {
  it("separates settled, in-flight and parked states", () => {
    expect(lifecycleTone("InService")).toBe("success")
    expect(lifecycleTone("Pending")).toBe("info")
    expect(lifecycleTone("Pending:Wait")).toBe("warning")
    expect(lifecycleTone("Terminating:Wait")).toBe("warning")
    expect(lifecycleTone("Terminating")).toBe("danger")
    expect(lifecycleTone(undefined)).toBe("default")
  })
})

describe("healthTone", () => {
  it("only calls a known-healthy instance healthy", () => {
    expect(healthTone("Healthy")).toBe("success")
    expect(healthTone("Unhealthy")).toBe("danger")
    expect(healthTone(undefined)).toBe("default")
  })
})

describe("activityTone", () => {
  it("distinguishes finished from in-flight activities", () => {
    expect(activityTone("Successful")).toBe("success")
    expect(activityTone("Failed")).toBe("danger")
    expect(activityTone("Cancelled")).toBe("warning")
    expect(activityTone("PreInService")).toBe("info")
    expect(activityTone(undefined)).toBe("default")
  })
})

describe("capacitySummary", () => {
  it("reports in-service against desired", () => {
    expect(capacitySummary(2, 2)).toBe("2 / 2")
    expect(capacitySummary(1, 3)).toBe("1 / 3")
    expect(capacitySummary(0, undefined)).toBe("0 / 0")
  })
})

describe("isConverged", () => {
  it("requires every instance to be in service, not just enough of them", () => {
    expect(isConverged([{ LifecycleState: "InService" }], 1)).toBe(true)
    expect(
      isConverged([{ LifecycleState: "InService" }, { LifecycleState: "Terminating" }], 1),
    ).toBe(false)
    expect(isConverged([{ LifecycleState: "Pending" }], 1)).toBe(false)
    expect(isConverged([], 0)).toBe(true)
  })
})

describe("formatTime", () => {
  it("renders a dash rather than Invalid Date", () => {
    expect(formatTime(undefined)).toBe("—")
    expect(formatTime("not a date")).toBe("—")
    expect(formatTime(new Date(0))).not.toBe("—")
  })
})
