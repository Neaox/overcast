+ [cli] `overcast start` / `stop` / `restart` manage a background daemon instance natively, no Docker required
+ [cli] `overcast start --docker` runs the instance as a container instead of a native process, with --image/--channel/--data-volume/--mount-docker-socket options
+ [cli] `overcast logs` streams a background instance's log output, natively or from its container, with -f/--follow and -n/--tail
+ [cli] `overcast status` lists every registered instance alongside its existing single-endpoint health check.
  shown per instance: name, backend, endpoint, and running/stopped/unknown state
