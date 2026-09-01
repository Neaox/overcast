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
# A *published* doc is not one of them. embed.go compiles docs/*.md, docs/cdk
# and docs/services into the binary, and internal/docsindex derives the console
# navigation and the search corpus from exactly that set at runtime — so a
# published doc is an input to the Go build and to tests that assert on it
# (internal/docsindex/corpus_test.go pins search rankings over the real
# corpus). Editing one is a code change and runs everything.
#
# This used to fall out by accident: a docs edit also had to regenerate two
# committed index artifacts, and those were matched as code. The artifacts are
# gone (see internal/docsindex), so the rule is stated directly rather than
# depending on a side effect that no longer happens.
#
# docs/plans/ and docs/dev/ are contributor-only. They are excluded from the
# embed pattern and from the index, so nothing builds or tests them — which is
# what keeps a plan-doc edit, this file's main beneficiary, genuinely free.
PROSE_SUFFIXES = (".md",)
PROSE_PREFIXES = (
    "docs/plans/",
    "docs/dev/",
    ".changelog/",
)

# Published docs: read by the build and by the tests, whatever their suffix.
CODE_PREFIXES = ("docs/",)


def is_prose(path: str) -> bool:
    """Is this a file no build, test or image reads?"""
    if path.startswith(PROSE_PREFIXES):
        return True
    if path.startswith(CODE_PREFIXES):
        return False
    return path.endswith(PROSE_SUFFIXES)


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
