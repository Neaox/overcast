#!/usr/bin/env python3
"""Rich interactive prompt for `changelog.py new`.

Optional. changelog.py itself is stdlib-only because CI runs it on every push;
this module needs prompt_toolkit and is imported lazily, so the plain prompt
still works everywhere:

    pip install prompt_toolkit

What it adds over the plain loop: syntax highlighting as you type, completion
for area slugs drawn from the services that actually exist, per-line validation
before the line is accepted, and a status bar that reads back how the line
parsed — so the compatibility marker is visible rather than inferred.
"""

from __future__ import annotations

import re
from pathlib import Path

from prompt_toolkit import PromptSession
from prompt_toolkit.completion import Completer, Completion
from prompt_toolkit.formatted_text import HTML
from prompt_toolkit.lexers import Lexer
from prompt_toolkit.styles import Style
from prompt_toolkit.validation import ValidationError, Validator

import changelog


# Areas with no service directory behind them. Services are discovered, so this
# list only has to carry the cross-cutting names.
CONCEPT_AREAS = (
    "ci",
    "cli",
    "compat",
    "config",
    "docker",
    "docs",
    "networking",
    "release",
    "router",
    "routing",
    "state",
    "tls",
    "web",
)

KIND_PREFIX = re.compile(r"^([+\-~*]|[A-Za-z]+)([!.]?)")
AREA_BLOCK = re.compile(r"^(\s*)\[([^\]]*)\]")
CODE_SPAN = re.compile(r"(`[^`]*`)")

STYLE = Style.from_dict(
    {
        "kind.added": "ansigreen bold",
        "kind.removed": "ansired bold",
        "kind.changed": "ansiyellow bold",
        "kind.fixed": "ansicyan bold",
        "kind.deprecated": "ansimagenta bold",
        "kind.security": "ansimagenta bold",
        "marker.breaking": "ansired bold reverse",
        "marker.safe": "ansibrightblack",
        "bracket": "ansibrightblack",
        "area.known": "ansiblue",
        "area.unknown": "ansiyellow underline",
        "code": "ansimagenta",
        "migration": "ansigreen italic",
        "error": "ansired underline",
        "bottom-toolbar": "reverse",
    }
)


def area_vocabulary(root: Path | None = None) -> dict[str, str]:
    """Known area slugs mapped to where they came from, for completion meta."""
    root = root or Path(__file__).resolve().parent.parent
    areas: dict[str, str] = {}

    services = root / "internal" / "services"
    if services.is_dir():
        for path in sorted(services.iterdir()):
            if path.is_dir() and changelog.AREA_SLUG.match(path.name):
                areas[path.name] = "service"

    for slug in CONCEPT_AREAS:
        areas.setdefault(slug, "area")

    # Anything already used in a fragment is legitimate by definition, and
    # picking it up keeps the vocabulary honest without a list to maintain.
    fragments = root / ".changelog"
    if fragments.is_dir():
        for fragment in sorted(fragments.iterdir()):
            if fragment.name == "README.md" or fragment.is_dir():
                continue
            entries, _ = changelog.parse_entries(
                fragment.read_text(encoding="utf-8"), require_migration=False
            )
            for entry in entries:
                for slug in entry.areas:
                    areas.setdefault(slug, "in use")
    return areas


class EntryLexer(Lexer):
    def __init__(self, areas: dict[str, str]) -> None:
        self.areas = areas

    def lex_document(self, document):
        def get_line(lineno: int) -> list[tuple[str, str]]:
            return list(self.tokens(document.lines[lineno]))

        return get_line

    def tokens(self, text: str):
        if not text:
            return
        if text[0].isspace():
            stripped = text.lstrip()
            yield ("", text[: len(text) - len(stripped)])
            if stripped.lower().startswith(changelog.MIGRATION_PREFIX):
                cut = len(changelog.MIGRATION_PREFIX)
                yield ("class:migration", stripped[:cut])
                yield from self.prose(stripped[cut:])
            else:
                yield from self.prose(stripped)
            return

        match = KIND_PREFIX.match(text)
        if match is None:
            yield ("class:error", text)
            return
        kind, marker = match.group(1), match.group(2)
        section = changelog.ENTRY_SYMBOLS.get(kind) or changelog.SECTION_BY_WORD.get(
            kind.lower()
        )
        yield (f"class:kind.{section.lower()}" if section else "class:error", kind)
        if marker:
            yield (
                "class:marker.breaking" if marker == "!" else "class:marker.safe",
                marker,
            )

        rest = text[match.end() :]
        areas = AREA_BLOCK.match(rest)
        if areas is not None:
            yield ("", areas.group(1))
            yield ("class:bracket", "[")
            for index, part in enumerate(areas.group(2).split("/")):
                if index:
                    yield ("class:bracket", "/")
                known = part.strip() in self.areas
                yield ("class:area.known" if known else "class:area.unknown", part)
            yield ("class:bracket", "]")
            rest = rest[areas.end() :]
        yield from self.prose(rest)

    def prose(self, text: str):
        for chunk in CODE_SPAN.split(text):
            if chunk:
                yield ("class:code" if chunk.startswith("`") else "", chunk)


class EntryCompleter(Completer):
    def __init__(self, areas: dict[str, str]) -> None:
        self.areas = areas

    def get_completions(self, document, complete_event):
        text = document.text_before_cursor
        opened = text.rfind("[")
        if opened != -1 and "]" not in text[opened:]:
            typed = text[opened + 1 :].rsplit("/", 1)[-1].strip()
            for slug, origin in self.areas.items():
                if slug.startswith(typed):
                    yield Completion(
                        slug, start_position=-len(typed), display_meta=origin
                    )
            return
        if " " not in text:
            for word in sorted(changelog.SECTION_BY_WORD):
                if word.startswith(text.lower()) and text:
                    yield Completion(
                        word, start_position=-len(text), display_meta="section"
                    )


class EntryValidator(Validator):
    def validate(self, document) -> None:
        text = document.text
        # Blank finishes the prompt and indented lines are continuations, so
        # neither is a standalone entry to check.
        if not text.strip() or text[0].isspace():
            return
        _, errors = changelog.parse_entries(text, require_migration=False)
        if errors:
            message = errors[0].split(": ", 1)[-1]
            raise ValidationError(message=message, cursor_position=len(text))


def describe(text: str) -> str:
    """One-line read-back of how the current line parsed."""
    if not text.strip():
        return "blank line finishes | Ctrl+C aborts"
    if text[0].isspace():
        kind = (
            "migration note"
            if text.strip().lower().startswith(changelog.MIGRATION_PREFIX)
            else "continuation"
        )
        return f"{kind} for the entry above"
    entries, errors = changelog.parse_entries(text, require_migration=False)
    if errors or not entries:
        return "not a valid entry yet"
    entry = entries[0]
    areas = "/".join(entry.areas) or "no area"
    compat = "BREAKING" if entry.breaking else "compatible"
    return f"{entry.section} | {areas} | {compat}"


def rich_prompt_entries() -> str:
    areas = area_vocabulary()
    session: PromptSession = PromptSession(
        lexer=EntryLexer(areas),
        completer=EntryCompleter(areas),
        complete_while_typing=True,
        validator=EntryValidator(),
        validate_while_typing=True,
        style=STYLE,
        bottom_toolbar=lambda: HTML(
            "  <b>+</b> added  <b>-</b> removed  <b>~</b> changed  <b>*</b> fixed"
            "   <b>!</b> breaking  <b>.</b> not   |  {}"
        ).format(describe(session.default_buffer.text)),
    )

    print(changelog.PROMPT_BANNER)
    lines: list[str] = []
    while True:
        try:
            raw = session.prompt("> ")
        except (EOFError, KeyboardInterrupt):
            print()
            break
        if not raw.strip():
            if not lines:
                continue
            _, errors = changelog.parse_entries("\n".join(lines))
            if not errors:
                break
            for error in errors:
                print(f"  ! {error}")
            print("  (add a line to fix it, or Ctrl+C to abort)")
            continue
        lines.append(raw)
    return "\n".join(lines)
