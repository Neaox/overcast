#!/usr/bin/env python3
"""Guard tracked text files against encoding damage.

Three defects, one family: a file that was read as CP1252 and written back as
UTF-8 somewhere along the way. That mistake is invisible to compilers and most
reviews — the bytes are valid UTF-8 — but it garbles every em dash into three junk characters,
and PR #1065 pulled 219 of those out of shipped UI strings, a published doc,
and test comments. This check exists so the next bad write fails CI instead of
reaching a placeholder.

Findings:
  mojibake   a CP1252 misreading of a UTF-8 lead byte (Â Ã Î Ï â) followed by
             a CP1252 misreading of a continuation byte — a bigram that does
             not occur in intentional text
  U+FFFD     the replacement character: some earlier decode already destroyed
             the original bytes (unrecoverable — fix by hand from context)
  BOM        a UTF-8 byte-order mark at byte 0; tools in this repo treat BOMs
             as damage (a BOM once made the /no-changelog waiver silently skip)

Usage:
  python3 scripts/check_encoding.py          # scan tracked files, exit 1 on findings
  python3 scripts/check_encoding.py --fix    # repair mojibake runs and strip BOMs
  python3 scripts/check_encoding.py FILE...  # scan just the named files
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

# Extensions the scan trusts to be text. Everything else (images, archives,
# fixtures with deliberate byte soup) is out of scope.
TEXT_EXTENSIONS = {
    ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
    ".md", ".mdx", ".py", ".json", ".jsonl", ".yml", ".yaml",
    ".sh", ".ps1", ".psm1", ".css", ".html", ".txt", ".sql",
    ".toml", ".tmpl", ".tf", ".proto", ".graphql", ".env",
}

# CP1252's renderings of UTF-8 lead bytes that begin real-world sequences:
# 0xC2/0xC3 (Latin-1 range), 0xCE/0xCF (Greek), 0xE2 (general punctuation --
# em dashes, arrows, ellipses, box drawing).
_TWO_BYTE_LEADS = "ÂÃÎÏ"
_THREE_BYTE_LEAD = "â"  # CP1252's reading of 0xE2

# CP1252's renderings of UTF-8 continuation bytes 0x80-0xBF, derived rather
# than typed: five of those bytes are unmapped in CP1252 (a misread containing
# one would have failed instead of producing mojibake), and hand-listing the
# rest is exactly the kind of write this script polices.
_CONTINUATIONS = "".join(
    bytes([b]).decode("cp1252")
    for b in range(0x80, 0xC0)
    if b not in (0x81, 0x8D, 0x8F, 0x90, 0x9D)
)

_CONT = f"[{re.escape(_CONTINUATIONS)}]"
MOJIBAKE = re.compile(f"[{re.escape(_TWO_BYTE_LEADS + _THREE_BYTE_LEAD)}]{_CONT}")

# One damaged character's worth of text: a two-byte misreading (lead + one
# continuation) or a three-byte one (0xE2's + two). Repairing match-by-match
# keeps adjacent legitimate characters -- even ones from the continuation set,
# like a real ellipsis beside a mojibake em dash -- out of the re-decode.
_FIXABLE = re.compile(
    f"{re.escape(_THREE_BYTE_LEAD)}{_CONT}{{2}}|[{re.escape(_TWO_BYTE_LEADS)}]{_CONT}"
)

BOM = "﻿"


def tracked_files() -> list[Path]:
    out = subprocess.run(
        ["git", "ls-files", "-z"], check=True, capture_output=True
    ).stdout.decode("utf-8")
    return [Path(p) for p in out.split("\0") if p]


# Lines carrying this marker are exempt — for text that talks *about* encoding
# damage: a doc comment illustrating mojibake, a test asserting U+FFFD output.
ALLOW_PRAGMA = "encoding-check:allow"


def scan_text(text: str) -> list[tuple[int, str]]:
    """Return (1-based line, description) findings for one file's content."""
    findings: list[tuple[int, str]] = []
    if text.startswith(BOM):
        findings.append((1, "UTF-8 BOM at start of file"))
    lines = text.splitlines()

    def allowed(line: int) -> bool:
        return ALLOW_PRAGMA in lines[line - 1]

    for match in MOJIBAKE.finditer(text):
        line = text.count("\n", 0, match.start()) + 1
        if allowed(line):
            continue
        pair = " ".join(f"U+{ord(c):04X}" for c in match.group(0))
        try:
            meant = match.group(0).encode("cp1252").decode("utf-8")
            hint = f" (decodes to {meant!r})"
        except (UnicodeEncodeError, UnicodeDecodeError):
            hint = ""
        findings.append((line, f"mojibake bigram [{pair}]{hint}"))
    for match in re.finditer("\N{REPLACEMENT CHARACTER}", text):
        line = text.count("\n", 0, match.start()) + 1
        if allowed(line):
            continue
        findings.append((line, "U+FFFD replacement character (original bytes lost)"))
    return findings


def fix_text(text: str) -> str:
    """Strip a leading BOM and re-decode each mojibake match individually."""
    if text.startswith(BOM):
        text = text[len(BOM):]

    def repair(match: re.Match[str]) -> str:
        try:
            return match.group(0).encode("cp1252").decode("utf-8")
        except (UnicodeEncodeError, UnicodeDecodeError):
            return match.group(0)

    return "\n".join(
        line if ALLOW_PRAGMA in line else _FIXABLE.sub(repair, line)
        for line in text.split("\n")
    )


def main() -> int:
    # Windows consoles default to a legacy codepage; the findings this tool
    # prints are exactly the characters such a console cannot encode.
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("files", nargs="*", help="scan only these files")
    parser.add_argument("--fix", action="store_true", help="repair findings in place")
    args = parser.parse_args()

    paths = [Path(f) for f in args.files] if args.files else tracked_files()
    total = 0
    for path in paths:
        if path.suffix.lower() not in TEXT_EXTENSIONS or not path.is_file():
            continue
        raw = path.read_bytes()
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError as e:
            print(f"{path}:0: not valid UTF-8 ({e.reason} at byte {e.start})")
            total += 1
            continue
        findings = scan_text(text)
        if not findings:
            continue
        if args.fix:
            fixed = fix_text(text)
            remaining = scan_text(fixed)
            path.write_bytes(fixed.encode("utf-8"))
            repaired = len(findings) - len(remaining)
            print(f"{path}: repaired {repaired} finding(s)", end="")
            print(f", {len(remaining)} need hand-fixing" if remaining else "")
            total += len(remaining)
        else:
            for line, desc in findings:
                print(f"{path}:{line}: {desc}")
            total += len(findings)

    if total and not args.fix:
        print(
            f"\n{total} finding(s). `python3 scripts/check_encoding.py --fix` repairs "
            "mojibake and BOMs; U+FFFD needs the original text.",
            file=sys.stderr,
        )
    return 1 if total else 0


if __name__ == "__main__":
    sys.exit(main())
