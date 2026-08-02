#!/usr/bin/env python3
"""changelog-required.py — does this pull request still owe a changelog fragment?

A forgotten fragment and a fragment nobody needed look identical from outside
the change: both are an empty `.changelog/` diff. Only the author knows which
one it is, so this asks, and takes the answer as a comment on the pull request:

    /no-changelog <reason>     nothing here belongs in the release notes
    /needs-changelog           put the question back

The reason is required, and it is kept. The point is not the acknowledgement —
it is that somebody decided, said why, and a reviewer can read the why instead
of guessing. A bare tick would become a reflex, and a reflex is
indistinguishable from forgetting, which is the thing being caught.

Three exemptions, none of them a judgement about the change:

  * The PR adds or edits a fragment. Nothing to ask.
  * Every file the PR touches is in an area that never produces a release
    note — `EXEMPT_*` below. Every one: a single file outside them puts the
    whole PR back in scope, because that file is the one that might ship.

Editing `CHANGELOG.md` is never an answer here, in any window. Only the release
PR touches that file: while one is open, `release-prep.yml` merges `main` into
its branch on every push, and a second hand editing the same region does not
merely conflict — the bot aborts the merge and the release PR stops refreshing
itself until somebody untangles it. Fragments exist precisely so that concurrent
PRs never meet in one file. See RELEASE.md § Keeping The Release PR Current.

Three questions, deliberately apart:

    exempt(path)               can a change to this file ever be release-noted?
    parse_command(body)        is this comment a waiver command, and a valid one?
    latest_waiver(comments)    what is the standing answer on this PR?

Never exits non-zero except on a usage error. The workflow decides what a
verdict means; a comments file it cannot read degrades to "no waiver", which
asks the question again rather than letting a change through unrecorded.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path


WAIVE = "/no-changelog"
REVOKE = "/needs-changelog"

# GitHub computes author_association from the commenter's relationship to the
# repository; it is not something the commenter can set. Same set as
# /retarget: everyone else is ignored rather than refused, so the command is
# not a way to make the bot talk.
WAIVER_ROLES = frozenset({"OWNER", "MEMBER", "COLLABORATOR"})

# Long enough that "no" and "n/a" do not clear it, short enough that a real
# reason written in passing does.
MIN_REASON = 10
MAX_REASON = 500

VERDICT_FRAGMENT = "fragment"
VERDICT_NOT_APPLICABLE = "not-applicable"
VERDICT_WAIVED = "waived"
VERDICT_MISSING = "missing"

# Areas whose contents cannot reach a user of Overcast, so a change confined to
# them can never owe a release note.
#
# The test each entry had to pass is "can this file's content reach a released
# artifact or a page a user reads?", answered by inspection rather than by how
# the change feels. Deliberately narrow, because a wrong entry here ships a
# change with no note and nobody asked — the exact failure this check exists to
# catch — while a missing entry costs one comment.
#
# What is left out, on purpose: everything under `.github/` bar the agent
# instructions, because that is where the workflows that build, tag and publish
# live and a bright line there is easier to trust than a list of sub-exceptions;
# `scripts/`, which runs the release; published `docs/`, which is user-facing
# guidance; `Makefile`, `Dockerfile`, `docker-compose*.yml` and `.dockerignore`,
# which decide what the artifacts contain; and `README.md`, `RELEASE.md` and
# `STATUS.md`, which users read.
EXEMPT_PREFIXES = (
    # The suites observe the emulator rather than change it, however large the
    # diff. Stated policy: .changelog/README.md § What merits a fragment. The
    # package is imported only by cmd/compat — never by cmd/overcast or
    # internal/ — and cmd/compat is not one of the shipped binaries: every
    # release target in the Makefile and the Dockerfile builds ./cmd/overcast.
    "compat/",
    "cmd/compat/",
    # Test-only. tests/ is the integration tree; *_test.go is the unit half,
    # which lives beside the code it tests.
    "tests/",
    # Working documents. scripts/docs-index.go skips both outright, so they are
    # not published pages — the repo's own rule that plan docs stay accurate
    # per-commit also makes this the highest-traffic exemption here.
    "docs/plans/",
    "docs/dev/",
    # Agent instructions, repo-local skills, and the editor/dev-container
    # setup. None of it is built, shipped or read by a user.
    ".agents/",
    ".claude/",
    ".vscode/",
    ".devcontainer/",
)
# Test files, by each language's own convention: Go's `_test.go` (which the
# toolchain itself excludes from a build), the `scripts/*_test.py` pattern CI
# loops over, and vitest's `.test.ts[x]` / `.spec.ts[x]` under web/. None of the
# three is reachable from a build, so a change confined to them ships nothing.
EXEMPT_SUFFIXES = (
    "_test.go",
    "_test.py",
    ".test.ts",
    ".test.tsx",
    ".spec.ts",
    ".spec.tsx",
)
EXEMPT_FILES = frozenset(
    {
        # Contributor and agent guidance. AGENTS.md's siblings are symlinks to
        # it; a user of Overcast never reads any of them.
        "AGENTS.md",
        "CLAUDE.md",
        "CONTRIBUTING.md",
        ".github/copilot-instructions.md",
        # How to write a fragment, which is not itself a fragment.
        ".changelog/README.md",
        # Local tooling and repo hygiene: lint rules and hot-reload config
        # change no output, and git's own metadata files ship nowhere.
        # .dockerignore is deliberately *not* here — it decides what goes into
        # the image.
        ".golangci.yml",
        ".air.toml",
        "opencode.json",
        ".codex",
        ".gitignore",
        ".gitattributes",
    }
)

COMMAND_NONE = "none"
COMMAND_WAIVE = "waive"
COMMAND_REVOKE = "revoke"
COMMAND_INVALID = "invalid"

NO_REASON = (
    f"`{WAIVE}` needs a reason after it — a few words on why this change has "
    "nothing a user would read in the release notes. It is kept as the record "
    "of the decision, so \"n/a\" does not count."
)


@dataclass
class Command:
    """What a comment turned out to be, and why it was not accepted."""

    kind: str
    reason: str = ""
    # One sentence for the bot to reply with; "" unless kind is invalid.
    problem: str = ""


@dataclass
class Waiver:
    """A standing waiver: what was said, by whom, and where."""

    reason: str
    by: str
    url: str = ""


def exempt(path: str) -> bool:
    """Can a change to this file ever be worth a release note?

    Path only. Nothing here reads the diff: a rule that depended on *what*
    changed inside a file would be a judgement about the change, and judging
    the change is the thing only the author can do.
    """
    # Not lstrip("./"): that strips a character *set*, and every exempt dotted
    # path — .agents/, .changelog/README.md — would lose its leading dot and
    # stop matching.
    path = path.strip()
    if path.startswith("./"):
        path = path[2:]
    if not path:
        return False
    return (
        path in EXEMPT_FILES
        or path.startswith(EXEMPT_PREFIXES)
        or path.endswith(EXEMPT_SUFFIXES)
    )


def in_scope(paths: list[str]) -> list[str]:
    """The files that put this PR in scope — empty when none of them do."""
    return [path for path in paths if path.strip() and not exempt(path)]


def clean_reason(text: str) -> str:
    """One line, no HTML comments, bounded length.

    The reason is contributor prose that the bot quotes back in a comment of
    its own. It cannot execute anything, but an HTML comment inside it could
    imitate the marker the bot finds its own comments by — and a wall of text
    in a blockquote helps nobody.
    """
    text = re.sub(r"<!--.*?-->", "", text, flags=re.S)
    text = text.replace("<!--", "").replace("-->", "")
    text = " ".join(text.split())
    if len(text) > MAX_REASON:
        text = text[: MAX_REASON - 1].rstrip() + "…"
    return text


def parse_command(body: str) -> Command:
    """Read a comment as a waiver command, if it is one at all.

    The command is the first non-empty line, as with /retarget — so the bot's
    own message, which mentions both commands in the middle of a sentence, is
    never mistaken for one. Everything after the command word is the reason,
    including later lines, because a reason is prose and prose wraps.
    """
    lines = body.replace("\r\n", "\n").split("\n")
    index = 0
    while index < len(lines) and not lines[index].strip():
        index += 1
    if index == len(lines):
        return Command(COMMAND_NONE)

    word, _, rest = lines[index].strip().partition(" ")
    if word == REVOKE:
        return Command(COMMAND_REVOKE)
    if word != WAIVE:
        return Command(COMMAND_NONE)

    reason = clean_reason(" ".join([rest, *lines[index + 1 :]]))
    if len(reason) < MIN_REASON:
        return Command(COMMAND_INVALID, problem=NO_REASON)
    return Command(COMMAND_WAIVE, reason=reason)


def commenter(comment: dict) -> str:
    """The login, from either the API shape or the flattened one CI passes."""
    if comment.get("login"):
        return str(comment["login"])
    user = comment.get("user") or {}
    return str(user.get("login") or "")


def latest_waiver(comments: list[dict]) -> Waiver | None:
    """The standing answer on this PR: the last accepted command wins.

    Skipped, in order: comments from someone without write access, comments
    that are not commands, and commands that are malformed. A malformed
    command is answered on the PR when it is posted — it must not silently
    revoke a good waiver already standing, and it must not grant one either.
    """
    standing: Waiver | None = None
    ordered = sorted(comments, key=lambda c: (str(c.get("created_at") or ""), c.get("id") or 0))
    for comment in ordered:
        if comment.get("author_association") not in WAIVER_ROLES:
            continue
        command = parse_command(str(comment.get("body") or ""))
        if command.kind == COMMAND_WAIVE:
            standing = Waiver(
                reason=command.reason,
                by=commenter(comment),
                url=str(comment.get("html_url") or ""),
            )
        elif command.kind == COMMAND_REVOKE:
            standing = None
    return standing


def load_comments(path: str) -> list[dict]:
    """The PR's comments, or none of them.

    An unreadable file means the question gets asked again, never that a
    change slips through as if it had been waived.
    """
    if not path:
        return []
    try:
        data = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as err:
        print(f"::warning::could not read {path} ({err}); assuming no waiver", file=sys.stderr)
        return []
    return [item for item in data if isinstance(item, dict)] if isinstance(data, list) else []


def load_changed(path: str) -> list[str]:
    """Every path the PR touches, one per line; none of them if unreadable.

    Degrades towards asking: an empty list is never all-exempt, so a missing
    file makes the question get asked rather than skipped.
    """
    if not path:
        return []
    try:
        text = Path(path).read_text(encoding="utf-8")
    except OSError as err:
        print(f"::warning::could not read {path} ({err}); assuming nothing is exempt", file=sys.stderr)
        return []
    return [line.strip() for line in text.splitlines() if line.strip()]


def command_check(args: argparse.Namespace) -> int:
    """Print one verdict word; write the waiver's details when there is one."""
    if [path for path in args.fragments if path.strip()]:
        print(VERDICT_FRAGMENT)
        return 0

    changed = load_changed(args.changed_files_file)

    # No CHANGELOG.md carve-out, deliberately. A release window forbids
    # fragments, but the way out of that is to wait for the tag or to waive —
    # never to hand-edit the file the release PR owns.
    scoped = in_scope(changed)
    if changed and not scoped:
        print("::notice::Every file changed here is in an area that never produces a release note.", file=sys.stderr)
        print(VERDICT_NOT_APPLICABLE)
        return 0
    if scoped:
        print(f"In scope because of: {', '.join(sorted(scoped)[:20])}", file=sys.stderr)

    waiver = latest_waiver(load_comments(args.comments_file))
    if waiver is None:
        print(VERDICT_MISSING)
        return 0

    if args.waiver_file:
        Path(args.waiver_file).write_text(
            json.dumps({"reason": waiver.reason, "by": waiver.by, "url": waiver.url}),
            encoding="utf-8",
        )
    print(VERDICT_WAIVED)
    return 0


def command_parse(args: argparse.Namespace) -> int:
    """Classify one comment body: none | waive | revoke | invalid."""
    body = Path(args.body_file).read_text(encoding="utf-8") if args.body_file else sys.stdin.read()
    command = parse_command(body)
    if args.problem_file:
        Path(args.problem_file).write_text(command.problem, encoding="utf-8")
    print(command.kind)
    return 0


def use_utf8_stdio() -> None:
    """Write UTF-8 whatever the ambient console encoding is (Windows: cp1252).

    A waiver reason is contributor-written and routinely has an em dash or a
    curly quote in it.
    """
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8")


def main() -> int:
    use_utf8_stdio()
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    check = subparsers.add_parser("check", help="verdict for a PR's changelog fragment")
    check.add_argument(
        "--changed-files-file",
        default="",
        help="every path the PR touches, one per line",
    )
    check.add_argument(
        "--comments-file", default="", help="JSON array of the PR's issue comments"
    )
    check.add_argument(
        "--waiver-file", default="", help="write the standing waiver here as JSON"
    )
    check.add_argument("fragments", nargs="*", help="fragment paths this PR adds")
    check.set_defaults(func=command_check)

    parse = subparsers.add_parser("parse", help="classify one comment body")
    parse.add_argument("--body-file", default="", help="read the body here rather than stdin")
    parse.add_argument("--problem-file", default="", help="write the refusal sentence here")
    parse.set_defaults(func=command_parse)

    args = parser.parse_args()
    return int(args.func(args))


if __name__ == "__main__":
    sys.exit(main())
