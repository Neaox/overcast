#!/usr/bin/env python3
"""Validate CHANGELOG.md for a release version."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


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
