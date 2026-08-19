/**
 * Must execute before `prismjs` does — import this ahead of it (see
 * [prism.ts](./prism.ts), the only place that imports Prism directly).
 *
 * Prism core reads `_self.Prism.disableWorkerMessageHandler` at its own
 * module init, and when it finds itself in a scope with no `document` — a
 * dedicated worker — and the flag unset, it registers its own `message`
 * listener that `JSON.parse`s every event's data. Our highlight worker's
 * protocol is a structured-cloned object, so that listener would throw an
 * uncaught SyntaxError on the first request, firing the Worker's `error`
 * event and making the facade retire the worker — a silent, permanent
 * fallback to main-thread tokenization. Setting the flag *after* importing
 * Prism is too late (the listener is already registered), hence this module:
 * ES imports hoist, so the only way to run a statement before a dependency's
 * body is to put it in an earlier import.
 */
const scope = globalThis as { Prism?: { disableWorkerMessageHandler?: boolean } }
scope.Prism = { ...scope.Prism, disableWorkerMessageHandler: true }

export {}
