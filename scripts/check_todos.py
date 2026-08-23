#!/usr/bin/env python3
"""Guard comments against markers the TODO-to-issue Action would misread.

.github/workflows/todo-to-issue.yml opens a GitHub issue for every marker
added to main. The action matches its identifier *anywhere* in a comment and
takes the rest of the line as the issue title, accepting a bare space where
the colon should be -- so a comment that merely mentions a marker in passing
files an issue. Issue #1138 was titled "on apigateway.Method) -- intentionally
not": the middle of a sentence explaining why some CloudFormation properties
are deliberately not forwarded, cut at a line break, with the next line of the
same sentence as its body. A lowercase "todo" in ordinary prose does it too --
"the todo list is long" would file "list is long".

No input to the action anchors the match, and upstream's regex is unchanged as
of v5.1.15, so the anchoring happens here instead. In this repo a marker may
only ever open a comment, in the canonical form CONTRIBUTING.md documents:

    <comment marker> TODO(priority:P2): what needs doing

and that description has to read as a title, because that is what it becomes.
Everything after the first line reaches the issue as its body, so detail goes
there; see MAX_TITLE_LENGTH below for what a run-on first line costs.

There is deliberately no allow-pragma. Suppressing a finding here would not
stop the action -- it would only hide the issue it is about to file.

Two inputs are read rather than restated, so this gate cannot drift from the
action it protects: .github/todo-languages.json decides which files the action
reads and how comments open in them, and the workflow's IGNORE decides which
paths it skips.

Usage:
  python3 scripts/check_todos.py          # scan the working tree, exit 1 on findings
  python3 scripts/check_todos.py FILE...  # scan just the named files
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
LANGUAGES_FILE = REPO_ROOT / ".github" / "todo-languages.json"
WORKFLOW_FILE = REPO_ROOT / ".github" / "workflows" / "todo-to-issue.yml"

# Copied verbatim from the action's TodoParser._get_title (v5.1.15) with its
# identifier substituted in. Detection has to be the action's own rule, not an
# approximation of it: what this gate must catch is exactly what the action
# would act on. The bare identifier is the superset: every line the workflow's
# priority identifiers match, this one matches too.
TITLE_PATTERN = re.compile(
    r"(^|(^.*?)(\s*?)\s)(TODO)(\(([^)]+)\))?\s*(:|\s)\s*(.+)", re.IGNORECASE
)

# The one form allowed to survive that match: the marker opening the comment,
# a priority, a colon, and a description for the issue title. Any digit is
# accepted so an unmapped priority reads as an unlabelled issue rather than as
# a gate failure; the workflow maps P0 to P3 onto their labels.
CANONICAL_PATTERN = re.compile(r"TODO\(priority:P[0-9]\): *\S")

# How long that description may be. The action takes the marker's first line
# and nothing else as the issue title, so a description that runs on becomes a
# title that runs on: #1087 got 350 characters, #1126 got 285. Past 256 the
# action's own guard misfires — `title + '...' if len(title) > 256 else title`
# parses as append-rather-than-truncate, so it lengthens the title it meant to
# cut — and a cap here keeps that branch out of reach. 120 is well inside it
# and is what a title can be read at a glance; the longest marker in the repo
# when this landed was 163, and detail below the first line still reaches the
# issue as its body.
MAX_TITLE_LENGTH = 120


def tracked_files() -> list[Path]:
    """Tracked files, plus untracked ones git would let you commit.

    A new file is the likeliest place for a mis-shaped marker, and it is
    untracked right up until the commit that would set the action loose on it.
    """
    out = subprocess.run(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"],
        check=True,
        capture_output=True,
        cwd=REPO_ROOT,
    ).stdout.decode("utf-8")
    return [REPO_ROOT / p for p in out.split("\0") if p]


def load_languages(path: Path = LANGUAGES_FILE) -> dict[str, list[dict]]:
    """Map file extension -> comment markers, as the action loads them."""
    markers_by_extension: dict[str, list[dict]] = {}
    for language in json.loads(path.read_text(encoding="utf-8")):
        for extension in language["extensions"]:
            markers_by_extension[extension] = language["markers"]
    return markers_by_extension


def load_ignores(path: Path = WORKFLOW_FILE) -> list[re.Pattern[str]]:
    """Read the workflow's IGNORE input: paths the action never opens."""
    match = re.search(r"^\s*IGNORE:\s*(.+?)\s*$", path.read_text(encoding="utf-8"), re.MULTILINE)
    if not match:
        return []
    value = match.group(1).strip("\"'")
    return [re.compile(p.strip()) for p in value.split(",") if p.strip()]


def comment_text(line: str, marker: dict, in_block: bool) -> str | None:
    """Return the comment on this line, cleaned as TodoParser._clean_line does.

    That includes its quirks, because the quirks are what the action acts on:
    the first line marker wins even when it sits inside a string literal, and
    a leading asterisk is stripped from block-comment continuation lines.
    """
    if marker["type"] == "line":
        segments = re.search(rf'^(.*?)({marker["pattern"]})(\s*)(.*?)\s*$', line)
        return segments.group(4) if segments else None

    start, end = marker["pattern"]["start"], marker["pattern"]["end"]
    if not in_block and not re.match(rf"\s*{start}", line):
        return None
    text = line.strip()
    text = re.sub(rf"^{start}", "", text)
    text = re.sub(rf"{end}$", "", text)
    if "*" in start and text.startswith("*"):
        text = text.lstrip("*")
    return text.strip()


def block_state(line: str, marker: dict, in_block: bool) -> bool:
    """Track whether a block comment is still open after this line.

    A block opens only where the action's own pattern lets one open: at the
    start of a line, after whitespace. Anywhere else and a route literal like
    "/v2/tags/*" would open a block that never closes, turning the rest of the
    file into comment.
    """
    start, end = marker["pattern"]["start"], marker["pattern"]["end"]
    if in_block:
        return not re.search(end, line)
    opened = re.match(rf"\s*{start}", line)
    return bool(opened) and not re.search(end, line[opened.end():])


def scan_text(text: str, markers: list[dict]) -> list[tuple[int, str, str]]:
    """Return (1-based line, reason, issue title) findings for one file."""
    findings: list[tuple[int, str, str]] = []
    open_blocks = [False] * len(markers)
    for number, line in enumerate(text.splitlines(), 1):
        for index, marker in enumerate(markers):
            in_block = open_blocks[index]
            comment = comment_text(line, marker, in_block)
            if marker["type"] == "block":
                open_blocks[index] = block_state(line, marker, in_block)
            if not comment:
                continue
            match = TITLE_PATTERN.search(comment)
            if not match:
                continue
            title = match.group(8)
            if not CANONICAL_PATTERN.match(comment):
                findings.append((number, "would file an issue titled", title))
            elif len(title) > MAX_TITLE_LENGTH:
                findings.append(
                    (
                        number,
                        f"title runs to {len(title)} characters "
                        f"(max {MAX_TITLE_LENGTH}) — move the detail to the next line",
                        title,
                    )
                )
            else:
                continue
            break
    return findings


def main() -> int:
    # The findings quote source comments, which in this repo contain em dashes;
    # a legacy Windows codepage cannot encode them.
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("files", nargs="*", help="scan only these files")
    args = parser.parse_args()

    markers_by_extension = load_languages()
    ignores = load_ignores()
    cwd = Path.cwd()
    paths = [(cwd / f).resolve() for f in args.files] if args.files else tracked_files()

    total = 0
    for full in paths:
        markers = markers_by_extension.get(full.suffix.lower())
        try:
            relative = full.relative_to(REPO_ROOT).as_posix()
        except ValueError:
            relative = full.as_posix()
        if not markers or any(i.match(relative) for i in ignores):
            continue
        if not full.is_file():
            continue
        for number, reason, title in scan_text(full.read_text(encoding="utf-8"), markers):
            print(f'{relative}:{number}: {reason} "{title}"')
            total += 1

    if total:
        print(
            f"\n{total} comment(s) the TODO-to-issue Action would file a bad issue for. "
            "A marker must open the comment as TODO(priority:PN): description, with a "
            "description short enough to read as a title; reword any other mention so "
            "it does not read as a marker at all.",
            file=sys.stderr,
        )
    return 1 if total else 0


if __name__ == "__main__":
    sys.exit(main())
