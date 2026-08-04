#!/usr/bin/env python3
"""ci-scope.py — decide whether a pull request needs the expensive CI jobs.

Reads the changed paths on stdin, one per line, and prints `code=true` or
`code=false` for a workflow to gate jobs on.

A pull request that only edits prose cannot change what the emulator does, and
the Go suite, the SPA build and the image builds are the bulk of a CI run.

Why this is a classifier and not a `paths-ignore:` on the workflow
------------------------------------------------------------------
Nine of test.yml's jobs are in the branch ruleset's required set, and a
required check that never reports leaves the pull request waiting on it
forever. `paths-ignore:` skips the whole workflow, so those checks would never
report at all.

The obvious repair — a twin "skip" workflow declaring the same job names — is
broken for the mixed case. GitHub skips on `paths-ignore` only when *every*
changed file matches, but runs on `paths` when *any* one does, so a pull
request touching a doc and a handler satisfies both and two different runs
report under the same check name.

So the jobs keep their names and always run; only their expensive steps are
conditional. A prose-only pull request gets the same green checks, in seconds
instead of minutes.

Bias: anything unrecognised counts as code. A false `true` costs a CI run; a
false `false` skips the suite on a change that needed it, which is the failure
this must not have. An empty list is therefore `true` as well — not knowing
what changed is not the same as knowing nothing did.
"""

import os
import sys

# Paths that cannot change emulator behaviour. Deliberately short.
#
# Generated files are why it can be: editing a published doc also regenerates
# internal/docssearch/index.gen.go and web/src/docs-index.gen.ts, neither of
# which is matched here, so a docs change that touches the index is code and
# runs everything. docs/plans/ and docs/dev/ are not published and regenerate
# nothing, which is what makes a plan-doc edit — this file's main beneficiary —
# genuinely free.
PROSE_SUFFIXES = (".md",)
PROSE_PREFIXES = (
    "docs/",
    ".changelog/",
)


def is_prose(path: str) -> bool:
    """Is this a file no build, test or image reads?"""
    if path.endswith(PROSE_SUFFIXES):
        return True
    return path.startswith(PROSE_PREFIXES)


def needs_code_jobs(changed: list[str]) -> bool:
    """Does this change set need the expensive jobs?"""
    if not changed:
        return True
    return any(not is_prose(p) for p in changed)


def main() -> int:
    # Anything that is not a pull request — a push to main, a release, a manual
    # dispatch — runs the lot. This exists to make review cheaper, not to
    # decide what main is allowed to skip.
    if os.environ.get("EVENT_NAME") != "pull_request":
        print("code=true")
        return 0

    paths = [line.strip() for line in sys.stdin if line.strip()]
    print(f"code={'true' if needs_code_jobs(paths) else 'false'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
