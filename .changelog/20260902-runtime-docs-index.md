~ [build] the docs navigation and search index are derived at runtime instead of generated into the repository.
  `web/src/docs-nav.gen.ts` and `internal/docssearch/index.gen.jsonl` are deleted. Both were sorted one-entry-per-page manifests that every docs PR rewrote, so two docs branches conflicted on files nobody had written by hand — all three docs PRs open at the time conflicted pairwise on the search index, including a pair whose Markdown did not overlap at all
  `internal/docsindex` parses `docs/` — the same set `embed.go` compiles into the binary — and `internal/bff` serves it from a new `GET /api/docs/nav`, beside the `/api/docs/page` the console already called
  `docs/dev/generated-files.md` is the new inventory: every generated artefact, whether it is committed, build output or derived at runtime, and the rule that decides which
~ [docs] editing a published doc needs no regeneration step any more.
  `make docs-index` is replaced by `make docs-lint`, which checks frontmatter, in-page anchors, service page structure and the description budget
~ [web] the console fetches its docs sidebar and page outline instead of importing a 7,332-line generated module.
  that data leaves the SPA bundle; the docs page already needed the Go BFF for every page body it renders
