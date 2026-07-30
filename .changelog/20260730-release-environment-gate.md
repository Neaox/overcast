---
section: Changed
area: ci
---

- **CI (release safety)** — publishing now requires the maintainer's one-click approval: the release workflow's four publish jobs run in a `release` environment with a required reviewer, so builds and tests stay unattended while nothing ships without a human seeing the exact SHA. Alongside the repository ruleset changes (required status checks, no bypass actors), routine merges no longer use `--admin` — the rules that were previously convention are now enforced, including on automation.
