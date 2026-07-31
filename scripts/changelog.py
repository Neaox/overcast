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


def parse_entries(
    text: str, origin: str = "", require_migration: bool = True
) -> tuple[list[Entry], list[str]]:
    """Parse entry lines. `origin` prefixes errors with the source file.

    `require_migration` is off when validating a single line as it is typed:
    a breaking entry's migration note arrives on the *next* line, so demanding
    it per-line would reject input that is about to become correct.
    """
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
    if require_migration:
        for entry in entries:
            if entry.breaking and not entry.migration:
                errors.append(
                    f"{where}line {entry.line}: a breaking entry needs an indented "
                    "'migration:' line saying what a user has to do about it."
                )

    return entries, errors


PROMPT_BANNER = """\
One entry per line, blank line to finish.

  +  Added      -  Removed     ~  Changed     *  Fixed
  !  breaking   .  not breaking (only needed when the default is wrong)
  indent to continue the entry above, or to add a 'migration:' note

  e.g.  + [sqs] long polling on `ReceiveMessage`
"""


def prompt_entries() -> str:
    """Collect entry lines interactively, validating each as it is typed.

    Upgrades itself to the prompt_toolkit prompt — highlighting, area
    completion, a status bar — when that module is importable. This file stays
    stdlib-only because CI runs it on every push; the rich layer is a local
    convenience and lives in changelog_prompt.py.
    """
    try:
        from changelog_prompt import rich_prompt_entries
    except ImportError:
        print("(pip install prompt_toolkit for highlighting and completion)\n")
    else:
        return rich_prompt_entries()

    print(PROMPT_BANNER)
    lines: list[str] = []
    while True:
        try:
            raw = input("> ")
        except EOFError:
            print()
            break

        if not raw.strip():
            if not lines:
                continue
            # Whole-buffer check: this is where a breaking entry missing its
            # migration note surfaces, and another line can still fix it.
            _, errors = parse_entries("\n".join(lines))
            if not errors:
                break
            for error in errors:
                print(f"  ! {error}")
            print("  (add a line to fix it, or Ctrl+C to abort)")
            continue

        if not raw[0].isspace():
            _, errors = parse_entries(raw, require_migration=False)
            if errors:
                for error in errors:
                    print(f"  ! {error}")
                continue
        lines.append(raw)

    return "\n".join(lines)


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


REPO_URL = "https://github.com/Neaox/overcast"
PRERELEASE = re.compile(
    r"^(?P<base>\d+\.\d+\.\d+)-(?P<channel>[0-9A-Za-z-]+)\.(?P<counter>\d+)$"
)


def next_version(current: str) -> str:
    """The version after `current`.

    While in alpha this is arithmetic, not a judgement: the prerelease counter
    increments and the base version stays put whatever the release contains.
    Deriving a stable bump from the entries is deliberately NOT implemented —
    the mapping from breaking/added to major/minor is an open policy decision
    that takes effect at 1.0 (see RELEASE.md, Version Format). An honest gap
    beats untested code pretending to make a decision nobody is making yet.
    """
    match = PRERELEASE.match(current.strip())
    if match is None:
        raise ValueError(
            f"cannot derive the next version from '{current.strip()}': it is not "
            "a prerelease, and the stable bump rules are unimplemented until "
            "1.0. Pass the version explicitly."
        )
    counter = int(match.group("counter")) + 1
    return f"{match.group('base')}-{match.group('channel')}.{counter}"


def release_versions(changelog: str) -> list[str]:
    """Released versions, newest first."""
    return [
        match.group(1)
        for match in re.finditer(r"^## \[([^\]]+)\]", changelog, flags=re.M)
        if match.group(1) != "Unreleased"
    ]


def insert_release_section(changelog: str, section: str) -> str:
    match = re.search(r"^## \[Unreleased\].*?$", changelog, flags=re.M)
    if match is None:
        raise ValueError("CHANGELOG.md has no '## [Unreleased]' section.")
    return f"{changelog[: match.end()]}\n\n{section.strip()}{changelog[match.end() :]}"


def update_links(changelog: str, version: str, previous: str | None) -> str:
    """Repoint [Unreleased] at the new tag and add the new version's link."""
    pattern = re.compile(r"^\[Unreleased\]:.*$", flags=re.M)
    if pattern.search(changelog) is None:
        raise ValueError("CHANGELOG.md has no '[Unreleased]' link reference.")
    unreleased = f"[Unreleased]: {REPO_URL}/compare/v{version}...HEAD"
    if previous:
        added = f"[{version}]: {REPO_URL}/compare/v{previous}...v{version}"
    else:
        added = f"[{version}]: {REPO_URL}/releases/tag/v{version}"
    # A lambda replacement, so a URL is never read as a backreference.
    return pattern.sub(lambda _: f"{unreleased}\n{added}", changelog, count=1)


def release_summary(fragments: list[Fragment], version: str) -> str:
    """Markdown summary of what a release contains, breaking changes first."""
    entries = [entry for fragment in fragments for entry in fragment.entries]
    counts = {
        section: sum(1 for entry in entries if entry.section == section)
        for section in SECTIONS
    }
    tally = ", ".join(f"{count} {name}" for name, count in counts.items() if count)
    lines = [f"**{version}** — {len(entries)} entries ({tally})", ""]

    breaking = [entry for entry in entries if entry.breaking]
    if not breaking:
        lines.append("No breaking changes.")
        return "\n".join(lines) + "\n"

    # Listed first and in full: the version cannot signal a break while in
    # alpha, so this comment is where a reader finds out.
    lines.append(f"### Breaking changes ({len(breaking)})")
    lines.append("")
    for entry in breaking:
        areas = f"[{'/'.join(entry.areas)}] " if entry.areas else ""
        lines.append(f"- {areas}{entry.prose}")
        if entry.migration:
            lines.append(f"  - **migration:** {entry.migration}")
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


def command_new(args: argparse.Namespace) -> int:
    if args.file:
        text = (
            sys.stdin.read()
            if args.file == "-"
            else Path(args.file).read_text(encoding="utf-8")
        )
    elif args.entry:
        text = "\n".join(args.entry)
    elif sys.stdin.isatty():
        # No entries and a terminal to ask on: prompt. Piped input still works
        # without -f, so `... new < entries.txt` behaves as expected too.
        text = prompt_entries()
    else:
        text = sys.stdin.read()
    if not text.strip():
        print("::error::no entry lines given.", file=sys.stderr)
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


def command_release(args: argparse.Namespace) -> int:
    """Perform the mechanical half of release prep.

    Everything here is what `check-release-changelog.py` fails a release for
    and what a human otherwise hand-writes: the section, both compare links,
    VERSION, and the fragment deletions. Curation stays a human job — this
    produces the draft, not the finished notes.
    """
    changelog_path = Path(args.changelog)
    version_path = Path(args.version_file)

    fragments, errors = load_fragments(Path(args.fragments_dir))
    if errors:
        for error in errors:
            print(f"::error::{error}", file=sys.stderr)
        return 1
    if not fragments:
        print(
            f"::error::{args.fragments_dir}: no fragments to release.", file=sys.stderr
        )
        return 1

    version = args.version
    if version is None:
        try:
            version = next_version(version_path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            print(f"::error::{exc}", file=sys.stderr)
            return 1

    date = args.date or datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d")
    changelog = changelog_path.read_text(encoding="utf-8")
    versions = release_versions(changelog)
    if version in versions:
        print(
            f"::error::CHANGELOG.md already has a '## [{version}]' section.",
            file=sys.stderr,
        )
        return 1

    try:
        updated = insert_release_section(
            changelog, assemble(fragments, version, date)
        )
        updated = update_links(updated, version, versions[0] if versions else None)
    except ValueError as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1

    summary = release_summary(fragments, version)
    if args.summary_file:
        Path(args.summary_file).write_text(summary, encoding="utf-8", newline="\n")

    if args.dry_run:
        sys.stdout.write(summary)
        print(f"\n--- would write {version_path} and {changelog_path}, and delete:")
        for fragment in fragments:
            print(f"    {fragment.path}")
        return 0

    changelog_path.write_text(updated, encoding="utf-8", newline="\n")
    version_path.write_text(f"{version}\n", encoding="utf-8", newline="\n")
    for fragment in fragments:
        fragment.path.unlink()

    print(f"{version_path}: {version}")
    print(f"{changelog_path}: added '## [{version}] - {date}'")
    print(f"{args.fragments_dir}: removed {len(fragments)} fragments")
    return 0


def command_next_version(args: argparse.Namespace) -> int:
    try:
        print(next_version(Path(args.version_file).read_text(encoding="utf-8")))
    except (OSError, ValueError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1
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
    release_parser = subparsers.add_parser(
        "release", help="apply the mechanical release-prep edit"
    )
    release_parser.add_argument(
        "version", nargs="?", default=None, help="default: derived from VERSION"
    )
    release_parser.add_argument("--version-file", default="VERSION")
    release_parser.add_argument(
        "--date", default=None, help="release date (UTC, YYYY-MM-DD; default today)"
    )
    release_parser.add_argument(
        "--summary-file", default=None, help="write the markdown summary here"
    )
    release_parser.add_argument(
        "--dry-run", action="store_true", help="print what would be written"
    )
    version_parser = subparsers.add_parser(
        "next-version", help="print the version after the current one"
    )
    version_parser.add_argument("--version-file", default="VERSION")
    args = parser.parse_args()

    # `new` writes fragments rather than reading them, and must work while an
    # unrelated fragment on disk is mid-edit and failing the linter.
    if args.command == "new":
        return command_new(args)
    if args.command == "next-version":
        return command_next_version(args)
    if args.command == "release":
        return command_release(args)

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
