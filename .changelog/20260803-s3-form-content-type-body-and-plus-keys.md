* [s3/router] `PutObject` no longer discards the body when the request carries
  `Content-Type: application/x-www-form-urlencoded` — the content type many
  minimal HTTP clients send when none is set, `curl --data-binary` among them.
  Protocol detection read AWS Query fields out of the body before routing had
  decided who owned the request, which drained it: the write answered `200 OK`
  with the empty-string ETag and stored a zero-byte object, with nothing to tell
  the caller. Form fields are now read through a body-preserving parse, and only
  for `POST`, which is the only method the Query protocol puts them on. IAM
  enforcement did the same thing on the same requests and is fixed with it.
* [s3] An object key containing `+` is now stored decoded rather than as the
  literal `%2B` a client sends on the wire. Only characters Go leaves bare when
  it re-escapes a path were affected — `+`, `=`, `&` — which is why `%20` and
  multi-byte UTF-8 looked fine.
