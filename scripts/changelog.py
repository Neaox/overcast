#!/usr/bin/env python3
"""Changelog fragment tooling.

Between releases, CHANGELOG.md's [Unreleased] section stays empty. Every
release-note-worthy change is recorded as one fragment file per PR under
.changelog/, so concurrent PRs never edit the same lines and can never
merge-conflict over the changelog. At release time the fragments are curated
into the new versioned section and deleted (see RELEASE.md).

Subcommands:
    check      Lint the fragments and enforce the empty-[Unreleased] rule.
               CI runs this on every push and pull request.
    assemble   Print a draft '## [<version>]' section built from the
               fragments, grouped by category then area, as raw material
               for the release-prep curation pass.
"""

from __future__ import annotations

import argparse
import datetime
import re
import sys
from dataclasses import dataclass
from pathlib import Path


SECTIONS = ("Added", "Changed", "Fixed", "Removed", "Deprecated", "Security")
FRAGMENT_NAME = re.compile(r"^\d{8}-[a-z0-9][a-z0-9-]*\.md$")
AREA_SLUG = re.compile(r"^[a-z0-9][a-z0-9-]*$")
FRONTMATTER_KEYS = ("section", "area")


@dataclass
class Fragment:
    path: Path
    section: str
    area: str  # empty when the fragment declared none
    body: str


def parse_fragment(path: Path) -> tuple[Fragment | None, list[str]]:
    errors: list[str] = []
    if not FRAGMENT_NAME.match(path.name):
        errors.append(
            f"{path}: fragment file names must match YYYYMMDD-<slug>.md "
            "(lowercase letters, digits, hyphens)."
        )

    lines = path.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0].strip() != "---":
        errors.append(f"{path}: must start with a '---' frontmatter block.")
        return None, errors
    closer = None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            closer = i
            break
    if closer is None:
        errors.append(f"{path}: frontmatter block is not closed with '---'.")
        return None, errors

    meta: dict[str, str] = {}
    for raw in lines[1:closer]:
        if not raw.strip():
            continue
        key, sep, value = raw.partition(":")
        key = key.strip()
        if not sep or not key:
            errors.append(f"{path}: malformed frontmatter line {raw!r}.")
            continue
        # Allow trailing '# ...' comments after the value.
        value = value.split("#", 1)[0].strip()
        if key not in FRONTMATTER_KEYS:
            errors.append(
                f"{path}: unknown frontmatter key '{key}' "
                f"(allowed: {', '.join(FRONTMATTER_KEYS)})."
            )
            continue
        if key in meta:
            errors.append(f"{path}: duplicate frontmatter key '{key}'.")
            continue
        meta[key] = value

    section = meta.get("section", "")
    if section not in SECTIONS:
        errors.append(
            f"{path}: 'section' must be one of {', '.join(SECTIONS)}; got '{section}'."
        )
    area = meta.get("area", "")
    if area and not AREA_SLUG.match(area):
        errors.append(
            f"{path}: 'area' must be a lowercase slug (letters, digits, hyphens); "
            f"got '{area}'."
        )

    body = "\n".join(lines[closer + 1 :]).strip()
    if not body:
        errors.append(f"{path}: fragment body is empty.")
    else:
        if not body.startswith("- "):
            errors.append(
                f"{path}: fragment body must be one or more markdown list items "
                "starting with '- '."
            )
        if any(line.lstrip().startswith("#") for line in body.splitlines()):
            errors.append(f"{path}: fragment bodies must not contain headings.")

    if errors:
        return None, errors
    return Fragment(path=path, section=section, area=area, body=body), errors


def load_fragments(fragments_dir: Path) -> tuple[list[Fragment], list[str]]:
    if not fragments_dir.is_dir():
        return [], [f"{fragments_dir}: fragments directory is missing."]

    fragments: list[Fragment] = []
    errors: list[str] = []
    for path in sorted(fragments_dir.iterdir()):
        if path.name == "README.md":
            continue
        if path.is_dir():
            errors.append(f"{path}: unexpected directory in the fragments directory.")
            continue
        fragment, fragment_errors = parse_fragment(path)
        errors.extend(fragment_errors)
        if fragment is not None:
            fragments.append(fragment)
    return fragments, errors


def unreleased_errors(changelog_path: Path) -> list[str]:
    text = changelog_path.read_text(encoding="utf-8")
    text = re.sub(r"<!--.*?-->", "", text, flags=re.S)
    match = re.search(r"^## \[Unreleased\].*?$", text, flags=re.M)
    if match is None:
        return [f"{changelog_path}: missing a '## [Unreleased]' section."]
    rest = text[match.end() :]
    next_heading = re.search(r"^## ", rest, flags=re.M)
    body = rest[: next_heading.start()] if next_heading else rest
    for line in body.splitlines():
        stripped = line.strip()
        if not stripped or re.match(r"^###\s+", stripped):
            continue
        return [
            f"{changelog_path}: '[Unreleased]' must stay empty between releases. "
            "Record the change as a fragment file under .changelog/ instead "
            "(see .changelog/README.md)."
        ]
    return []


def assemble(fragments: list[Fragment], version: str, date: str) -> str:
    lines = [f"## [{version}] - {date}"]
    for section in SECTIONS:
        picked = [f for f in fragments if f.section == section]
        if not picked:
            continue
        # Fragments without an area sort last; same-area fragments end up
        # adjacent so the curation pass can merge them into one bullet.
        picked.sort(key=lambda f: (f.area or "~", f.path.name))
        lines.append("")
        lines.append(f"### {section}")
        for fragment in picked:
            lines.append("")
            lines.append(fragment.body)
    return "\n".join(lines) + "\n"


def use_utf8_stdio() -> None:
    """Write UTF-8 regardless of the ambient console encoding.

    Fragment bodies carry non-ASCII routinely ('—' in feature entries, '→' in
    perf entries), and Windows consoles default to cp1252, which cannot encode
    them. reconfigure() only exists on TextIOWrapper, so guard for it: under a
    pipe redirect or a test harness these may be another file-like object.
    """
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8")


def main() -> int:
    use_utf8_stdio()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fragments-dir", default=".changelog")
    parser.add_argument("--changelog", default="CHANGELOG.md")
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser(
        "check", help="lint fragments and enforce the empty-[Unreleased] rule"
    )
    assemble_parser = subparsers.add_parser(
        "assemble", help="print a draft release section built from the fragments"
    )
    assemble_parser.add_argument("version")
    assemble_parser.add_argument(
        "--date", default=None, help="release date (UTC, YYYY-MM-DD; default today)"
    )
    args = parser.parse_args()

    fragments, errors = load_fragments(Path(args.fragments_dir))

    if args.command == "check":
        errors.extend(unreleased_errors(Path(args.changelog)))
        for error in errors:
            print(f"::error::{error}")
        return 1 if errors else 0

    if errors:
        for error in errors:
            print(f"::error::{error}", file=sys.stderr)
        return 1
    if not fragments:
        print(f"::error::{args.fragments_dir}: no fragments to assemble.", file=sys.stderr)
        return 1
    date = args.date or datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d")
    sys.stdout.write(assemble(fragments, args.version, date))
    return 0


if __name__ == "__main__":
    sys.exit(main())
