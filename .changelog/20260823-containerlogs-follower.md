~ [ecs] a task's `awslogs` output is followed with reconnect, exact de-duplication of the lines a
  timestamp-based resume replays, and batched CloudWatch writes, so a broken daemon connection no
  longer silently ends a task's logs; event timestamps are the container's own, not the host's
  arrival time
