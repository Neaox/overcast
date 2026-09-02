+ [compat/testcontainers] the LocalStack Testcontainers modules for Java, Node, Python, Go and .NET start the Overcast image with only the image name changed.
  Overcast prints LocalStack's `Ready.` line once every listener is bound, and the image answers `/usr/local/bin/docker-entrypoint.sh`; per-language snippets and each module's tag rule are in docs/testcontainers.md
+ [config] `ECS_DOCKER_FLAGS`, `EC2_DOCKER_FLAGS` and `BATCH_DOCKER_FLAGS` join the recognised-but-inert LocalStack variables.
  the Java Testcontainers module sets all three inside the container; Overcast labels the containers it starts itself, so they are named at startup rather than silently unknown
