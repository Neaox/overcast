#!/usr/bin/env python3
"""Changelog fragment tooling.

Between releases, CHANGELOG.md's [Unreleased] section stays empty. Every
release-note-worthy change is recorded as an entry line in a fragment file
under .changelog/ — one file per PR, at a unique path, so concurrent PRs can
never merge-conflict over the changelog. Every line carries its own category,
scope and compatibility marker, so a file is a container rather than a
grouping: the grouping happens once, at release time, when the entries are
curated into the new versioned section and deleted (see RELEASE.md).

Entry grammar, one entry per line:

    <+|-|~|*|section>[!|.] [area[/area...]] <prose>

'+' Added, '-' Removed, '~' Changed, '*' Fixed; any section may be spelled out,
which is how Deprecated and Security are written. '!' marks the change
breaking, '.' marks it explicitly not breaking — needed only where the default
does not apply (see BREAKING_DEFAULT). An indented line continues the entry
above it; an indented 'migration:' line carries the upgrade instructions every
breaking entry must include.

Subcommands:
    check      Lint the fragments and enforce the empty-[Unreleased] rule.
               CI runs this on every push and pull request.
    assemble   Print a draft '## [<version>]' section built from the
               fragments, grouped by category then area, as raw material
               for the release-prep curation pass.
    new        Append entry lines to this PR's fragment file:
                   changelog.py new -e '+! [sqs/lambda] text'
"""

from __future__ import annotations

import argparse
import datetime
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path


SECTIONS = ("Added", "Changed", "Fixed", "Removed", "Deprecated", "Security")
SECTION_BY_WORD = {section.lower(): section for section in SECTIONS}
FRAGMENT_NAME = re.compile(r"^\d{8}-[a-z0-9][a-z0-9-]*\.md$")
AREA_SLUG = re.compile(r"^[a-z0-9][a-z0-9-]*$")

ENTRY_SYMBOLS = {"+": "Added", "-": "Removed", "~": "Changed", "*": "Fixed"}
SYMBOL_BY_SECTION = {section: symbol for symbol, section in ENTRY_SYMBOLS.items()}
ENTRY_LINE = re.compile(
    r"^(?P<kind>[+\-~*]|[A-Za-z]+)(?P<marker>[!.]?)"
    r"(?:\s*\[(?P<areas>[^\]]*)\])?"
    r"\s+(?P<prose>\S.*)$"
)
MIGRATION_PREFIX = "migration:"
SLUG_MAX = 48

# Whether an unmarked entry in each category breaks compatibility.
#
# Almost nothing is breaking, so almost nothing should have to say so: a marker
# demanded on a common category becomes a reflex, and a reflex is
# indistinguishable from forgetting. The friction is therefore spent in one
# place only — Removed inverts, because dropping something is breaking unless
# the author says otherwise, and removals are rare enough that the cost lands
# on roughly the entries that deserve the thought. Everything else defaults to
# compatible and relies on BREAKING_HINTS to force the question when the prose
# gives the change away.
BREAKING_DEFAULT = {
    "Added": False,
    "Changed": False,
    "Fixed": False,
    "Deprecated": False,
    "Security": False,
    "Removed": True,
}

# Phrases that read like a compatibility break whatever the category. A hit
# forces an explicit marker either way — it does not assume breakage, it just
# refuses to guess. This is what catches the breaking change nobody would have
# marked: validation that now rejects input it used to accept, or a default
# that quietly moved, filed under Fixed or Changed like any other improvement.
# Calibrated against the fragments in flight: a bare "no longer" matched ten
# entries, nearly all of them "no longer <misbehaves>" — the house idiom for a
# bug fix or a perf win. The phrases kept are the ones whose object is an input
# or output contract rather than a behaviour.
BREAKING_HINTS = (
    "now requires",
    "is now required",
    "now rejects",
    "no longer accepts",
    "no longer returns",
    "no longer supported",
    "no longer supports",
    "no longer available",
    "must now",
    "renamed",
    "default is now",
    "now defaults to",
    "removed support",
    "breaking",
)


@dataclass
class Entry:
    section: str
    breaking: bool
    # Areas the change touches, primary first; empty when none was declared.
    # Only the primary is load-bearing — it is the assembly sort key. The rest
    # record the scope of a cross-cutting change, which stays one entry.
    areas: list[str]
    prose: str
    migration: str = ""
    line: int = 0
    source: str = ""  # fragment filename, for stable assembly ordering

    @property
    def area(self) -> str:
        return self.areas[0] if self.areas else ""


@dataclass
class Fragment:
    path: Path
    entries: list[Entry] = field(default_factory=list)


def parse_areas(raw: str, separator: str) -> tuple[list[str], list[str]]:
    """Split and validate an area list, preserving order."""
    areas: list[str] = []
    problems: list[str] = []
    for part in raw.split(separator):
        slug = part.strip()
        if not slug:
            problems.append(f"has an empty entry; separate slugs with '{separator}'")
            continue
        if not AREA_SLUG.match(slug):
            problems.append(
                f"must be lowercase slugs (letters, digits, hyphens); got '{slug}'"
            )
            continue
        if slug in areas:
            problems.append(f"repeats '{slug}'")
            continue
        areas.append(slug)
    return areas, problems


def breaking_hint(prose: str) -> str:
    lowered = prose.lower()
    return next((hint for hint in BREAKING_HINTS if hint in lowered), "")


def parse_entries(text: str, origin: str = "") -> tuple[list[Entry], list[str]]:
    """Parse entry lines. `origin` prefixes errors with the source file."""
    where = f"{origin}: " if origin else ""
    entries: list[Entry] = []
    errors: list[str] = []
    continuing_migration = False

    for number, raw in enumerate(text.splitlines(), start=1):
        if not raw.strip() or raw.startswith("#"):
            continue

        if raw[0].isspace():
            if not entries:
                errors.append(
                    f"{where}line {number}: continuation line has no entry above it."
                )
                continue
            stripped = raw.strip()
            if stripped.lower().startswith(MIGRATION_PREFIX):
                note = stripped[len(MIGRATION_PREFIX) :].strip()
                if not note:
                    errors.append(
                        f"{where}line {number}: 'migration:' needs the upgrade "
                        "instructions after it."
                    )
                    continue
                entries[-1].migration = f"{entries[-1].migration} {note}".strip()
                continuing_migration = True
            elif continuing_migration:
                entries[-1].migration = f"{entries[-1].migration} {stripped}"
            else:
                entries[-1].prose = f"{entries[-1].prose} {stripped}"
            continue

        continuing_migration = False
        match = ENTRY_LINE.match(raw)
        if match is None:
            errors.append(
                f"{where}line {number}: expected "
                f"'<+|-|~|*|section>[!|.] [area] text'; got {raw!r}."
            )
            continue

        kind = match.group("kind")
        section = ENTRY_SYMBOLS.get(kind) or SECTION_BY_WORD.get(kind.lower(), "")
        if not section:
            errors.append(
                f"{where}line {number}: unknown kind '{kind}'; use one of "
                f"{', '.join(ENTRY_SYMBOLS)} or a section name."
            )
            continue

        prose = match.group("prose").strip()
        marker = match.group("marker")
        default = BREAKING_DEFAULT[section]
        if marker == "!":
            breaking = True
        elif marker == ".":
            breaking = False
        else:
            hint = breaking_hint(prose)
            if hint:
                errors.append(
                    f"{where}line {number}: {hint!r} reads like a compatibility "
                    "break; write '!' (breaking) or '.' (not) after the kind to "
                    "say which."
                )
                continue
            breaking = default

        areas: list[str] = []
        raw_areas = match.group("areas")
        if raw_areas is not None:
            areas, problems = parse_areas(raw_areas, "/")
            errors.extend(
                f"{where}line {number}: area list {problem}." for problem in problems
            )

        entries.append(
            Entry(
                section=section,
                breaking=breaking,
                areas=areas,
                prose=prose,
                line=number,
            )
        )

    # Marking a change breaking has to cost something, or '!' becomes noise:
    # the release notes need the upgrade instructions anyway, and requiring
    # them here keeps the marker a deliberate act rather than a checkbox.
    for entry in entries:
        if entry.breaking and not entry.migration:
            errors.append(
                f"{where}line {entry.line}: a breaking entry needs an indented "
                "'migration:' line saying what a user has to do about it."
            )

    return entries, errors


def parse_fragment(path: Path) -> tuple[Fragment | None, list[str]]:
    errors: list[str] = []
    if not FRAGMENT_NAME.match(path.name):
        errors.append(
            f"{path}: fragment file names must match YYYYMMDD-<slug>.md "
            "(lowercase letters, digits, hyphens)."
        )

    text = path.read_text(encoding="utf-8")
    if text.lstrip().startswith("---"):
        errors.append(
            f"{path}: fragments no longer use frontmatter — every entry line "
            "carries its own category and scope. See .changelog/README.md."
        )
        return None, errors

    entries, entry_errors = parse_entries(text, origin=str(path))
    errors.extend(entry_errors)
    if not entries and not entry_errors:
        errors.append(f"{path}: fragment has no entries.")
    if errors:
        return None, errors

    for entry in entries:
        entry.source = path.name
    return Fragment(path=path, entries=entries), errors


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
    entries = [entry for fragment in fragments for entry in fragment.entries]
    lines = [f"## [{version}] - {date}"]
    for section in SECTIONS:
        picked = [entry for entry in entries if entry.section == section]
        if not picked:
            continue
        # Entries without an area sort last; same-area entries end up adjacent
        # so the curation pass can merge them into one bullet. Sorting entries
        # rather than files groups two entries about one service even when they
        # arrived in different PRs.
        picked.sort(key=lambda entry: (entry.area or "~", entry.source, entry.line))
        lines.append("")
        lines.append(f"### {section}")
        for entry in picked:
            prefix = f"[{'/'.join(entry.areas)}] " if entry.areas else ""
            flag = "**BREAKING** " if entry.breaking else ""
            lines.append("")
            lines.append(f"- {flag}{prefix}{entry.prose}")
            if entry.migration:
                lines.append(f"  migration: {entry.migration}")
    return "\n".join(lines) + "\n"


def render_entry(entry: Entry) -> str:
    """Render an entry back to its canonical line form."""
    symbol = SYMBOL_BY_SECTION.get(entry.section, entry.section.lower())
    marker = ""
    if entry.breaking != BREAKING_DEFAULT[entry.section] or breaking_hint(entry.prose):
        marker = "!" if entry.breaking else "."
    areas = f" [{'/'.join(entry.areas)}]" if entry.areas else ""
    rendered = f"{symbol}{marker}{areas} {entry.prose}\n"
    if entry.migration:
        rendered += f"  {MIGRATION_PREFIX} {entry.migration}\n"
    return rendered


def slugify(text: str, limit: int = SLUG_MAX) -> str:
    slug = re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")
    if len(slug) <= limit:
        return slug
    return (slug[:limit].rsplit("-", 1)[0] or slug[:limit]).strip("-")


def branch_slug() -> str:
    """Slugified current git branch, or '' when git cannot answer."""
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
    except (OSError, subprocess.SubprocessError):
        return ""
    name = result.stdout.strip()
    if not name or name == "HEAD":
        return ""
    return slugify(name.rsplit("/", 1)[-1])


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


def command_new(args: argparse.Namespace) -> int:
    if args.file:
        text = (
            sys.stdin.read()
            if args.file == "-"
            else Path(args.file).read_text(encoding="utf-8")
        )
    else:
        text = "\n".join(args.entry or ())
    if not text.strip():
        print("::error::no entry lines given; use --entry or --file.", file=sys.stderr)
        return 1

    date = args.date or datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d")
    if not re.fullmatch(r"\d{8}", date):
        print(f"::error::--date must be YYYYMMDD; got '{date}'.", file=sys.stderr)
        return 1

    entries, errors = parse_entries(text)
    if errors:
        for error in errors:
            print(f"::error::{error}", file=sys.stderr)
        return 1

    slug = args.name or branch_slug() or slugify(entries[0].prose) or "change"
    if not AREA_SLUG.match(slug):
        print(
            f"::error::--name must be a lowercase slug (letters, digits, "
            f"hyphens); got '{slug}'.",
            file=sys.stderr,
        )
        return 1

    directory = Path(args.fragments_dir)
    path = directory / f"{date}-{slug}.md"
    body = "".join(render_entry(entry) for entry in entries)

    if args.dry_run:
        verb = "append to" if path.exists() else "create"
        print(f"--- would {verb} {path}")
        sys.stdout.write(body)
        return 0

    # Append rather than replace: one file per PR means later commits on the
    # same branch land their entries alongside the earlier ones. newline='\n'
    # is not incidental — .gitattributes normalises the repository to LF, and
    # the default text mode would write CRLF for every Windows contributor.
    directory.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8", newline="\n") as handle:
        handle.write(body)
    print(path)
    return 0


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
    new_parser = subparsers.add_parser(
        "new", help="append entry lines to this PR's fragment file"
    )
    new_parser.add_argument(
        "-e",
        "--entry",
        action="append",
        default=None,
        help="one entry line, e.g. '+! [sqs/lambda] text'; repeatable",
    )
    new_parser.add_argument(
        "-f", "--file", default=None, help="read entry lines from a file ('-' for stdin)"
    )
    new_parser.add_argument(
        "--name", default=None, help="fragment slug (default: the git branch)"
    )
    new_parser.add_argument(
        "--date", default=None, help="fragment date (UTC, YYYYMMDD; default today)"
    )
    new_parser.add_argument(
        "--dry-run", action="store_true", help="print what would be written"
    )
    args = parser.parse_args()

    # `new` writes fragments rather than reading them, and must work while an
    # unrelated fragment on disk is mid-edit and failing the linter.
    if args.command == "new":
        return command_new(args)

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
