import { describe, it, expect } from "vitest";
import { reducer, initial } from "./reducer";
import type { RunState } from "../types/index";

describe("connection_status", () => {
  it("updates the connection field without touching anything else", () => {
    const next = reducer(initial, {
      type: "connection_status",
      status: "reconnecting",
      attempt: 2,
    });
    expect(next.connection).toEqual({ status: "reconnecting", attempt: 2 });
    expect(next.services).toBe(initial.services); // untouched slice, same ref
  });

  it("returns the same state object for a no-op transition", () => {
    const next = reducer(initial, {
      type: "connection_status",
      status: "connecting",
      attempt: 0,
    });
    expect(next).toBe(initial);
  });
});

describe("toast_error / dismiss_toast", () => {
  it("appends a toast with a unique id", () => {
    const withOne = reducer(initial, {
      type: "toast_error",
      message: "Run trigger failed (409): run already in progress",
    });
    expect(withOne.toasts).toHaveLength(1);
    expect(withOne.toasts[0].message).toBe(
      "Run trigger failed (409): run already in progress",
    );

    const withTwo = reducer(withOne, {
      type: "toast_error",
      message: "second failure",
    });
    expect(withTwo.toasts).toHaveLength(2);
    expect(withTwo.toasts[0].id).not.toBe(withTwo.toasts[1].id);
  });

  it("removes exactly the dismissed toast", () => {
    const withOne = reducer(initial, {
      type: "toast_error",
      message: "oops",
    });
    const id = withOne.toasts[0].id;
    const after = reducer(withOne, { type: "dismiss_toast", id });
    expect(after.toasts).toEqual([]);
  });
});

describe("reset preserves connection and toasts", () => {
  it("does not wipe live connection status or pending toasts on a data reset", () => {
    const withStatus: RunState = {
      ...initial,
      connection: { status: "reconnecting", attempt: 3 },
      toasts: [{ id: "1", message: "m", createdAt: 0 }],
      status: "done",
    };
    const after = reducer(withStatus, { type: "reset" });
    expect(after.connection).toEqual({ status: "reconnecting", attempt: 3 });
    expect(after.toasts).toEqual([{ id: "1", message: "m", createdAt: 0 }]);
    expect(after.status).toBe("idle"); // everything else really did reset
  });

  it("preserves connection/toasts through the clean-start fast path", () => {
    const withStatus: RunState = {
      ...initial,
      connection: { status: "open", attempt: 0 },
      toasts: [{ id: "1", message: "m", createdAt: 0 }],
    };
    const after = reducer(withStatus, {
      type: "event",
      payload: {
        event: "run_start",
        suite: "go-sdk",
        endpoint: "http://localhost:4566",
      },
    });
    expect(after.connection).toEqual({ status: "open", attempt: 0 });
    expect(after.toasts).toEqual([{ id: "1", message: "m", createdAt: 0 }]);
  });

  it("preserves connection/toasts through a full run_reset", () => {
    const withStatus: RunState = {
      ...initial,
      connection: { status: "open", attempt: 0 },
      toasts: [{ id: "1", message: "m", createdAt: 0 }],
      suites: ["go-sdk"],
    };
    const after = reducer(withStatus, {
      type: "event",
      payload: { event: "run_reset", suites: [] },
    });
    expect(after.connection).toEqual({ status: "open", attempt: 0 });
    expect(after.toasts).toEqual([{ id: "1", message: "m", createdAt: 0 }]);
  });
});

describe("seed_registry carries suites scope onto the group row", () => {
  it("attaches the registry's suites restriction to a new group", () => {
    const after = reducer(initial, {
      type: "seed_registry",
      groups: [
        {
          service: "cdk",
          name: "cdk-lifecycle",
          suites: ["cdk"],
          tests: [{ name: "Bootstrap" }],
        },
        {
          service: "s3",
          name: "s3-crud",
          tests: [{ name: "CreateBucket" }],
        },
      ],
    });
    expect(after.services.get("cdk")?.groups.get("cdk-lifecycle")?.suites).toEqual([
      "cdk",
    ]);
    expect(
      after.services.get("s3")?.groups.get("s3-crud")?.suites,
    ).toBeUndefined();
  });

  it("attaches suites scope even if the group already existed from a live event", () => {
    const withGroup = reducer(initial, {
      type: "event",
      payload: {
        event: "test_result",
        suite: "cdk",
        service: "cdk",
        group: "cdk-lifecycle",
        test: "Bootstrap",
        status: "pass",
        duration_ms: 1,
      },
    });
    expect(
      withGroup.services.get("cdk")?.groups.get("cdk-lifecycle")?.suites,
    ).toBeUndefined();

    const after = reducer(withGroup, {
      type: "seed_registry",
      groups: [
        {
          service: "cdk",
          name: "cdk-lifecycle",
          suites: ["cdk"],
          tests: [{ name: "Bootstrap" }],
        },
      ],
    });
    expect(
      after.services.get("cdk")?.groups.get("cdk-lifecycle")?.suites,
    ).toEqual(["cdk"]);
    // The existing cell survives — seeding is additive, never destructive.
    expect(
      after.services.get("cdk")?.groups.get("cdk-lifecycle")?.tests.get("Bootstrap"),
    ).toEqual({ cdk: { status: "pass", error: undefined, op: undefined } });
  });
});
