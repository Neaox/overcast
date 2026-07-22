#!/usr/bin/env python3
"""Validate CHANGELOG.md for a release version."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


REPO_URL = "https://github.com/Neaox/overcast"


def strip_comments(text: str) -> str:
    return re.sub(r"<!--.*?-->", "", text, flags=re.S).strip()


def normalize_section(text: str) -> str:
    lines = [line.rstrip() for line in text.splitlines()]
    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    return "\n".join(lines).strip()


def section(changelog: str, name: str) -> str | None:
    pattern = re.compile(rf"^## \[{re.escape(name)}\].*?$", re.M)
    match = pattern.search(changelog)
    if not match:
        return None
    rest = changelog[match.end() :]
    next_match = re.search(r"^## ", rest, flags=re.M)
    body = rest[: next_match.start()] if next_match else rest
    return normalize_section(strip_comments(body))


def section_is_empty(text: str) -> bool:
    for line in normalize_section(text).splitlines():
        stripped = line.strip()
        if not stripped or re.match(r"^###\s+", stripped):
            continue
        return False
    return True


def release_versions(changelog: str) -> list[str]:
    versions: list[str] = []
    for match in re.finditer(r"^## \[([^\]]+)\]", changelog, flags=re.M):
        version = match.group(1)
        if version != "Unreleased":
            versions.append(version)
    return versions


def link_references(changelog: str) -> dict[str, str]:
    refs: dict[str, str] = {}
    for match in re.finditer(r"^\[([^\]]+)\]:\s+(\S+)\s*$", changelog, flags=re.M):
        refs[match.group(1)] = match.group(2)
    return refs


def validate_compare_links(changelog: str, version: str) -> list[str]:
    errors: list[str] = []
    changelog = strip_comments(changelog)
    versions = release_versions(changelog)
    refs = link_references(changelog)
    if not versions:
        return errors

    if versions[0] != version:
        errors.append(
            "CHANGELOG.md latest release section must match VERSION "
            f"({version}); found {versions[0]}."
        )

    expected_unreleased = f"{REPO_URL}/compare/v{version}...HEAD"
    if refs.get("Unreleased") != expected_unreleased:
        errors.append(f"CHANGELOG.md link [Unreleased] must be {expected_unreleased}.")

    for i, current in enumerate(versions):
        if i+1 < len(versions):
            previous = versions[i+1]
            expected = f"{REPO_URL}/compare/v{previous}...v{current}"
        else:
            expected = f"{REPO_URL}/releases/tag/v{current}"

        actual = refs.get(current)
        if actual is None:
            errors.append(f"CHANGELOG.md is missing link reference [{current}].")
        elif actual != expected:
            errors.append(f"CHANGELOG.md link [{current}] must be {expected}.")

    release_set = set(versions)
    for name in sorted(refs):
        if name != "Unreleased" and name not in release_set:
            errors.append(f"CHANGELOG.md has link reference [{name}] without a release section.")

    return errors


def validate(changelog_path: Path, version: str) -> list[str]:
    changelog = changelog_path.read_text(encoding="utf-8")
    errors: list[str] = []

    release_notes = section(changelog, version)
    if release_notes is None:
        errors.append(f"CHANGELOG.md is missing a '## [{version}]' release section.")
    elif section_is_empty(release_notes):
        errors.append(f"CHANGELOG.md release section '## [{version}]' is empty.")

    unreleased = section(changelog, "Unreleased")
    if unreleased is None:
        errors.append("CHANGELOG.md is missing a '## [Unreleased]' section.")
    elif not section_is_empty(unreleased):
        errors.append(
            "CHANGELOG.md still has unreleased entries. Move them under "
            f"'## [{version}]' before releasing."
        )

    errors.extend(validate_compare_links(changelog, version))

    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("version")
    parser.add_argument("--changelog", default="CHANGELOG.md")
    args = parser.parse_args()

    errors = validate(Path(args.changelog), args.version)
    if errors:
        for error in errors:
            print(f"::error::{error}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
