import { describe, it, expect, beforeEach, vi } from "vitest";
import { readPersisted, writePersisted } from "./persisted-storage";

describe("persisted-storage", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("returns the fallback when nothing has been written", () => {
    expect(readPersisted("missing", 42)).toBe(42);
  });

  it("round-trips a written value", () => {
    writePersisted("statuses", ["na", "unimplemented"]);
    expect(readPersisted<string[]>("statuses", [])).toEqual([
      "na",
      "unimplemented",
    ]);
  });

  it("falls back on malformed JSON left by an older build", () => {
    window.localStorage.setItem("overcast-compat:broken", "{not json");
    expect(readPersisted("broken", "default")).toBe("default");
  });

  it("namespaces keys so it never collides with unrelated storage", () => {
    writePersisted("x", 1);
    expect(window.localStorage.getItem("overcast-compat:x")).toBe("1");
    expect(window.localStorage.getItem("x")).toBeNull();
  });

  it("swallows a write failure instead of throwing", () => {
    const spy = vi
      .spyOn(window.localStorage.__proto__, "setItem")
      .mockImplementation(() => {
        throw new Error("quota exceeded");
      });
    expect(() => writePersisted("y", 1)).not.toThrow();
    spy.mockRestore();
  });

  it("swallows a read failure instead of throwing", () => {
    const spy = vi
      .spyOn(window.localStorage.__proto__, "getItem")
      .mockImplementation(() => {
        throw new Error("storage disabled");
      });
    expect(readPersisted("z", "fallback")).toBe("fallback");
    spy.mockRestore();
  });
});
