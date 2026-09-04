~ [docs] README points at the docs hub instead of reproducing it, and is now linted for the house-style tells
  Fourteen routing rows were a second copy of `docs/README.md`, and the storage rules were inlined four times over; both are now the fact plus a link.
  `internal/docslint` reaches README.md through a new `Options.Overview`, which applies the tells rule and nothing else while keeping one shared allowlist.
