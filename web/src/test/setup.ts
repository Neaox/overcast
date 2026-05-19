import "@testing-library/jest-dom/vitest"
import { afterAll, afterEach, beforeAll } from "vitest"
import { server } from "./server"

// ─── MSW ─────────────────────────────────────────────────────────────────

// Start the MSW Node server before all tests, reset handlers after each
// test (clears any per-test overrides), and close after the suite.
beforeAll(() => server.listen({ onUnhandledRequest: "error" }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

// ─── Browser API stubs ────────────────────────────────────────────────────

class ResizeObserverMock {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia
}

if (!window.ResizeObserver) {
  window.ResizeObserver = ResizeObserverMock as typeof window.ResizeObserver
}
