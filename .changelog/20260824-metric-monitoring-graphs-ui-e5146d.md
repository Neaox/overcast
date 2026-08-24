+ [apigateway/web] Monitor tab on the REST and HTTP API detail pages, charting requests, 4XX/5XX errors, and latency per API or per stage
~ [apigateway] request metrics are recorded under every AWS-documented dimension combination — `ApiName` and `ApiName`+`Stage` (REST), `ApiId` and `ApiId`+`Stage` (HTTP) alongside the detailed per-route set — so CloudWatch queries at the API or stage level now return data
~! [apigateway] HTTP (v2) API error metrics are recorded under AWS's real metric names, `4xx` and `5xx`
  migration: CloudWatch queries or alarms watching `4XXError`/`5XXError` on an HTTP API must switch to `4xx`/`5xx`; REST API metric names are unchanged
+ [web] Monitor charts: expand any card into a full-width dialog with drag-to-zoom into a time window, a y-axis scale, and per-series whole-range summaries in the legend
* [web] a Monitor chart bucket with no adjacent data now renders as a visible dot; it previously painted nothing, leaving a chart that looked empty despite real data
+ [web] Monitor charts gained labeled X/Y axes with aligned gridlines, an AWS-console-style auto-refresh interval picker (Off/10s/30s/1m/5m), and a bucket-width chip; the time window holds still while auto-refresh is off or a chart is being inspected
~ [metrics] the 5-minute rollup tier is retained for 30 days (previously 7), and the 30-day Monitor view charts 15-minute buckets instead of 1-hour ones
