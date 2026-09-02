* [ecr] shutdown now waits for the daemon to confirm the registry container is actually removed, instead of giving up once its name merely stopped resolving.
  the container's AutoRemove can race Docker's own exit-triggered removal against our explicit one; only the daemon's own `/wait?condition=removed` signal can tell them apart from a stalled removal.
