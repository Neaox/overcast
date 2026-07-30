---
section: Changed
area: ci
---

- **Release process (changelog)** — unreleased changes are now recorded as one fragment file per PR under `.changelog/` instead of direct edits to `CHANGELOG.md`'s `[Unreleased]` section, which every concurrent PR used to merge-conflict over. CI lints the fragments and keeps `[Unreleased]` empty (`scripts/changelog.py check`); release prep assembles the fragments into the versioned section (`scripts/changelog.py assemble`), and the release gate fails if any fragment is left unconsumed.
