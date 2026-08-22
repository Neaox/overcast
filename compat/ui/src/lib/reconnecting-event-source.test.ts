import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  ReconnectingEventSource,
  retryDelayMs,
  type EventSourceLike,
} from "./reconnecting-event-source";

describe("retryDelayMs", () => {
  it("doubles from a 1s base up to a 5s ceiling", () => {
    expect(retryDelayMs(0)).toBe(1_000);
    expect(retryDelayMs(1)).toBe(2_000);
    expect(retryDelayMs(2)).toBe(4_000);
    expect(retryDelayMs(3)).toBe(5_000); // would be 8s uncapped
    expect(retryDelayMs(10)).toBe(5_000); // stays capped forever
  });
});

/** A controllable fake standing in for the browser's EventSource. */
class FakeEventSource implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent<string>) => void) | null = null;
  closed = false;

  close(): void {
    this.closed = true;
  }

  open(): void {
    this.onopen?.(new Event("open"));
  }

  error(): void {
    this.onerror?.(new Event("error"));
  }

  message(data: string): void {
    this.onmessage?.({ data } as MessageEvent<string>);
  }
}

describe("ReconnectingEventSource", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("reports connecting, then open, on a clean first connection", () => {
    const sources: FakeEventSource[] = [];
    const statuses: Array<[string, number]> = [];
    const opens: number[] = [];

    new ReconnectingEventSource({
      url: "/events",
      onMessage: () => {},
      onStatusChange: (s, a) => statuses.push([s, a]),
      onOpen: (priorAttempt) => opens.push(priorAttempt),
      open: () => {
        const s = new FakeEventSource();
        sources.push(s);
        return s;
      },
    });

    expect(statuses).toEqual([["connecting", 0]]);
    sources[0].open();
    expect(statuses).toEqual([
      ["connecting", 0],
      ["open", 0],
    ]);
    expect(opens).toEqual([0]);
  });

  it("passes every message through untouched", () => {
    const sources: FakeEventSource[] = [];
    const messages: string[] = [];

    new ReconnectingEventSource({
      url: "/events",
      onMessage: (data) => messages.push(data),
      onStatusChange: () => {},
      onOpen: () => {},
      open: () => {
        const s = new FakeEventSource();
        sources.push(s);
        return s;
      },
    });

    sources[0].open();
    sources[0].message('{"event":"run_start"}');
    sources[0].message('{"event":"run_end"}');
    expect(messages).toEqual([
      '{"event":"run_start"}',
      '{"event":"run_end"}',
    ]);
  });

  it("reconnects with backoff after a drop, and reports priorAttempt on recovery", () => {
    const sources: FakeEventSource[] = [];
    const statuses: Array<[string, number]> = [];
    const opens: number[] = [];

    new ReconnectingEventSource({
      url: "/events",
      onMessage: () => {},
      onStatusChange: (s, a) => statuses.push([s, a]),
      onOpen: (priorAttempt) => opens.push(priorAttempt),
      open: () => {
        const s = new FakeEventSource();
        sources.push(s);
        return s;
      },
    });

    sources[0].open();
    statuses.length = 0;
    opens.length = 0;

    // First drop: scheduled 1s out.
    sources[0].error();
    expect(sources[0].closed).toBe(true);
    expect(statuses).toEqual([["reconnecting", 1]]);

    vi.advanceTimersByTime(999);
    expect(sources).toHaveLength(1); // not yet
    vi.advanceTimersByTime(1);
    expect(sources).toHaveLength(2); // reconnect attempt fired

    // Second drop before the retry succeeds: backs off to 2s. The middle
    // entry is the retry attempt itself firing (still "reconnecting" — only
    // the very first-ever attempt reports "connecting").
    sources[1].error();
    expect(statuses).toEqual([
      ["reconnecting", 1],
      ["reconnecting", 1],
      ["reconnecting", 2],
    ]);
    vi.advanceTimersByTime(2_000);
    expect(sources).toHaveLength(3);

    // This attempt succeeds.
    sources[2].open();
    expect(opens).toEqual([2]); // recovered after 2 failed attempts
    expect(statuses[statuses.length - 1]).toEqual(["open", 0]);
  });

  it("stops scheduling reconnects once closed", () => {
    const sources: FakeEventSource[] = [];

    const stream = new ReconnectingEventSource({
      url: "/events",
      onMessage: () => {},
      onStatusChange: () => {},
      onOpen: () => {},
      open: () => {
        const s = new FakeEventSource();
        sources.push(s);
        return s;
      },
    });

    sources[0].open();
    stream.close();
    sources[0].error();

    vi.advanceTimersByTime(10_000);
    expect(sources).toHaveLength(1); // no reconnect attempt was scheduled
  });

  it("ignores an error from a source that has already been superseded", () => {
    const sources: FakeEventSource[] = [];
    const statuses: Array<[string, number]> = [];

    new ReconnectingEventSource({
      url: "/events",
      onMessage: () => {},
      onStatusChange: (s, a) => statuses.push([s, a]),
      onOpen: () => {},
      open: () => {
        const s = new FakeEventSource();
        sources.push(s);
        return s;
      },
    });

    sources[0].open();
    sources[0].error(); // schedules a reconnect
    vi.advanceTimersByTime(1_000); // sources[1] now current

    statuses.length = 0;
    sources[0].error(); // stale — must not affect state
    expect(statuses).toEqual([]);
  });
});
