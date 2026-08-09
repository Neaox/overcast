* [lambda] a failed `CreateFunction` or `UpdateFunctionCode` no longer leaves the
  stored deployment package ahead of the function's `CodeSha256`, `RevisionId` and
  source metadata, so a later read or cold start cannot run code the configuration
  does not describe
