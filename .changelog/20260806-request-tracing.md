+ [router] new `/_debug/trace/*` endpoints for full request tracing — request/response
bodies, structured log entries, and AWS errors. Active when `OVERCAST_DEBUG=true`;
traces are looked up by the request ID already returned in every response
(`x-amzn-requestid` / `x-amz-request-id`). Includes BFF proxy routes for the web UI.
