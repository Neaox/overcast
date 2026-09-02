+ [router] Serve `/_health` and `/_localstack/health` as aliases of `/_overcast/health`, so a healthcheck carried over from LocalStack or from before #927 works.
  A 404 there is read by an orchestrator as a dead container: it restarts Overcast, and on the default in-memory state backend a restart wipes every resource a deploy in flight had created.
  `/_localstack/health` answers in LocalStack's own shape (a `services` map plus `edition` and `version`); the rest of `/_localstack/` now 404s with the Overcast endpoint that replaces it instead of an S3 error.
