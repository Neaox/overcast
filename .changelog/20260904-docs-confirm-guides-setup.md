~ [docs] Confirmed the CDK, CLI, SDK, local-dev, Testcontainers and debug-endpoint guides against the code, and corrected what had drifted.
  CDK limitations pointed at `OVERCAST_LOG_LEVEL=debug` to find stubbed resources; the `Overcast:`-prefixed `ResourceStatusReason` is the actual route.
  `docs/cli/networks.md` had no link back to the CLI reference, `docs/sdk-cli.md` none to the environment variable reference, and `docs/cli.md` never mentioned `overcast --version`.
  Turned the `/_aws/` compatibility prose in docs/debug-endpoints.md into a parameter table, refreshed the pinned Testcontainers image tag, and cut the "simply"/"just" house habits.
