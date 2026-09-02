+ [router] `/_localstack/init`, `/_localstack/init/{stage}` and `POST /_localstack/state/reset` are served as aliases of their `/_overcast/` originals.
  each runs the same handler as the endpoint it aliases, so the two paths cannot drift; the init shape already matched LocalStack's, stage names and script states included
~ [router] the `/_localstack/` 404 now names `diagnose`, `config` and `usage` too, and no longer points at paths Overcast serves outright.
