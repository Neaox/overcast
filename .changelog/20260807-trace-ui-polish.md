+ [router] new `/_debug/trace/*` endpoints for full request tracing — request/response bodies, per-handler structured log capture across 22 services, internal service-hop recording, and AWS errors. Active when `OVERCAST_DEBUG=true`; traces are looked up by the request ID already returned in every response. Includes web UI at `/debug/traces` with overview (side-by-side request/response with syntax highlighting), hops (upstream/downstream), logs, errors, and events tabs; Prism-based syntax highlighting for JSON, XML, form-encoded, and Go stack traces; infinite scroll + live polling; copy-as-curl; caller/callee navigation via hop request IDs.
* [router] `/_debug/traces` list and `/_debug/trace/{id}` detail endpoints with cursor-based bidirectional pagination
* [trace/buffer] internal traces (health, inbox, SSE, debug polling) capped at 20% of capacity so they never crowd out user-facing requests; oldest internal entries evicted first when non-internal entries arrive at a full buffer
* [trace] body-based service/operation detection for Query-protocol services (Action= param) using the generated awsapi.Registry
* [trace] stack traces captured during handler execution via Logger middleware's WriteHeader interceptor; Prism gostacktrace language for syntax highlighting
* [events] History.FindByRequestID for querying events by request ID; `GET /_events/request/{requestId}` endpoint wired
* [web] trace list: service dropdown with counts, status message in brackets, millisecond timestamps, deduplication, hop count badges
* [web] trace detail: Overview tab with side-by-side request/response, copy buttons on all metadata fields, referer field
* [web] events tab on trace detail: event-console matching layout with collapsed payload, categorical source colors, Prism JSON tree
* [web] shared CheckboxFilterDropdown (traces and events pages)
* [web] shared formatBodyForDisplay for JSON/XML/form-encoded/s3 preview
