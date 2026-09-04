~ [ci] The real-engine RDS master-user tests retry an engine container the Docker daemon declined to start, and say what the container did when they fail.
  a daemon that cannot start the engine image at all is now a skip naming the reason, rather than a failure that reads as an RDS regression.
  a failure quotes the container's state and the last 40 lines of its output, where the reason on the record was one truncated line.
