#!/usr/bin/env python3
"""release-hold.py — must this pull request wait for the release in flight?

A minor or patch release promises that nothing in it breaks. Its section of
CHANGELOG.md is written and reviewed while the release PR is open, so a
breaking change merged to `main` in that window is folded into a section
somebody already read — and ships under a version number that told users it
was safe. While such a release is open, breaking changes wait.

Two questions, deliberately apart, because only the first is policy:

    holds(version)          does a release of this version hold breaking changes?
    breaking_entries(paths) which entries in these fragments are breaking?

The second is just the fragment grammar, already parsed by changelog.py — the
'!' marker, and Removed, which is breaking unless it says otherwise.

Never exits non-zero except on a usage error. The workflow decides what a
verdict means; a version this cannot parse degrades to 'not holding' with a
warning rather than wedging every PR behind an unparseable VERSION file.
"""

from __future__ import annotations

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import changelog  # noqa: E402  (path set above so this runs from any cwd)


# Core of a semantic version. Prerelease and build metadata are matched but
# unused: 1.2.0-rc.1 is a minor release that happens not to have shipped yet,
# and holds exactly as 1.2.0 does.
VERSION_CORE = re.compile(r"^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$")

VERDICT_NO_RELEASE = "no-release"
VERDICT_NOT_HOLDING = "not-holding"
VERDICT_CLEAR = "clear"
VERDICT_HELD = "held"


@dataclass
class Hold:
    """Whether a release of some version holds breaking changes, and why."""

    holds: bool
    # "minor" or "patch" when holding; "" otherwise. The word the message uses.
    kind: str
    # One clause, phrased to follow "…because": for the workflow log only.
    reason: str
    next_major: str = ""


def holds(version: str) -> Hold:
    """Does a release of `version` hold breaking changes out of `main`?

    Only a major release is allowed to break, so every other release holds —
    with one carve-out. Pre-1.0 promises nothing: 0.x versions make no
    compatibility claim a user could rely on, and Overcast spends its whole
    alpha there, bumping a prerelease counter. Holding on those would fire on
    every release without protecting a promise anyone was given. Breaking
    entries are still marked and still carry a migration note; see
    .changelog/README.md, which says the marker is there to tell users what
    broke, not to move a version number.
    """
    match = VERSION_CORE.match(version.strip())
    if match is None:
        return Hold(False, "", f"'{version}' is not a semantic version")

    major, minor, patch = (int(group) for group in match.groups())
    if major == 0:
        return Hold(False, "", f"{version} is pre-1.0 and promises no compatibility")
    if (minor, patch) == (0, 0):
        return Hold(False, "", f"{version} is a major release, which may break")

    return Hold(
        holds=True,
        kind="patch" if patch else "minor",
        reason=f"{version} is a {'patch' if patch else 'minor'} release",
        next_major=f"{major + 1}.0.0",
    )


def breaking_entries(paths: list[Path]) -> list[tuple[Path, list[changelog.Entry]]]:
    """Breaking entries in the given fragments, grouped by file, files in order.

    A fragment that does not parse is skipped rather than reported: the
    'Changelog fragments' check already lints every fragment on every PR, and
    failing twice for one malformed file would just bury the message that
    actually says what is wrong with it.
    """
    found: list[tuple[Path, list[changelog.Entry]]] = []
    for path in paths:
        if not path.is_file():
            continue
        fragment, errors = changelog.parse_fragment(path)
        if fragment is None:
            print(f"skipping {path}: {len(errors)} parse error(s)", file=sys.stderr)
            continue
        entries = [entry for entry in fragment.entries if entry.breaking]
        if entries:
            found.append((path, entries))
    return found


def render_entries(found: list[tuple[Path, list[changelog.Entry]]]) -> str:
    """The breaking entries as markdown, grouped under the file to edit.

    Bullets come from changelog.render_bullet, so they read exactly as they
    will in CHANGELOG.md — including the migration note, which is the part a
    reviewer most wants to see before deciding anything.
    """
    blocks: list[str] = []
    for path, entries in sorted(found, key=lambda item: item[0].as_posix()):
        lines = [f"`{path.as_posix()}`", ""]
        for entry in entries:
            lines.extend(changelog.render_bullet(entry))
        blocks.append("\n".join(lines))
    return "\n\n".join(blocks) + "\n"


def command_check(args: argparse.Namespace) -> int:
    """Print one verdict word; write the entry markdown when there is any."""
    version = args.release_version.strip()
    if not version:
        print(VERDICT_NO_RELEASE)
        return 0

    decision = holds(version)
    if not decision.holds:
        print(f"::notice::No hold: {decision.reason}.", file=sys.stderr)
        print(VERDICT_NOT_HOLDING)
        return 0

    found = breaking_entries([Path(path) for path in args.fragments])
    if args.entries_file:
        Path(args.entries_file).write_text(
            render_entries(found) if found else "", encoding="utf-8"
        )
    print(VERDICT_HELD if found else VERDICT_CLEAR)
    return 0


def command_describe(args: argparse.Namespace) -> int:
    """Print `kind` or `next-major` for a version — the message's variables."""
    decision = holds(args.release_version)
    print(decision.kind if args.field == "kind" else decision.next_major)
    return 0


def use_utf8_stdio() -> None:
    """Write UTF-8 whatever the ambient console encoding is (Windows: cp1252).

    Fragment prose is contributor-written and routinely has an em dash or a
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

    check = subparsers.add_parser(
        "check", help="verdict for a PR against the release in flight"
    )
    check.add_argument(
        "--release-version",
        default="",
        help="version being prepared; empty when no release is in flight",
    )
    check.add_argument(
        "--entries-file", default="", help="write the breaking entries here as markdown"
    )
    check.add_argument("fragments", nargs="*", help="fragment paths this PR adds")
    check.set_defaults(func=command_check)

    describe = subparsers.add_parser("describe", help="one field of the hold decision")
    describe.add_argument("release_version")
    describe.add_argument("field", choices=["kind", "next-major"])
    describe.set_defaults(func=command_describe)

    args = parser.parse_args()
    return int(args.func(args))


if __name__ == "__main__":
    sys.exit(main())
