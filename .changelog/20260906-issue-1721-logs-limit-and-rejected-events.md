* [cloudwatch-logs] `DescribeLogGroups` and `DescribeLogStreams` honour `limit` and page with `nextToken`.
  the page defaults to and is capped at AWS's documented 50 items; an unrecognised `nextToken` reports `InvalidParameterException` instead of silently restarting at page one.
*! [cloudwatch-logs] `PutLogEvents` enforces AWS's ingestion window and batch ordering.
  an event more than 2 hours ahead, older than 14 days, or older than the log group's retention is discarded behind a `200` and reported in `rejectedLogEventsInfo`, as on AWS.
  a batch whose events are not in chronological order is refused whole with `InvalidParameterException`.
  migration: send current timestamps and sort each batch by `timestamp` — events outside the window are no longer stored, and the field is the only signal they were dropped.
+ [cloudwatch-logs] `FilterLogEvents` returns an `eventId` for every event, and `GetLogRecord` resolves one back to that event.
