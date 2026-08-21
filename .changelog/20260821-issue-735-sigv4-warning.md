* [docs] `OVERCAST_SIGV4_VALIDATE`'s startup log and configuration-reference row said SigV4
  validation was "not yet implemented — all requests are accepted" while the middleware was
  validating signatures and answering a bad one with 403 `InvalidSignatureException`. The flag's
  own signals actively misdirected an operator away from the real cause of signature failures;
  the log line and the docs now describe what the flag does, including where signing secrets
  come from (IAM user access keys and STS session credentials, with the local-dev `test`
  fallback). Validation behaviour itself is unchanged.
