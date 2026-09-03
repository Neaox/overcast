~ [docs] Confirmed the CDK, CLI, SDK, local-dev, Testcontainers and debug-endpoint guides against the code, and corrected what had drifted.
  CDK limitations pointed at `OVERCAST_LOG_LEVEL=debug` to find stubbed resources; the `Overcast:`-prefixed `ResourceStatusReason` is the actual route.
  `docs/cli/networks.md` had no link back to the CLI reference, `docs/sdk-cli.md` none to the environment variable reference, and `docs/cli.md` never mentioned `overcast --version`.
  Refreshed the pinned image tag in the Testcontainers examples, and cut the house-habit "simply"/"just" wording.
