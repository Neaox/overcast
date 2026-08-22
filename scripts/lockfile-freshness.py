#!/usr/bin/env python3
"""lockfile-freshness.py — was this pull request's lockfile generated against
the `main` it is about to merge into?

Prints one word on stdout — `not-applicable`, `fresh` or `stale` — and, with
`--details-file`, writes a short Markdown explanation for a job summary or a
check-run body. Exit status is 0 for any verdict and 2 for a usage or git
error; the caller decides what a verdict means, the same way
changelog-required.py is used.

Why this exists
---------------
`web/pnpm-lock.yaml` is one generated file whose peer-resolution keys refer to
each other: an entry is literally named
`vitest@4.1.5(jsdom@27.4.0)(vite@8.0.16(…))(…)`. Two copies of it regenerated
independently cannot be combined by merging their text, even when git reports
no conflict, because the keys one side wrote name versions the other side
changed. On 2026-08-22 five npm majors merged back to back, each carrying its
own regenerated lock, and left `main` with a lockfile still naming the pre-bump
jsdom — every pull request failed `pnpm install --frozen-lockfile` for 28
minutes (#1340).

Each of those pull requests was green. Its checks ran against the merge of its
head and *the `main` of that moment*, and nothing re-ran them when `main`
moved. GitHub's "require branches to be up to date" closes that for every file
at once, at the cost of a manual Update-branch on every pull request that
falls behind; a merge queue closes it properly, at the cost of `merge_group`
plumbing in every required workflow. This is the scoped version: the same
rule, for the one file it is known to matter for.

What it decides
---------------
A pull request touches the lockfile if the lockfile differs between its
merge-base with `main` and its head. If it does not, the verdict is
`not-applicable` — most pull requests, and the check passes without a word.

If it does, the lockfile on `main` at the merge-base is compared with the
lockfile on `main` *now*. Same blob: `fresh` — the pull request regenerated
against the lockfile `main` still has, so the merged result is the pull
request's lock and nothing else. Different blob: `stale` — `main`'s lock moved
since this branch last saw it, and the pull request's lock was generated
against one that no longer exists. Rebase (or merge `main` in) and run
`pnpm install` again; that regenerates the lock against what actually landed.

Compared by blob, not by history, on purpose. A pull request whose merge-base
is old but whose lockfile `main` has not touched since is fresh — that is the
whole point of scoping to the file rather than requiring the branch to be up to
date. And a `main` that changed the lockfile and then changed it back is fresh
again, because the bytes the pull request generated against are the bytes
`main` has.

`--base` is a live ref (`origin/main`), never the event payload's `base.sha`.
The payload is a snapshot from when the pull request was opened and is
replayed unchanged on every re-run (#813); this check is only useful if a
re-run after `main` moves gives the *new* answer.
"""

from __future__ import annotations

import argparse
import subprocess
import sys

DEFAULT_PATHS = ("web/pnpm-lock.yaml",)

VERDICT_NOT_APPLICABLE = "not-applicable"
VERDICT_FRESH = "fresh"
VERDICT_STALE = "stale"


def verdict(touched: bool, main_blob: str | None, merge_base_blob: str | None) -> str:
    """The decision for one path.

    `touched` is whether the pull request changes the path at all; the two
    blobs are the path's object id on `main` now and on `main` at the
    merge-base, `None` where the path does not exist there. A path the pull
    request does not touch is never stale, whatever `main` did to it: the
    merged result carries `main`'s copy, which is consistent with `main` by
    definition.
    """
    if not touched:
        return VERDICT_NOT_APPLICABLE
    if main_blob == merge_base_blob:
        return VERDICT_FRESH
    return VERDICT_STALE


def combine(verdicts: list[str]) -> str:
    """One verdict for several paths: any stale path makes the set stale."""
    if VERDICT_STALE in verdicts:
        return VERDICT_STALE
    if VERDICT_FRESH in verdicts:
        return VERDICT_FRESH
    return VERDICT_NOT_APPLICABLE


class GitError(RuntimeError):
    pass


def git(*args: str, cwd: str | None = None) -> str:
    proc = subprocess.run(
        ["git", *args],
        cwd=cwd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    if proc.returncode != 0:
        raise GitError(
            f"git {' '.join(args)} failed ({proc.returncode}): {proc.stderr.strip()}"
        )
    return proc.stdout.strip()


def blob_at(commit: str, path: str, cwd: str | None = None) -> str | None:
    """The object id of `path` in `commit`, or None if it is not there."""
    proc = subprocess.run(
        ["git", "rev-parse", "--verify", "--quiet", f"{commit}:{path}"],
        cwd=cwd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    if proc.returncode != 0:
        return None
    return proc.stdout.strip()


def touched_paths(merge_base: str, head: str, paths: tuple[str, ...], cwd: str | None = None) -> set[str]:
    """Which of `paths` the pull request changes between merge-base and head."""
    out = git("diff", "--name-only", "--no-renames", merge_base, head, "--", *paths, cwd=cwd)
    return {line.strip() for line in out.splitlines() if line.strip()}


def judge(head: str, base: str, paths: tuple[str, ...], cwd: str | None = None) -> tuple[str, list[tuple[str, str]], str]:
    """Judge one head against a live base ref.

    Returns the combined verdict, each path's own verdict, and the merge-base
    commit so a caller can say which commit the branch last shared with main.
    """
    merge_base = git("merge-base", base, head, cwd=cwd)
    touched = touched_paths(merge_base, head, paths, cwd=cwd)
    per_path = []
    for path in paths:
        per_path.append(
            (
                path,
                verdict(
                    path in touched,
                    blob_at(base, path, cwd=cwd),
                    blob_at(merge_base, path, cwd=cwd),
                ),
            )
        )
    return combine([v for _, v in per_path]), per_path, merge_base


def details(overall: str, per_path: list[tuple[str, str]], merge_base: str, base: str, head: str) -> str:
    """A Markdown explanation of the verdict, for a job summary or a check run."""
    short_base = merge_base[:10]
    lines = [f"**Lockfile freshness: {overall}**", ""]
    if overall == VERDICT_NOT_APPLICABLE:
        lines.append(
            "This pull request does not change a lockfile, so there is nothing to "
            "judge. Whatever `main` does to its lockfile, the merged result carries "
            "`main`'s copy."
        )
        return "\n".join(lines) + "\n"

    stale = [p for p, v in per_path if v == VERDICT_STALE]
    fresh = [p for p, v in per_path if v == VERDICT_FRESH]
    for path in fresh:
        lines.append(
            f"- `{path}`: regenerated against the copy `main` still has "
            f"(unchanged on `main` since `{short_base}`)."
        )
    for path in stale:
        lines.append(
            f"- `{path}`: **stale** — `main` changed this file after `{short_base}`, "
            f"the last commit this branch shares with it, and this pull request's "
            f"copy was generated against the old one."
        )
    if stale:
        lines += [
            "",
            "A lockfile's peer-resolution keys refer to each other, so two copies "
            "regenerated independently cannot be combined by merging their text — "
            "git reports no conflict and the result still fails "
            "`pnpm install --frozen-lockfile` (#1340). Rebase onto `main` (or merge "
            "it in), run `pnpm install` in `web/` to regenerate against what "
            "actually landed, and push. Renovate's pull requests rebase themselves.",
        ]
    else:
        lines += ["", f"Judged `{head[:10]}` against `{base}`."]
    return "\n".join(lines) + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    check = sub.add_parser("check", help="judge one head against a live base ref")
    check.add_argument("--head", required=True, help="the pull request's head commit")
    check.add_argument("--base", required=True, help="the live base ref, e.g. origin/main — never the event payload's base.sha")
    check.add_argument(
        "--path",
        action="append",
        dest="paths",
        help=f"a lockfile to judge; repeatable (default: {', '.join(DEFAULT_PATHS)})",
    )
    check.add_argument("--details-file", help="write a Markdown explanation here")
    check.add_argument("-C", dest="cwd", help="run git in this directory")
    args = parser.parse_args(argv)

    paths = tuple(args.paths) if args.paths else DEFAULT_PATHS
    try:
        overall, per_path, merge_base = judge(args.head, args.base, paths, cwd=args.cwd)
    except GitError as err:
        print(f"lockfile-freshness: {err}", file=sys.stderr)
        return 2

    text = details(overall, per_path, merge_base, args.base, args.head)
    if args.details_file:
        with open(args.details_file, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(text)
    print(text, file=sys.stderr, end="")
    print(overall)
    return 0


if __name__ == "__main__":
    sys.exit(main())
