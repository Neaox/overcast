*! [config] `OVERCAST_HOST` renamed to `OVERCAST_LISTEN` (LocalStack's `GATEWAY_LISTEN` idiom) and removed rather than kept as an alias — a leftover `OVERCAST_HOST` now fails at startup naming `OVERCAST_LISTEN` as the replacement, instead of being silently ignored
  migration: rename `OVERCAST_HOST` to `OVERCAST_LISTEN` wherever it is set (env, `.env` files, `docker-compose.yml`, CI config) — the value format is unchanged (the same comma-separated address list)
+ [config] a startup log line names the resolved bind address(es) and that `OVERCAST_LISTEN` changes them
* [config] fixed a code comment that falsely claimed `OVERCAST_HOST` is equivalent to LocalStack's `LOCALSTACK_HOST` — the true analogue is `OVERCAST_HOSTNAME`
