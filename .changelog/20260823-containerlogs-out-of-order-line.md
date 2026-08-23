* [lambda] a line a function printed is no longer dropped from CloudWatch Logs and the
  `X-Amz-Log-Result` tail when the daemon wrote it out of timestamp order — which it does
  whenever stdout and stderr are written close together, since each stream has its own
  copier: a Python handler's first `print` regularly lost to a `sys.stderr` write that landed
  first. The follower's reconnect de-duplication treated "older than the newest line seen" as
  "already delivered" on every connection; it now does so only for the replay a reconnect
  asks for, up to the watermark it resumed from
* [ecs] the same for a task's `awslogs` output, which has followed the same path since the
  shared follower landed; and after a reconnect, the lines a traceback prints within one
  nanosecond are no longer dropped past the first
