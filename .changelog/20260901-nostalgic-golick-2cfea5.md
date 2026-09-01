* [ecs] a drained or deleted service's tasks left their `internal.ecs.pause` network-namespace containers running on the daemon.
  stayed until the background container GC got to them, while the same teardown removed the application containers inline
  the service scheduler's scale-down and a failed launch's unwind now take the namespace container down before returning, so the API call's return means the task's containers are actually gone
* [compat] the post-run leaked-container audit read one `docker ps` snapshot the instant the resource sweep finished.
  containers whose removal the emulator had already queued — ECS pause containers, Lambda execution environments, ElastiCache nodes — were reported as leaks on nearly every CI run
  it now polls for up to 30 s and reports only the containers that survive the grace period
