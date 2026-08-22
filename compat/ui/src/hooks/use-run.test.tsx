import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import type { ReactNode } from "react";
import { DispatchContext } from "../state/dispatch-context";
import { useRun } from "./use-run";

function wrapperWith(dispatch: (action: unknown) => void) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <DispatchContext.Provider value={dispatch as never}>
        {children}
      </DispatchContext.Provider>
    );
  };
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("useRun", () => {
  const dispatch = vi.fn();

  beforeEach(() => {
    dispatch.mockClear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("dispatches queued entries and returns ok on success", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(202, {
          batch_id: "b1",
          queued: [{ batch_id: "b1", suite: "go-sdk", group: "", test: "" }],
        }),
      ),
    );

    const { result } = renderHook(() => useRun(), {
      wrapper: wrapperWith(dispatch),
    });

    let outcome: { ok: boolean; batch_id?: string } | undefined;
    await act(async () => {
      outcome = await result.current({ suite: "go-sdk" });
    });

    expect(outcome).toEqual({ ok: true, batch_id: "b1" });
    expect(dispatch).toHaveBeenCalledWith({
      type: "queued",
      entries: [{ batch_id: "b1", suite: "go-sdk", group: "", test: "" }],
    });
  });

  it("surfaces a 409 with the server's own error message instead of failing silently", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          jsonResponse(409, { error: "run already in progress" }),
        ),
    );

    const { result } = renderHook(() => useRun(), {
      wrapper: wrapperWith(dispatch),
    });

    let outcome: { ok: boolean } | undefined;
    await act(async () => {
      outcome = await result.current({});
    });

    expect(outcome).toEqual({ ok: false });
    expect(dispatch).toHaveBeenCalledWith({
      type: "toast_error",
      message: "Run trigger failed (409): run already in progress",
    });
  });

  it("surfaces a network error with an actionable message", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );

    const { result } = renderHook(() => useRun(), {
      wrapper: wrapperWith(dispatch),
    });

    let outcome: { ok: boolean } | undefined;
    await act(async () => {
      outcome = await result.current({});
    });

    expect(outcome).toEqual({ ok: false });
    expect(dispatch).toHaveBeenCalledWith({
      type: "toast_error",
      message:
        "Could not reach the compat server to start the run — check it is still running.",
    });
  });

  it("falls back to plain text when the error body is not JSON", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response("internal server error", { status: 500 }),
        ),
    );

    const { result } = renderHook(() => useRun(), {
      wrapper: wrapperWith(dispatch),
    });

    await act(async () => {
      await result.current({});
    });

    expect(dispatch).toHaveBeenCalledWith({
      type: "toast_error",
      message: "Run trigger failed (500): internal server error",
    });
  });
});
