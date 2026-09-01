~ [docs] removed dead citations to internal-only `docs/plans/**` and `docs/dev/**` content from published pages, per a content audit.
  affected six service docs, the migration guide, README, cdk, networking, storage, performance, efs, route53, cloudformation, autoscaling
  also tightened repetitive or meta-commentary prose across those pages
+ [docs] added a one-page content charter (`docs/dev/content-charter.md`) for published docs.
  covers citation, prose-economy, and table-vs-prose rules; referenced from `CONTRIBUTING.md` and `AGENTS.md`
+. [docs] `scripts/docs-index.go --check` now rejects a published doc that cites `docs/dev/**` or `docs/plans/**`.
  catches both a literal path and a resolved Markdown link
  also rejects a doc whose frontmatter `description` exceeds 220 characters
