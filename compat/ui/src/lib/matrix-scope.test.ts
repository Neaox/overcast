import { describe, it, expect } from "vitest";
import { isOutOfScope } from "./matrix-scope";

describe("isOutOfScope", () => {
  it("is never out of scope when the group declares no suites restriction", () => {
    expect(isOutOfScope(undefined, "go-sdk")).toBe(false);
  });

  it("is out of scope when the suite is not in the declared list", () => {
    expect(isOutOfScope(["cdk"], "go-sdk")).toBe(true);
  });

  it("is in scope when the suite is in the declared list", () => {
    expect(isOutOfScope(["cdk", "cli"], "cli")).toBe(false);
  });

  it("treats an empty suites list as out of scope for everything", () => {
    expect(isOutOfScope([], "cli")).toBe(true);
  });
});
