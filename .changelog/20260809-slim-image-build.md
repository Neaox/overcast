* [docker] the published `overcast-slim` image is a slim build. Every caller
  that selected the slim stage had to pass a matching `NOSQLITE=1` build arg as
  well, and the release workflow did not, so `-slim` shipped the console binary
  — embedded web console, `/_mcp`, SQLite and all, the same size as the console
  image. The build target now decides the flavour on its own, and each builder
  asserts what it produced
~! [docker] `overcast-slim` has no SQLite, so a volume mounted at `/data` no
  longer gives it persistent storage: `auto` resolves to memory, and `hybrid`
  and `persistent` refuse to start. This is what the image was always
  documented to do — it only appeared to persist because the wrong binary was
  shipped
  migration: use `wal`, which is durable and needs no SQLite, or switch to the
  full `overcast` image
