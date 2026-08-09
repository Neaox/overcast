* [debug/cloudformation] request traces record every internal hop, including the
  work that continues after the response is sent — a CloudFormation or CDK deploy
  runs for minutes past its HTTP reply, and every hop after the first second used
  to be dropped
* [cloudformation/debug] `DeleteStack` and `RollbackStack` record the internal
  calls they make, as `CreateStack` and `UpdateStack` already did — teardown hops
  were previously never recorded at all
* [debug] a hop links reliably to the request it triggered, whichever protocol
  answered it; S3 hops in particular used to link to nothing
~ [web/debug] the Request Traces status and method filters are multi-select, so
  "all errors" is 4xx and 5xx ticked together rather than two separate looks;
  3xx joins the status list
~ [web/debug] Request Traces filters live in the URL, so they survive Back from
  a trace's detail page and can be shared as a link
* [web/debug] long request paths no longer stretch the traces table off-screen —
  the Path column is bounded against the page's own width and carries the full
  path as a tooltip
* [web/debug] the traces table's horizontal scrollbar is reachable without
  scrolling to the bottom of an infinitely growing list
