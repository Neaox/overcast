~! [release] the GitHub org and Go module path moved to `overcast-sh`.
  repo `github.com/Neaox/overcast` -> `github.com/overcast-sh/overcast`; images now publish as `ghcr.io/overcast-sh/overcast[:tag]`
  migration: update import paths from `github.com/Neaox/overcast` to `github.com/overcast-sh/overcast` (pre-rename module versions stay fetchable at the old path); pull `ghcr.io/overcast-sh/overcast[:tag]` instead of `ghcr.io/neaox/overcast[:tag]`; old GitHub URLs under `github.com/Neaox/overcast` redirect automatically
